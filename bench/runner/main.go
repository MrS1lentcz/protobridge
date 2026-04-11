// Benchmark runner for isolated Docker-based testing.
// Sends parallel HTTP requests to the REST proxy and reports throughput,
// latency percentiles, and error rate.
package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	target := os.Getenv("TARGET_URL")
	if target == "" {
		target = "http://localhost:8080"
	}

	fmt.Println("=== protobridge benchmark ===")
	fmt.Printf("target: %s\n\n", target)

	// Wait for proxy to be ready.
	waitForHealthy(target+"/healthz", 30*time.Second)

	// Benchmark 1: Health endpoint (baseline).
	runBench("GET /healthz (baseline)", 10000, 50, func() (*http.Response, error) {
		return http.Get(target + "/healthz")
	})

	// Benchmark 2: Unary POST without auth.
	body := []byte(`{"name":"bench-item","priority":"PRIORITY_HIGH"}`)
	runBench("POST /items/noauth (unary, no auth)", 10000, 50, func() (*http.Response, error) {
		return http.Post(target+"/items/noauth", "application/json", bytes.NewReader(body))
	})

	// Benchmark 3: Unary POST with auth.
	runBench("POST /items (unary, with auth)", 10000, 50, func() (*http.Response, error) {
		req, _ := http.NewRequest("POST", target+"/items", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "bench-user")
		req.Header.Set("user_id", "bench-user")
		return http.DefaultClient.Do(req)
	})

	// Benchmark 4: High concurrency (200 workers).
	runBench("POST /items/noauth (200 concurrent)", 20000, 200, func() (*http.Response, error) {
		return http.Post(target+"/items/noauth", "application/json", bytes.NewReader(body))
	})
}

func runBench(name string, total, concurrency int, doReq func() (*http.Response, error)) {
	fmt.Printf("--- %s ---\n", name)
	fmt.Printf("requests: %d, concurrency: %d\n", total, concurrency)

	var (
		errors   atomic.Int64
		latencies = make([]time.Duration, total)
		mu        sync.Mutex
		idx       atomic.Int64
	)

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	start := time.Now()

	for i := 0; i < total; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			t0 := time.Now()
			resp, err := doReq()
			d := time.Since(t0)

			i := idx.Add(1) - 1
			mu.Lock()
			latencies[i] = d
			mu.Unlock()

			if err != nil {
				errors.Add(1)
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()

			if resp.StatusCode >= 400 {
				errors.Add(1)
			}
		}()
	}

	wg.Wait()
	elapsed := time.Since(start)

	// Sort latencies for percentiles.
	actual := latencies[:idx.Load()]
	sort.Slice(actual, func(i, j int) bool { return actual[i] < actual[j] })

	rps := float64(total) / elapsed.Seconds()
	errRate := float64(errors.Load()) / float64(total) * 100

	fmt.Printf("duration:    %s\n", elapsed.Round(time.Millisecond))
	fmt.Printf("throughput:  %.0f req/s\n", rps)
	fmt.Printf("errors:      %d (%.1f%%)\n", errors.Load(), errRate)
	if len(actual) > 0 {
		fmt.Printf("latency p50: %s\n", actual[len(actual)*50/100])
		fmt.Printf("latency p95: %s\n", actual[len(actual)*95/100])
		fmt.Printf("latency p99: %s\n", actual[len(actual)*99/100])
	}
	fmt.Println()
}

func waitForHealthy(url string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			fmt.Println("proxy is healthy")
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	fmt.Println("WARNING: proxy did not become healthy, proceeding anyway")
}

// Benchmark runner for isolated Docker-based testing.
// Sends parallel HTTP requests and reports throughput, latency, errors.
// Writes results to /results/benchmark.txt if the directory exists.
package main

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// HTTP client tuned for high concurrency benchmarking.
var client = &http.Client{
	Transport: &http.Transport{
		MaxIdleConns:        20000,
		MaxIdleConnsPerHost: 20000,
		MaxConnsPerHost:     0, // unlimited
		IdleConnTimeout:     90 * time.Second,
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	},
	Timeout: 30 * time.Second,
}

var output io.Writer = os.Stdout

func main() {
	target := os.Getenv("TARGET_URL")
	if target == "" {
		target = "http://localhost:8080"
	}

	// Write results to file if /results exists.
	if info, err := os.Stat("/results"); err == nil && info.IsDir() {
		f, err := os.Create("/results/benchmark.txt")
		if err == nil {
			defer f.Close()
			output = io.MultiWriter(os.Stdout, f)
		}
	}

	p := func(format string, args ...any) { fmt.Fprintf(output, format, args...) }

	p("=== protobridge benchmark ===\n")
	p("target: %s\n\n", target)

	waitForHealthy(target + "/healthz")

	body := []byte(`{"name":"bench-item","priority":"PRIORITY_HIGH"}`)

	// Baseline.
	runBench(p, "GET /healthz (baseline)", 10000, 50, func() int {
		resp, err := client.Get(target + "/healthz")
		if err != nil {
			return -1
		}
		code := resp.StatusCode
		resp.Body.Close()
		return code
	})

	// Unary no auth.
	runBench(p, "POST /items/noauth (unary, no auth)", 10000, 50, func() int {
		resp, err := client.Post(target+"/items/noauth", "application/json", bytes.NewReader(body))
		if err != nil {
			return -1
		}
		code := resp.StatusCode
		resp.Body.Close()
		return code
	})

	// Unary with auth.
	runBench(p, "POST /items (unary, with auth)", 10000, 50, func() int {
		req, _ := http.NewRequest("POST", target+"/items", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "bench-user")
		req.Header.Set("user_id", "bench-user")
		resp, err := client.Do(req)
		if err != nil {
			return -1
		}
		code := resp.StatusCode
		resp.Body.Close()
		return code
	})

	// Ramping concurrency.
	for _, conc := range []int{100, 500, 1000, 5000, 10000} {
		runBench(p, fmt.Sprintf("POST /items/noauth (%d concurrent)", conc), 50000, conc, func() int {
			resp, err := client.Post(target+"/items/noauth", "application/json", bytes.NewReader(body))
			if err != nil {
				return -1
			}
			code := resp.StatusCode
			resp.Body.Close()
			return code
		})
	}
}

func runBench(p func(string, ...any), name string, total, concurrency int, doReq func() int) {
	p("--- %s ---\n", name)
	p("requests: %d, concurrency: %d\n", total, concurrency)

	latencies := make([]int64, total) // nanoseconds
	var errors atomic.Int64
	var idx atomic.Int64

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
			code := doReq()
			ns := time.Since(t0).Nanoseconds()

			j := idx.Add(1) - 1
			latencies[j] = ns

			if code < 0 || code >= 400 {
				errors.Add(1)
			}
		}()
	}

	wg.Wait()
	elapsed := time.Since(start)

	n := int(idx.Load())
	sorted := make([]int64, n)
	copy(sorted, latencies[:n])
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	rps := float64(total) / elapsed.Seconds()

	p("duration:    %s\n", elapsed.Round(time.Millisecond))
	p("throughput:  %.0f req/s\n", rps)
	p("errors:      %d / %d (%.1f%%)\n", errors.Load(), total, float64(errors.Load())/float64(total)*100)
	if n > 0 {
		p("latency p50: %s\n", time.Duration(sorted[n*50/100]))
		p("latency p95: %s\n", time.Duration(sorted[n*95/100]))
		p("latency p99: %s\n", time.Duration(sorted[n*99/100]))
		p("latency max: %s\n", time.Duration(sorted[n-1]))
	}
	p("\n")
}

func waitForHealthy(url string) {
	for i := 0; i < 60; i++ {
		resp, err := client.Get(url)
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			fmt.Fprintf(output, "proxy is healthy\n\n")
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	fmt.Fprintf(output, "WARNING: proxy not healthy after 30s\n\n")
}

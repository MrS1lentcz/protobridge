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
		MaxConnsPerHost:     0,
		IdleConnTimeout:     90 * time.Second,
		DisableKeepAlives:   false,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
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
	runBench(p, "GET /healthz (baseline)", 10000, 50, func() (int, string) {
		resp, err := client.Get(target + "/healthz")
		if err != nil {
			return -1, errType(err)
		}
		code := resp.StatusCode
		resp.Body.Close()
		return code, ""
	})

	// Unary no auth.
	runBench(p, "POST /items/noauth (unary, no auth)", 10000, 50, func() (int, string) {
		resp, err := client.Post(target+"/items/noauth", "application/json", bytes.NewReader(body))
		if err != nil {
			return -1, errType(err)
		}
		code := resp.StatusCode
		resp.Body.Close()
		return code, ""
	})

	// Unary with auth.
	runBench(p, "POST /items (unary, with auth)", 10000, 50, func() (int, string) {
		req, _ := http.NewRequest("POST", target+"/items", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "bench-user")
		req.Header.Set("user_id", "bench-user")
		resp, err := client.Do(req)
		if err != nil {
			return -1, errType(err)
		}
		code := resp.StatusCode
		resp.Body.Close()
		return code, ""
	})

	// Ramping concurrency.
	for _, conc := range []int{100, 500, 1000, 5000, 10000} {
		runBench(p, fmt.Sprintf("POST /items/noauth (%d concurrent)", conc), 50000, conc, func() (int, string) {
			resp, err := client.Post(target+"/items/noauth", "application/json", bytes.NewReader(body))
			if err != nil {
				return -1, errType(err)
			}
			code := resp.StatusCode
			resp.Body.Close()
			return code, ""
		})
	}
}

type benchStats struct {
	latencies  []int64
	connErrors atomic.Int64
	http4xx    atomic.Int64
	http5xx    atomic.Int64
	idx        atomic.Int64
	errTypes   sync.Map // string → *atomic.Int64
}

func (s *benchStats) recordError(errClass string) {
	if errClass == "" {
		return
	}
	val, _ := s.errTypes.LoadOrStore(errClass, &atomic.Int64{})
	val.(*atomic.Int64).Add(1)
}

func runBench(p func(string, ...any), name string, total, concurrency int, doReq func() (int, string)) {
	p("--- %s ---\n", name)
	p("requests: %d, concurrency: %d\n", total, concurrency)

	stats := &benchStats{
		latencies: make([]int64, total),
	}

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
			code, errClass := doReq()
			ns := time.Since(t0).Nanoseconds()

			j := stats.idx.Add(1) - 1
			stats.latencies[j] = ns

			switch {
			case code < 0:
				stats.connErrors.Add(1)
				stats.recordError(errClass)
			case code >= 500:
				stats.http5xx.Add(1)
				stats.recordError(fmt.Sprintf("HTTP %d", code))
			case code >= 400:
				stats.http4xx.Add(1)
				stats.recordError(fmt.Sprintf("HTTP %d", code))
			}
		}()
	}

	wg.Wait()
	elapsed := time.Since(start)

	n := int(stats.idx.Load())
	sorted := make([]int64, n)
	copy(sorted, stats.latencies[:n])
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	totalErrors := stats.connErrors.Load() + stats.http4xx.Load() + stats.http5xx.Load()
	rps := float64(total) / elapsed.Seconds()

	p("duration:    %s\n", elapsed.Round(time.Millisecond))
	p("throughput:  %.0f req/s\n", rps)
	p("success:     %d / %d (%.1f%%)\n", int64(total)-totalErrors, total, float64(int64(total)-totalErrors)/float64(total)*100)
	if totalErrors > 0 {
		p("errors:      %d total\n", totalErrors)
		if stats.connErrors.Load() > 0 {
			p("  conn err:  %d\n", stats.connErrors.Load())
		}
		if stats.http4xx.Load() > 0 {
			p("  4xx:       %d\n", stats.http4xx.Load())
		}
		if stats.http5xx.Load() > 0 {
			p("  5xx:       %d\n", stats.http5xx.Load())
		}
		// Top error types.
		stats.errTypes.Range(func(key, value any) bool {
			p("  - %s: %d\n", key.(string), value.(*atomic.Int64).Load())
			return true
		})
	}
	if n > 0 {
		p("latency p50: %s\n", time.Duration(sorted[n*50/100]))
		p("latency p95: %s\n", time.Duration(sorted[n*95/100]))
		p("latency p99: %s\n", time.Duration(sorted[n*99/100]))
		p("latency max: %s\n", time.Duration(sorted[n-1]))
	}
	p("\n")
}

// errType classifies a connection error.
func errType(err error) string {
	if err == nil {
		return ""
	}
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		return "timeout"
	}
	if opErr, ok := err.(*net.OpError); ok {
		return fmt.Sprintf("net: %s", opErr.Op)
	}
	return "connection"
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

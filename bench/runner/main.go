// Benchmark runner using fasthttp for minimal CPU/memory overhead.
// The runner should NOT be the bottleneck – it fires requests as fast
// as possible and only reads status codes.
package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/valyala/fasthttp"
)

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
	p("target: %s\n", target)
	p("runner: fasthttp (zero-alloc)\n\n")

	waitForHealthy(target + "/healthz")

	body := []byte(`{"name":"bench-item","priority":"PRIORITY_HIGH"}`)

	// Warm-up: let connection pool scale up.
	p("--- warm-up ---\n")
	runBench(p, "warm-up", 5000, 100, func() int {
		return doPost(target+"/items/noauth", body, nil)
	})

	// Measured runs.
	runBench(p, "GET /healthz (baseline)", 50000, 50, func() int {
		return doGet(target + "/healthz")
	})

	runBench(p, "POST /items/noauth (unary, no auth)", 50000, 50, func() int {
		return doPost(target+"/items/noauth", body, nil)
	})

	authHeaders := map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "bench-user",
		"user_id":       "bench-user",
	}
	runBench(p, "POST /items (unary, with auth)", 50000, 50, func() int {
		return doPost(target+"/items", body, authHeaders)
	})

	// Ramping concurrency.
	for _, conc := range []int{100, 500, 1000, 5000, 10000} {
		runBench(p, fmt.Sprintf("POST /items/noauth (%d concurrent)", conc), 100000, conc, func() int {
			return doPost(target+"/items/noauth", body, nil)
		})
	}
}

func doGet(url string) int {
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	req.SetRequestURI(url)
	req.Header.SetMethod("GET")

	if err := fasthttp.Do(req, resp); err != nil {
		return -1
	}
	return resp.StatusCode()
}

func doPost(url string, body []byte, headers map[string]string) int {
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	req.SetRequestURI(url)
	req.Header.SetMethod("POST")
	req.Header.SetContentType("application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.SetBody(body)

	if err := fasthttp.Do(req, resp); err != nil {
		return -1
	}
	return resp.StatusCode()
}

func runBench(p func(string, ...any), name string, total, concurrency int, doReq func() int) {
	p("--- %s ---\n", name)
	p("requests: %d, concurrency: %d\n", total, concurrency)

	latencies := make([]int64, total)
	var connErrors, http4xx, http5xx atomic.Int64
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

			switch {
			case code < 0:
				connErrors.Add(1)
			case code >= 500:
				http5xx.Add(1)
			case code >= 400:
				http4xx.Add(1)
			}
		}()
	}

	wg.Wait()
	elapsed := time.Since(start)

	n := int(idx.Load())
	sorted := make([]int64, n)
	copy(sorted, latencies[:n])
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	totalErrors := connErrors.Load() + http4xx.Load() + http5xx.Load()
	rps := float64(total) / elapsed.Seconds()

	p("duration:    %s\n", elapsed.Round(time.Millisecond))
	p("throughput:  %.0f req/s\n", rps)
	p("success:     %d / %d (%.1f%%)\n", int64(total)-totalErrors, total, float64(int64(total)-totalErrors)/float64(total)*100)
	if totalErrors > 0 {
		p("errors:      %d (conn: %d, 4xx: %d, 5xx: %d)\n",
			totalErrors, connErrors.Load(), http4xx.Load(), http5xx.Load())
	}
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
		if code := doGet(url); code == 200 {
			fmt.Fprintf(output, "proxy is healthy\n\n")
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	fmt.Fprintf(output, "WARNING: proxy not healthy after 30s\n\n")
}

// httpload measures authenticated HTTP work with an open arrival schedule. Run only
// against the task-owned synthetic instance; requests and tokens are never logged.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type scenario struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Body   string `json:"body"`
	Status int    `json:"status"`
}
type config struct {
	Base, Token       string
	Rate, Workers     int
	Duration, Timeout time.Duration
	Requests          []scenario
}
type report struct {
	Offered         int         `json:"offered"`
	Completed       int         `json:"completed"`
	Failed          int         `json:"failed"`
	Dropped         int         `json:"dropped"`
	DurationSeconds float64     `json:"duration_seconds"`
	Rate            int         `json:"offered_rate"`
	Workers         int         `json:"workers"`
	P50MS           float64     `json:"p50_ms"`
	P95MS           float64     `json:"p95_ms"`
	P99MS           float64     `json:"p99_ms"`
	Statuses        map[int]int `json:"statuses"`
}

func validate(c config) error {
	u, err := url.Parse(c.Base)
	if err != nil {
		return errors.New("invalid base URL")
	}
	ip, err := netip.ParseAddr(u.Hostname())
	if err != nil || !ip.IsLoopback() || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("base URL must be an HTTP loopback IP origin")
	}
	if c.Rate < 1 || c.Rate > 10000 || c.Workers < 1 || c.Workers > 256 || c.Duration <= 0 || c.Duration > 10*time.Minute || c.Duration.Seconds()*float64(c.Rate) > 1000000 || c.Timeout <= 0 || c.Timeout > time.Minute || len(c.Requests) == 0 {
		return errors.New("invalid bounded workload")
	}
	for _, r := range c.Requests {
		p, err := url.Parse(r.Path)
		if err != nil || !strings.HasPrefix(r.Path, "/api/v1/") || p.IsAbs() || p.Host != "" || p.Fragment != "" || r.Status < 200 || r.Status > 599 {
			return errors.New("workload must name native API paths and expected statuses")
		}
		switch r.Method {
		case "GET", "POST", "PUT", "PATCH", "DELETE":
		default:
			return errors.New("unsupported method")
		}
	}
	return nil
}
func run(ctx context.Context, c config) (report, error) {
	out := report{Rate: c.Rate, Workers: c.Workers, DurationSeconds: c.Duration.Seconds(), Statuses: map[int]int{}}
	if err := validate(c); err != nil {
		return out, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = c.Workers
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: c.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	type work struct {
		at time.Time
		n  int
	}
	queue := make(chan work, c.Workers)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var elapsed []float64
	for range c.Workers {
		wg.Go(func() {
			for job := range queue {
				spec := c.Requests[job.n%len(c.Requests)]
				req, err := http.NewRequestWithContext(ctx, spec.Method, strings.TrimRight(c.Base, "/")+spec.Path, strings.NewReader(spec.Body))
				status := 0
				if err == nil {
					req.Header.Set("Authorization", "Bearer "+c.Token)
					if spec.Body != "" {
						req.Header.Set("Content-Type", "application/json")
					}
					// A request is a new command; distinct arrivals never share a replay key.
					req.Header.Set("Idempotency-Key", fmt.Sprintf("%08x-%04x-4000-8000-%012x", job.at.Unix()&0xffffffff, job.n&0xffff, job.at.UnixNano()&0xffffffffffff))
					var resp *http.Response
					resp, err = client.Do(req)
					if err == nil {
						status = resp.StatusCode
						_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 8<<20))
						resp.Body.Close()
					}
				}
				ms := float64(time.Since(job.at)) / float64(time.Millisecond)
				mu.Lock()
				out.Completed++
				out.Statuses[status]++
				if err != nil || status != spec.Status {
					out.Failed++
				}
				elapsed = append(elapsed, ms)
				mu.Unlock()
			}
		})
	}
	start := time.Now()
	total := int(c.Duration.Seconds() * float64(c.Rate))
	for n := 0; n < total; n++ {
		scheduled := start.Add(time.Duration(n) * time.Second / time.Duration(c.Rate))
		timer := time.NewTimer(time.Until(scheduled))
		select {
		case <-ctx.Done():
			timer.Stop()
			close(queue)
			wg.Wait()
			return out, ctx.Err()
		case <-timer.C:
		}
		mu.Lock()
		out.Offered++
		mu.Unlock()
		select {
		case queue <- work{scheduled, n}:
		default:
			mu.Lock()
			out.Dropped++
			mu.Unlock()
		}
	}
	close(queue)
	wg.Wait()
	sort.Float64s(elapsed)
	percentile := func(p float64) float64 {
		if len(elapsed) == 0 {
			return 0
		}
		return elapsed[int(float64(len(elapsed)-1)*p)]
	}
	out.P50MS, out.P95MS, out.P99MS = percentile(.5), percentile(.95), percentile(.99)
	return out, nil
}
func main() {
	base := flag.String("url", "http://127.0.0.1:18161", "synthetic instance origin")
	rate := flag.Int("rate", 20, "offered requests per second")
	workers := flag.Int("workers", 8, "maximum concurrent requests")
	duration := flag.Duration("duration", 30*time.Second, "arrival duration")
	workload := flag.String("workload", "", "JSON array of requests; defaults to library reads")
	p99 := flag.Float64("p99-ms", 1000, "failure threshold")
	maxErrors := flag.Float64("max-error-fraction", 0, "unexpected responses plus dropped arrivals / offered")
	flag.Parse()
	c := config{Base: *base, Token: os.Getenv("PASTIME_LOAD_TOKEN"), Rate: *rate, Workers: *workers, Duration: *duration, Timeout: 10 * time.Second, Requests: []scenario{{Method: "GET", Path: "/api/v1/library?limit=30", Status: 200}, {Method: "GET", Path: "/api/v1/library-stats", Status: 200}}}
	if *workload != "" {
		b, err := os.ReadFile(*workload)
		if err != nil {
			fmt.Fprintln(os.Stderr, "cannot read workload")
			os.Exit(2)
		}
		if json.Unmarshal(b, &c.Requests) != nil {
			fmt.Fprintln(os.Stderr, "invalid workload JSON")
			os.Exit(2)
		}
	}
	if c.Token == "" || *p99 <= 0 || *maxErrors < 0 || *maxErrors > 1 {
		fmt.Fprintln(os.Stderr, "a synthetic API token and valid thresholds are required")
		os.Exit(2)
	}
	result, err := run(context.Background(), c)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	json.NewEncoder(os.Stdout).Encode(result)
	if result.Offered == 0 || result.P99MS > *p99 || float64(result.Failed+result.Dropped)/float64(result.Offered) > *maxErrors {
		os.Exit(1)
	}
}

package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const maxBody = 4 << 20

// Client is the only path adapters use to reach the network. It bounds response size,
// per-host concurrency and retries; the caller's context supplies the overall deadline.
type Client struct {
	HTTP      *http.Client
	UserAgent string
	mu        sync.Mutex
	hosts     map[string]chan struct{}
	stats     map[string]*HostStats
}

// HostStats is the in-process health of one provider host since the server started.
type HostStats struct {
	Host         string    `json:"host"`
	Requests     int64     `json:"requests"`
	Failures     int64     `json:"failures"`
	RateLimited  int64     `json:"rate_limited"`
	TotalLatency int64     `json:"total_latency_ms"`
	LastStatus   int       `json:"last_status"`
	LastAt       time.Time `json:"last_at"`
	LastFailure  time.Time `json:"last_failure_at"`
}

// Stats returns a snapshot of every host contacted so far.
func (c *Client) Stats() []HostStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]HostStats, 0, len(c.stats))
	for _, h := range c.stats {
		out = append(out, *h)
	}
	return out
}

func (c *Client) record(host string, status int, took time.Duration, failed bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	h, ok := c.stats[host]
	if !ok {
		h = &HostStats{Host: host}
		c.stats[host] = h
	}
	now := time.Now()
	h.Requests++
	h.TotalLatency += took.Milliseconds()
	h.LastStatus, h.LastAt = status, now
	if status == 429 {
		h.RateLimited++
	}
	if failed {
		h.Failures++
		h.LastFailure = now
	}
}

func NewClient(userAgent string) *Client {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment, MaxIdleConnsPerHost: 8, IdleConnTimeout: 90 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second, ForceAttemptHTTP2: true,
	}
	return &Client{
		HTTP: &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 || req.URL.Scheme != "https" {
				return errors.New("redirect refused")
			}
			return nil
		}},
		UserAgent: userAgent, hosts: map[string]chan struct{}{}, stats: map[string]*HostStats{},
	}
}

func (c *Client) slot(host string) chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch, ok := c.hosts[host]
	if !ok {
		ch = make(chan struct{}, 4)
		c.hosts[host] = ch
	}
	return ch
}

// Request describes one provider call. Body, when set, is sent as JSON.
type Request struct {
	Method  string
	URL     string
	Header  http.Header
	Body    any
	RawBody []byte
	// ContentType applies to RawBody; JSON bodies always use application/json.
	ContentType string
	Accept      string
	Retries     int
}

// Do performs the call and returns the bounded response body.
func (c *Client) Do(ctx context.Context, r Request) ([]byte, error) {
	if r.Method == "" {
		r.Method = http.MethodGet
	}
	payload := r.RawBody
	if r.Body != nil {
		var err error
		if payload, err = json.Marshal(r.Body); err != nil {
			return nil, err
		}
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, r.Method, r.URL, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		for k, v := range r.Header {
			req.Header[k] = v
		}
		req.Header.Set("User-Agent", c.UserAgent)
		if r.Accept == "" {
			r.Accept = "application/json"
		}
		req.Header.Set("Accept", r.Accept)
		if r.Body != nil {
			req.Header.Set("Content-Type", "application/json")
		} else if payload != nil {
			req.Header.Set("Content-Type", r.ContentType)
		}
		slot := c.slot(req.URL.Host)
		select {
		case slot <- struct{}{}:
		case <-ctx.Done():
			return nil, ErrUnavailable
		}
		began := time.Now()
		body, status, retryAfter, err := c.once(req)
		<-slot
		c.record(req.URL.Host, status, time.Since(began), err != nil || status >= 500 || status == 429 || status == 401 || status == 403)
		if errors.Is(err, ErrInvalidPayload) {
			return nil, err
		}
		if err != nil {
			if ctx.Err() != nil || attempt >= r.Retries {
				return nil, ErrUnavailable
			}
		} else {
			switch {
			case status >= 200 && status < 300:
				return body, nil
			case status == 404:
				return nil, ErrNotFound
			case status == 401 || status == 403:
				return nil, fmt.Errorf("%w: status %d", ErrNotConfigured, status)
			case status == 429 && attempt >= r.Retries:
				return nil, ErrRateLimited
			case status < 500 && status != 429:
				return nil, fmt.Errorf("%w: status %d", ErrInvalidPayload, status)
			case attempt >= r.Retries:
				return nil, ErrUnavailable
			}
		}
		wait := time.Duration(attempt+1) * 500 * time.Millisecond
		if retryAfter > 0 && retryAfter <= 5*time.Second {
			wait = retryAfter
		}
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return nil, ErrUnavailable
		}
	}
}

func (c *Client) once(req *http.Request) ([]byte, int, time.Duration, error) {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, 0, 0, err
	}
	defer resp.Body.Close()
	var retryAfter time.Duration
	if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
		retryAfter = time.Duration(s) * time.Second
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, resp.StatusCode, retryAfter, err
	}
	if len(body) > maxBody {
		return nil, resp.StatusCode, retryAfter, ErrInvalidPayload
	}
	return body, resp.StatusCode, retryAfter, nil
}

// JSON performs the request and decodes a JSON response into out.
func (c *Client) JSON(ctx context.Context, r Request, out any) error {
	body, err := c.Do(ctx, r)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return ErrInvalidPayload
	}
	return nil
}

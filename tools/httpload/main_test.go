package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOpenArrivalsCountOverloadAndFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(30 * time.Millisecond); w.WriteHeader(503) }))
	defer srv.Close()
	c := config{Base: srv.URL, Rate: 200, Workers: 1, Duration: 150 * time.Millisecond, Timeout: time.Second, Requests: []scenario{{Method: "GET", Path: "/api/v1/library", Status: 200}}}
	got, err := run(context.Background(), c)
	if err != nil || got.Offered != 30 || got.Dropped == 0 || got.Failed != got.Completed || got.Completed+got.Dropped != got.Offered || got.P99MS < 30 {
		t.Fatal(got, err)
	}
	c.Base = "http://example.invalid"
	if validate(c) == nil {
		t.Fatal("external target permitted")
	}
}

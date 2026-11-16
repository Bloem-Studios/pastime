package cache

import (
	"context"
	"crypto/rand"
	"fmt"
	"net"
	"net/url"
	"os"
	"testing"
	"time"
)

func TestOptionalAndInvalidConfiguration(t *testing.T) {
	c, err := Open("")
	if c != nil || err != nil {
		t.Fatal("unconfigured cache should be optional")
	}
	if _, err := Open("https://secret@example.com"); err == nil {
		t.Fatal("invalid protocol accepted")
	}
}

func TestValkeyRoundTrip(t *testing.T) {
	raw := os.Getenv("PASTIME_TEST_VALKEY_URL")
	if raw == "" {
		if os.Getenv("PASTIME_REQUIRE_INTEGRATION") == "1" {
			t.Fatal("Valkey integration URL is required")
		}
		t.Skip("set PASTIME_TEST_VALKEY_URL")
	}
	u, err := url.Parse(raw)
	if err != nil || u == nil || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") {
		t.Fatal("Valkey integration endpoint must be loopback")
	}
	c, err := Open(raw)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.Ping(ctx).Err(); err != nil {
		t.Fatal("Valkey ping failed")
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprintf("test:pastime:%x", id)
	if err := c.Set(ctx, key, "fixture", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	defer c.Del(context.Background(), key)
	value, err := c.Get(ctx, key).Result()
	if err != nil || value != "fixture" {
		t.Fatalf("roundtrip: %q %v", value, err)
	}
	if err := c.Del(ctx, key).Err(); err != nil {
		t.Fatal(err)
	}
}

func TestUnavailableValkeyIsBounded(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	c, err := Open("redis://" + addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := c.Ping(ctx).Err(); err == nil {
		t.Fatal("unavailable cache reported healthy")
	}
	if time.Since(start) > time.Second {
		t.Fatal("cache failure exceeded budget")
	}
}

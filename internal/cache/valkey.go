// Package cache provides optional Redis-protocol connectivity to Valkey.
package cache

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

func Open(rawURL string) (*redis.Client, error) {
	if rawURL == "" {
		return nil, nil
	}
	opts, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, errors.New("invalid Valkey configuration")
	}
	opts.Protocol = 2
	opts.ClientName = "pastime"
	opts.PoolSize = 4
	opts.MaxActiveConns = 4
	opts.MinIdleConns = 0
	opts.MaxRetries = -1
	opts.DialerRetries = 1
	opts.DialTimeout = 250 * time.Millisecond
	opts.ReadTimeout = 250 * time.Millisecond
	opts.WriteTimeout = 250 * time.Millisecond
	opts.PoolTimeout = 100 * time.Millisecond
	opts.ContextTimeoutEnabled = true
	return redis.NewClient(opts), nil
}

// Bytes is a failure-tolerant byte cache. Every error is a miss; Valkey never decides
// correctness, so an outage only costs recomputation.
type Bytes struct{ Client *redis.Client }

func (b Bytes) Get(ctx context.Context, key string) ([]byte, bool) {
	if b.Client == nil {
		return nil, false
	}
	data, err := b.Client.Get(ctx, key).Bytes()
	return data, err == nil
}

func (b Bytes) Set(ctx context.Context, key string, value []byte, ttl time.Duration) {
	if b.Client != nil {
		_ = b.Client.Set(ctx, key, value, ttl).Err()
	}
}

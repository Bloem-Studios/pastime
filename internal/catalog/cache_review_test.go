package catalog

import (
	"context"
	"testing"
	"time"
)

func TestSharedFetchSurvivesFirstCallerCancellation(t *testing.T) {
	s := &Service{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	fetch := func(ctx context.Context) (string, error) {
		close(started)
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-release:
			return "result", nil
		}
	}
	go func() { _, err := cached(s, ctx, "synthetic", time.Minute, fetch); done <- err }()
	<-started
	shared := s.group.DoChan("synthetic", func() (any, error) { return "unexpected second fetch", nil })
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled caller remained blocked")
	}
	close(release)
	result := <-shared
	if result.Err != nil || result.Val != "result" {
		t.Fatalf("shared result lost: %v %v", result.Val, result.Err)
	}
}

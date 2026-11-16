package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"
)

type Handler func(context.Context, json.RawMessage) error

// Run uses bounded concurrency and polling; Valkey wakeups can be added later
// without changing the durability boundary. Only registered, reviewed handlers execute.
func Run(ctx context.Context, store Store, concurrency int, handlers map[string]Handler, logger *slog.Logger) {
	var wg sync.WaitGroup
	for lane := range concurrency {
		wg.Go(func() {
			timer := time.NewTimer(0)
			defer timer.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-timer.C:
				}
				if _, err := store.Recover(ctx); err != nil {
					if ctx.Err() == nil {
						logger.Error("job recovery unavailable")
					}
					timer.Reset(time.Second)
					continue
				}
				var kinds []string
				if lane == 0 && concurrency > 1 {
					kinds = []string{"scrobble", "listens", "runtime_probe"}
				}
				job, err := store.ClaimKinds(ctx, 30*time.Second, kinds)
				if err != nil {
					if ctx.Err() == nil {
						logger.Error("job claim unavailable")
					}
					timer.Reset(time.Second)
					continue
				}
				if job == nil {
					timer.Reset(500 * time.Millisecond)
					continue
				}
				execute(ctx, store, *job, handlers, logger)
				timer.Reset(0)
			}
		})
	}
	wg.Wait()
}

func execute(ctx context.Context, store Store, j Job, handlers map[string]Handler, logger *slog.Logger) {
	if j.PayloadVersion != 1 {
		_ = store.block(ctx, j, "payload_version_unsupported")
		return
	}
	handler, exists := handlers[j.Kind]
	if !exists {
		// An unregistered kind may require another worker version. Block instead
		// of pretending it ran or draining attempts in a hot retry loop.
		_, err := store.Pool.Exec(ctx, `UPDATE jobs SET status='blocked', lease_token=NULL, lease_until=NULL,
			last_error_code='handler_unavailable', updated_at=clock_timestamp()
			WHERE id=$1 AND lease_token=$2 AND status='running' AND lease_until>clock_timestamp()`, j.ID, j.LeaseToken)
		if err != nil && ctx.Err() == nil {
			logger.Error("could not block unknown job", "job_id", j.ID)
		}
		return
	}
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		tick := time.NewTicker(10 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-work.Done():
				return
			case <-tick.C:
				if err := store.Heartbeat(work, j, 30*time.Second); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	err := handler(work, j.Payload)
	lostOrCanceled := work.Err() != nil
	cancel()
	<-stopped
	if lostOrCanceled || ctx.Err() != nil {
		return
	}
	if errors.Is(err, ErrInvalidPayload) {
		err = store.block(ctx, j, "invalid_payload")
	} else if err != nil {
		err = store.Fail(ctx, j, "handler_failed")
	} else {
		err = store.Complete(ctx, j)
	}
	if err != nil && ctx.Err() == nil {
		logger.Warn("job outcome not recorded", "job_id", j.ID)
	}
}

func (s Store) block(ctx context.Context, j Job, code string) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE jobs SET status='blocked',lease_token=NULL,lease_until=NULL,last_error_code=$3,updated_at=clock_timestamp() WHERE id=$1 AND lease_token=$2 AND status='running' AND lease_until>clock_timestamp()`, j.ID, j.LeaseToken, code)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

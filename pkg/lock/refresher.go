package lock

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/kalbasit/ncps/pkg/analytics"
)

// StartRefresher starts a background goroutine that periodically extends the
// TTL of a lock to prevent it from expiring during long-running operations.
//
// The refresh interval is set to ttl*2/3 so that the lock is refreshed before
// it expires, leaving a buffer of ttl/3 for latency and retries.
//
// The returned stop function is idempotent: calling it multiple times is safe.
// The goroutine exits when stop() is called or ctx is cancelled.
//
// If Extend returns an error (e.g. lock already expired), a warning is logged
// but the refresher keeps retrying on the next interval.
func StartRefresher(ctx context.Context, locker Locker, key string, ttl time.Duration) (stop func()) {
	interval := ttl * 2 / 3

	var once sync.Once

	stopCh := make(chan struct{})

	stop = func() {
		once.Do(func() { close(stopCh) })
	}

	analytics.SafeGo(ctx, func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-stopCh:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := locker.Extend(ctx, key); err != nil {
					zerolog.Ctx(ctx).Warn().
						Err(err).
						Str("key", key).
						Dur("ttl", ttl).
						Msg("lock refresher: failed to extend lock TTL")
				}
			}
		}
	})

	return stop
}

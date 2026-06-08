package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog"

	"github.com/kalbasit/ncps/pkg/analytics"
)

// shutdownContext returns a context that is cancelled when the cache's Close()
// runs (shutdownCh closed), so background DB/storage cleanup cannot outlive
// shutdown and block Close() indefinitely. The caller MUST invoke the returned
// cancel to release the watcher goroutine. When shutdownCh is nil (a Cache built
// without New()), it degrades to a plain cancellable context.
func (c *Cache) shutdownContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())

	if c.shutdownCh == nil {
		return ctx, cancel
	}

	go func() {
		select {
		case <-c.shutdownCh:
			cancel()
		case <-ctx.Done():
		}
	}()

	return ctx, cancel
}

// reclaimStaging is terminal cleanup: it deletes the staging part-objects AND the
// staging_state record for hash. It is used by the GC sweep and the
// post-completion reclaim, where the staging lifecycle is over, so the row must be
// removed (resetting it to "requested" instead would make the sweep resurrect it
// forever). The takeover path uses resetStagingState instead, which preserves the
// row so a persisting cross-pod waiter is re-served. It is idempotent: deleting
// absent parts / records is a no-op.
func (c *Cache) reclaimStaging(ctx context.Context, hash string) error {
	if err := c.narStore.DeleteStagingParts(ctx, hash); err != nil {
		return fmt.Errorf("reclaim staging parts for %q: %w", hash, err)
	}

	if err := c.deleteStagingState(ctx, hash); err != nil {
		return fmt.Errorf("reclaim staging state for %q: %w", hash, err)
	}

	return nil
}

// resetStagingForTakeover discards a dead holder's partial (truncated) staging
// part-objects and resets staging_state to "requested" so the taking-over holder
// re-stages from zero if a cross-pod waiter still wants it. Unlike reclaimStaging
// it preserves the row: deleting it would drop the waiter's request marker (the
// waiter records it once, before its poll loop) and strand it on a full re-download.
func (c *Cache) resetStagingForTakeover(ctx context.Context, hash string) error {
	if err := c.narStore.DeleteStagingParts(ctx, hash); err != nil {
		return fmt.Errorf("reset staging parts on takeover for %q: %w", hash, err)
	}

	if err := c.resetStagingState(ctx, hash); err != nil {
		return fmt.Errorf("reset staging state on takeover for %q: %w", hash, err)
	}

	return nil
}

// scheduleStagingReclaim reclaims the staging artifacts for hash after the
// retention grace elapses, giving any in-flight cross-pod readers time to drain.
// It is the event-driven path, launched once the holder finishes staging (the
// final representation is now committed). The grace wait is interrupted by Close()
// so shutdown is not delayed; anything skipped on shutdown is caught by the sweep.
func (c *Cache) scheduleStagingReclaim(hash string) {
	grace := c.InflightStagingRetention()

	c.backgroundWG.Add(1)

	analytics.SafeGo(context.Background(), func() {
		defer c.backgroundWG.Done()

		// A shutdown-bound context interrupts both the grace wait and the reclaim
		// itself, so a stalled DeleteStagingParts/resetStagingState cannot keep
		// Close() blocked on backgroundWG.
		ctx, cancel := c.shutdownContext()
		defer cancel()

		if grace > 0 {
			timer := time.NewTimer(grace)
			defer timer.Stop()

			select {
			case <-timer.C:
			case <-ctx.Done():
				return
			}
		}

		if err := c.reclaimStaging(ctx, hash); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}

			zerolog.Ctx(context.Background()).Warn().
				Err(err).
				Str("hash", hash).
				Msg("failed to reclaim in-flight staging artifacts after grace")
		}
	})
}

// sweepStagingGC reclaims staging records that the event-driven path missed:
//   - completed staging whose retention grace has elapsed (a defensive backstop
//     for the event-driven reclaim), and
//   - orphaned staging whose holder died and was never taken over, detected by
//     updated_at staleness exceeding orphanAge (a live holder advances
//     parts_available — and thus updated_at — as it stages, so staleness, not
//     age, is the liveness signal; see the #1230 lesson on age-as-death proxies).
//
// It returns the number of records reclaimed.
func (c *Cache) sweepStagingGC(ctx context.Context, retention, orphanAge time.Duration) (int, error) {
	rows, err := c.dbClient.Ent().StagingState.Query().All(ctx)
	if err != nil {
		return 0, fmt.Errorf("staging GC: list staging_state: %w", err)
	}

	now := time.Now()
	reclaimed := 0

	for _, st := range rows {
		// updated_at advances as a live holder stages; fall back to created_at when
		// the row has never been updated (a bare request marker).
		ref := st.CreatedAt
		if st.UpdatedAt != nil {
			ref = *st.UpdatedAt
		}

		stale := now.Sub(ref)

		var due bool
		if st.Status == stagingStatusComplete {
			due = stale > retention
		} else {
			due = stale > orphanAge
		}

		if !due {
			continue
		}

		if err := c.reclaimStaging(ctx, st.Hash); err != nil {
			zerolog.Ctx(ctx).Warn().
				Err(err).
				Str("hash", st.Hash).
				Msg("staging GC: failed to reclaim a staging record")

			continue
		}

		reclaimed++
	}

	return reclaimed, nil
}

// stagingOrphanAge is the updated_at staleness beyond which a non-complete staging
// record is presumed orphaned (its holder died). It is generous: it exceeds the
// whole window during which a live holder could still be refreshing its download
// lock and advancing parts, plus the retention grace.
func (c *Cache) stagingOrphanAge() time.Duration {
	bound := c.downloadLockTTL
	if c.downloadPollTimeout > bound {
		bound = c.downloadPollTimeout
	}

	return bound + c.InflightStagingRetention()
}

// AddInflightStagingGCCronJob registers the periodic staging GC sweep. It binds
// only the logger from ctx, not ctx itself: each sweep derives a fresh
// shutdown-bound context, so a request/startup-scoped registration ctx being
// cancelled can never silently disable later sweeps.
func (c *Cache) AddInflightStagingGCCronJob(ctx context.Context, schedule cron.Schedule) {
	log := zerolog.Ctx(ctx)

	log.Info().
		Time("next-run", schedule.Next(time.Now())).
		Msg("adding a cronjob for in-flight staging GC")

	c.cron.Schedule(schedule, cron.FuncJob(c.runStagingGC(log)))
}

// runStagingGC returns the cron job body for the periodic staging sweep. It
// creates a fresh shutdown-bound context per run rather than closing over a
// caller context that may later be cancelled.
func (c *Cache) runStagingGC(log *zerolog.Logger) func() {
	return func() {
		if !c.InflightStagingEnabled() {
			return
		}

		ctx, cancel := c.shutdownContext()
		defer cancel()

		n, err := c.sweepStagingGC(ctx, c.InflightStagingRetention(), c.stagingOrphanAge())
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}

			log.Warn().Err(err).Msg("in-flight staging GC sweep failed")

			return
		}

		if n > 0 {
			log.Info().Int("reclaimed", n).Msg("in-flight staging GC sweep reclaimed records")
		}
	}
}

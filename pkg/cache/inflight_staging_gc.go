package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog"

	"github.com/kalbasit/ncps/pkg/analytics"
)

// reclaimStaging deletes the staging part-objects and the staging_state record
// for hash. It is idempotent: deleting absent parts / records is a no-op.
func (c *Cache) reclaimStaging(ctx context.Context, hash string) error {
	if err := c.narStore.DeleteStagingParts(ctx, hash); err != nil {
		return fmt.Errorf("reclaim staging parts for %q: %w", hash, err)
	}

	if err := c.resetStagingState(ctx, hash); err != nil {
		return fmt.Errorf("reclaim staging state for %q: %w", hash, err)
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

		if grace > 0 {
			timer := time.NewTimer(grace)
			defer timer.Stop()

			select {
			case <-timer.C:
			case <-c.shutdownCh:
				return
			}
		}

		if err := c.reclaimStaging(context.Background(), hash); err != nil {
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

// AddInflightStagingGCCronJob registers the periodic staging GC sweep.
func (c *Cache) AddInflightStagingGCCronJob(ctx context.Context, schedule cron.Schedule) {
	zerolog.Ctx(ctx).
		Info().
		Time("next-run", schedule.Next(time.Now())).
		Msg("adding a cronjob for in-flight staging GC")

	c.cron.Schedule(schedule, cron.FuncJob(c.runStagingGC(ctx)))
}

// runStagingGC returns the cron job body for the periodic staging sweep.
func (c *Cache) runStagingGC(ctx context.Context) func() {
	return func() {
		if !c.InflightStagingEnabled() {
			return
		}

		n, err := c.sweepStagingGC(ctx, c.InflightStagingRetention(), c.stagingOrphanAge())
		if err != nil {
			zerolog.Ctx(ctx).Warn().Err(err).Msg("in-flight staging GC sweep failed")

			return
		}

		if n > 0 {
			zerolog.Ctx(ctx).Info().Int("reclaimed", n).Msg("in-flight staging GC sweep reclaimed records")
		}
	}
}

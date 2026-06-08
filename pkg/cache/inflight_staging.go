package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kalbasit/ncps/ent"
	"github.com/kalbasit/ncps/ent/stagingstate"
)

// staging_state.status values. See ent/schema/staging_state.go. The "complete"
// and "abandoned" terminal statuses are introduced alongside the staging
// completion / GC logic that consumes them.
const (
	// stagingStatusRequested: a cross-pod waiter asked for staging; the holder
	// has not started writing part-objects yet.
	stagingStatusRequested = "requested"
	// stagingStatusStaging: the holder is actively writing part-objects.
	stagingStatusStaging = "staging"
)

// errStagingStateMissing is returned when an operation expects a staging_state
// row for a hash but none exists.
var errStagingStateMissing = errors.New("no staging_state row")

// markStagingRequested records that a cross-pod waiter wants the NAR for hash
// staged. It is idempotent: if a staging_state row already exists for the hash
// (in any status) it is left untouched, so a later waiter never resets an
// in-progress holder back to "requested". Keyed by hash alone, so it is safe to
// call during the active-download window before any nar_file row exists.
func (c *Cache) markStagingRequested(ctx context.Context, hash string) error {
	err := c.dbClient.Ent().StagingState.Create().
		SetHash(hash).
		SetRequestedAt(time.Now()).
		SetStatus(stagingStatusRequested).
		OnConflictColumns(stagingstate.FieldHash).
		Ignore().
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("mark staging requested for %q: %w", hash, err)
	}

	return nil
}

// getStagingState returns the staging_state row for hash, or (nil, nil) when no
// row exists.
func (c *Cache) getStagingState(ctx context.Context, hash string) (*ent.StagingState, error) {
	st, err := c.dbClient.Ent().StagingState.Query().
		Where(stagingstate.HashEQ(hash)).
		Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil //nolint:nilnil // (nil, nil) is the documented "no staging" sentinel
	}

	if err != nil {
		return nil, fmt.Errorf("get staging state for %q: %w", hash, err)
	}

	return st, nil
}

// advanceStagingParts records that partsAvailable part-objects (indices
// 0..partsAvailable-1) are now durably readable for hash, and moves the row to
// the staging status. compression is the compression of the staged bytes.
func (c *Cache) advanceStagingParts(ctx context.Context, hash string, partsAvailable int64, compression string) error {
	n, err := c.dbClient.Ent().StagingState.Update().
		Where(stagingstate.HashEQ(hash)).
		SetPartsAvailable(partsAvailable).
		SetCompression(compression).
		SetStatus(stagingStatusStaging).
		SetUpdatedAt(time.Now()).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("advance staging parts for %q: %w", hash, err)
	}

	if n == 0 {
		return fmt.Errorf("advance staging parts for %q: %w", hash, errStagingStateMissing)
	}

	return nil
}

// resetStagingState rewinds the staging_state row for hash to a clean,
// pre-staging state (parts_available=0, status="requested") so a takeover holder
// re-stages from zero. It deliberately preserves the row rather than deleting it:
// the row is the cross-pod "a waiter wants this staged" signal, so deleting it
// would strand any other replica still waiting (it recorded its request once,
// before its poll loop) until the full re-download completes. Keeping the row at
// "requested" lets the new holder re-stage if contention persists (D5).
func (c *Cache) resetStagingState(ctx context.Context, hash string) error {
	_, err := c.dbClient.Ent().StagingState.Update().
		Where(stagingstate.HashEQ(hash)).
		SetPartsAvailable(0).
		SetStatus(stagingStatusRequested).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("reset staging state for %q: %w", hash, err)
	}

	return nil
}

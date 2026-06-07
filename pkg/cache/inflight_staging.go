package cache

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/rs/zerolog"

	"github.com/kalbasit/ncps/ent"
	"github.com/kalbasit/ncps/ent/stagingstate"
)

// stagingActivationPollInterval is how often the holder's staging producer reads
// staging_state looking for a cross-pod waiter before staging activates (D10). It
// is coarse on purpose: backfill-from-zero makes late activation harmless, so this
// only needs to be cheap, not low-latency.
const stagingActivationPollInterval = time.Second

// defaultInflightStagingPartSize is the fallback staging part size (8 MiB, the
// conventional S3 multipart part size) used when the configured part size is unset
// or non-positive. See --cache-inflight-staging-part-size (D2a).
const defaultInflightStagingPartSize int64 = 8 << 20

// staging_state.status values. See ent/schema/staging_state.go. The "complete"
// and "abandoned" terminal statuses are introduced alongside the staging
// completion / GC logic that consumes them.
const (
	// stagingStatusRequested: a cross-pod waiter asked for staging; the holder
	// has not started writing part-objects yet.
	stagingStatusRequested = "requested"
	// stagingStatusStaging: the holder is actively writing part-objects.
	stagingStatusStaging = "staging"
	// stagingStatusComplete: the holder finished writing every part-object;
	// parts_available is final. A cross-pod reader that has consumed all
	// parts_available parts can then emit a clean EOF rather than waiting.
	stagingStatusComplete = "complete"
)

// errStagingStateMissing is returned when an operation expects a staging_state
// row for a hash but none exists.
var errStagingStateMissing = errors.New("no staging_state row")

// errStagingNoTempFile is returned when the staging producer is asked to stage a
// download state that has no temp file path yet.
var errStagingNoTempFile = errors.New("staging producer: download state has no temp file path")

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

// stageInflightNar runs in the holder's download goroutine. It does nothing until
// a cross-pod waiter records a staging request for hash (and the feature is
// enabled with a distributed locker). Once a request appears it tails the holder's
// temp file and writes the in-flight NAR to shared storage as ordered, fixed-size,
// immutable part-objects — backfilling from offset zero, then appending as bytes
// arrive — advancing staging_state.parts_available as each part becomes durable.
//
// It returns when the download completes, the context is cancelled, or staging
// fails. When no waiter ever appears it returns having created nothing (zero
// overhead until contention).
func (c *Cache) stageInflightNar(ctx context.Context, hash string, ds *downloadState) {
	if !c.InflightStagingEnabled() {
		return
	}

	if !c.waitForStagingRequest(ctx, hash, ds) {
		// The download ended (or the context was cancelled) before any cross-pod
		// waiter asked for staging: nothing to do.
		return
	}

	if err := c.produceStagingParts(ctx, hash, ds); err != nil {
		zerolog.Ctx(ctx).Warn().
			Err(err).
			Str("hash", hash).
			Msg("in-flight staging producer stopped with error; staging parts left for GC/takeover")

		return
	}

	// Staging is complete and the final representation is committed: reclaim the
	// staging artifacts after the retention grace, letting cross-pod readers drain.
	c.scheduleStagingReclaim(hash)
}

// waitForStagingRequest polls staging_state on a coarse ticker until a cross-pod
// waiter's request marker appears for hash. It returns true once a request is
// observed, or false if the download finishes (ds.done) or the context is
// cancelled first. It checks once immediately so an already-recorded request
// activates without waiting a full tick.
func (c *Cache) waitForStagingRequest(ctx context.Context, hash string, ds *downloadState) bool {
	if c.stagingRequested(ctx, hash) {
		return true
	}

	ticker := time.NewTicker(stagingActivationPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return false
		case <-ds.done:
			// One last check: a waiter may have raced the download's completion.
			return c.stagingRequested(ctx, hash)
		case <-ticker.C:
			if c.stagingRequested(ctx, hash) {
				return true
			}
		}
	}
}

// stagingRequested reports whether hash has a staging_state row in the
// "requested" status — the holder's activation signal. A read error is treated
// as "not requested" so a transient DB hiccup never spuriously activates
// staging. A row left in a terminal/in-progress status (staging/complete) from
// an earlier cycle must NOT re-activate a later holder: that would re-stage with
// no active waiter and duplicate immutable part uploads. A takeover reset rewinds
// the row to "requested" (see resetStagingState), which re-arms activation.
func (c *Cache) stagingRequested(ctx context.Context, hash string) bool {
	st, err := c.getStagingState(ctx, hash)
	if err != nil {
		zerolog.Ctx(ctx).Warn().
			Err(err).
			Str("hash", hash).
			Msg("error reading staging_state while waiting for a staging request")

		return false
	}

	return st != nil && st.Status == stagingStatusRequested
}

// produceStagingParts tails the holder's temp file from offset zero and writes it
// to shared storage as fixed-size part-objects, advancing parts_available after
// each part is durable. It records the temp file's native compression (D9) so a
// cross-pod reader can transcode at parity with the same-pod path. It returns when
// the whole NAR has been staged (clean EOF), or with an error on failure.
func (c *Cache) produceStagingParts(ctx context.Context, hash string, ds *downloadState) error {
	// The temp file path and its compression are set before ds.start is closed.
	select {
	case <-ds.start:
	case <-ctx.Done():
		return ctx.Err()
	}

	ds.mu.Lock()
	assetPath := ds.assetPath
	compression := ds.tempFileCompression
	downloadErr := ds.downloadError
	ds.mu.Unlock()

	// If the download failed to start (e.g. upstream 404 or a connection
	// timeout), assetPath is empty. Surface the real download error rather than
	// the generic "no temp file" sentinel, which would otherwise mask the cause.
	if downloadErr != nil {
		return downloadErr
	}

	if assetPath == "" {
		return errStagingNoTempFile
	}

	f, err := os.Open(assetPath)
	if err != nil {
		return fmt.Errorf("staging producer: open temp file %q: %w", assetPath, err)
	}
	defer f.Close()

	partSize := c.InflightStagingPartSize()
	if partSize <= 0 {
		partSize = defaultInflightStagingPartSize
	}

	// fileAvailableReader blocks until bytes are available and returns io.EOF once
	// ds.finalSize is reached, so reading it from offset zero naturally backfills
	// the already-written prefix and then appends new bytes as they arrive.
	reader := &fileAvailableReader{f: f, ds: ds, ctx: ctx}

	buf := make([]byte, partSize)

	var index int64

	for {
		n, readErr := io.ReadFull(reader, buf)
		if n > 0 {
			if _, err := c.narStore.PutStagingPart(ctx, hash, index, bytes.NewReader(buf[:n]), int64(n)); err != nil {
				return fmt.Errorf("staging producer: put part %d for %q: %w", index, hash, err)
			}

			index++

			if err := c.advanceStagingParts(ctx, hash, index, compression.String()); err != nil {
				return fmt.Errorf("staging producer: advance parts for %q: %w", hash, err)
			}
		}

		// io.EOF: clean end with no trailing bytes. io.ErrUnexpectedEOF: the final
		// short part was already written above. Both mean the NAR is fully staged,
		// so publish the terminal marker that lets readers emit a clean EOF.
		if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
			if err := c.markStagingComplete(ctx, hash); err != nil {
				return fmt.Errorf("staging producer: mark complete for %q: %w", hash, err)
			}

			return nil
		}

		if readErr != nil {
			return fmt.Errorf("staging producer: read temp file for %q: %w", hash, readErr)
		}
	}
}

// markStagingComplete marks staging for hash as complete: every part-object has
// been written and parts_available is final. A reader tailing the parts uses this
// to emit a clean EOF after consuming all parts instead of waiting indefinitely.
func (c *Cache) markStagingComplete(ctx context.Context, hash string) error {
	n, err := c.dbClient.Ent().StagingState.Update().
		Where(stagingstate.HashEQ(hash)).
		SetStatus(stagingStatusComplete).
		SetUpdatedAt(time.Now()).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("mark staging complete for %q: %w", hash, err)
	}

	if n == 0 {
		return fmt.Errorf("mark staging complete for %q: %w", hash, errStagingStateMissing)
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

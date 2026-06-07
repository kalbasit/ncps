package ncps

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	entnarfile "github.com/kalbasit/ncps/ent/narfile"
	entnarinfo "github.com/kalbasit/ncps/ent/narinfo"
	entnarinfonarfile "github.com/kalbasit/ncps/ent/narinfonarfile"

	"github.com/kalbasit/ncps/ent"
	"github.com/kalbasit/ncps/pkg/cache"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/nar"
)

// defaultDechunkResidueGrace is how long an un-de-chunkable chunked NAR must stay
// flagged before fsck --repair will reclaim it. Two daily fsck runs are a day
// apart, so a NAR that is merely mid-chunking (or whose narinfo is written a beat
// late) resolves long before this window elapses and is never purged.
const defaultDechunkResidueGrace = 24 * time.Hour

// residueDecision is the outcome of classifying one chunked nar_file as potential
// CDC residue. The fields are not mutually exclusive: a recoverable row that was
// previously flagged is both normalized (when its URL is inconsistent) and cleared.
type residueDecision struct {
	skip         bool // actively being chunked; leave untouched
	normalizeURL bool // recoverable but the narinfo URL advertises non-none compression
	clearFlag    bool // recoverable and currently flagged: clear the stale flag
	setFlag      bool // un-de-chunkable and unflagged: record first detection
	reclaim      bool // un-de-chunkable, flagged, and aged past the grace window
}

// decideChunkedResidue applies the two-tier residue policy to one chunked nar_file.
// It is a pure function of the row's recoverability, URL consistency, persistent
// residue flag, and in-flight-chunking state, so the policy is unit-testable in
// isolation from the database.
func decideChunkedResidue(
	resolvable, urlInconsistent bool,
	flaggedAt, chunkingStartedAt *time.Time,
	now time.Time,
	grace, chunkingTTL time.Duration,
) residueDecision {
	// Guard against in-flight chunking: a NAR whose chunking_started_at is within the
	// chunking lock TTL is being written right now, not residue — leave it untouched.
	if chunkingStartedAt != nil && now.Sub(*chunkingStartedAt) < chunkingTTL {
		return residueDecision{skip: true}
	}

	if resolvable {
		// Recoverable: a narinfo carries a verifiable NarHash. Never purge. Normalize an
		// inconsistent URL immediately (safe in any CDC state) and clear any stale flag.
		return residueDecision{
			normalizeURL: urlInconsistent,
			clearFlag:    flaggedAt != nil,
		}
	}

	// Un-de-chunkable: no narinfo carries a resolvable NarHash.
	if flaggedAt == nil {
		// First detection: flag, do not purge.
		return residueDecision{setFlag: true}
	}

	if now.Sub(*flaggedAt) >= grace {
		// Flagged longer ago than the grace window and still un-de-chunkable: reclaim.
		return residueDecision{reclaim: true}
	}

	// Flagged but still within the grace window: defer to a later run.
	return residueDecision{}
}

// chunkedResidueStats tallies what a residue reconcile run did, for summary logging.
type chunkedResidueStats struct {
	normalized int
	flagged    int
	unflagged  int
	purged     int
}

// chunkedNarHasInconsistentNarInfoURL reports whether any narinfo referencing the
// chunked NAR advertises a URL other than the Compression:none form. Such a narinfo
// is recoverable-but-inconsistent (e.g. a url=none/xz-NAR desync) and fsck --repair
// normalizes it in place without touching chunks.
func chunkedNarHasInconsistentNarInfoURL(
	ctx context.Context,
	dbClient *database.Client,
	nr *ent.NarFile,
	noneURL nar.URL,
) (bool, error) {
	inconsistent, err := dbClient.Ent().NarInfo.Query().
		Where(
			entnarinfo.Or(
				entnarinfo.HasNarInfoNarFilesWith(entnarinfonarfile.NarFileIDEQ(nr.ID)),
				entnarinfo.URLHasPrefix("nar/"+nr.Hash+"."),
			),
			entnarinfo.URLNotNil(),
			entnarinfo.URLNEQ(noneURL.String()),
		).
		Exist(ctx)
	if err != nil {
		return false, fmt.Errorf("check narinfo url consistency for nar_file %d: %w", nr.ID, err)
	}

	return inconsistent, nil
}

// forEachChunkedResidue pages over every chunked nar_file (total_chunks > 0),
// classifies it via the shared de-chunk resolver and the residue policy, and invokes
// fn with the row, its Compression:none URL, and the decision. Pagination is keyed on
// ascending id so a reclaim (row deletion) mid-iteration cannot skip or re-yield rows.
func forEachChunkedResidue(
	ctx context.Context,
	dbClient *database.Client,
	now time.Time,
	grace, chunkingTTL time.Duration,
	fn func(nr *ent.NarFile, noneURL nar.URL, d residueDecision) error,
) error {
	lastID := 0

	for {
		// Chunked NARs are always stored under the Compression:none representation (the
		// resolver, PurgeChunkedNar, and NormalizeChunkedNarInfoURL all look them up by
		// the none URL), so scope the scan to compression=none — any other compression on
		// a total_chunks>0 row is not a canonical chunked NAR and is unreachable by those
		// repairs anyway.
		rows, err := dbClient.Ent().NarFile.Query().
			Where(
				entnarfile.TotalChunksGT(0),
				entnarfile.CompressionEQ(nar.CompressionTypeNone.String()),
				entnarfile.IDGT(lastID),
			).
			Order(ent.Asc(entnarfile.FieldID)).
			Limit(fsckEagerLoadBatchSize).
			All(ctx)
		if err != nil {
			return fmt.Errorf("query chunked nar_files: %w", err)
		}

		if len(rows) == 0 {
			break
		}

		for _, nr := range rows {
			lastID = nr.ID

			noneURL, err := narFileRowToURL(nr.Hash, nar.CompressionTypeNone.String(), nr.Query)
			if err != nil {
				return fmt.Errorf("narFileRowToURL for nar_file %d: %w", nr.ID, err)
			}

			// Recoverability via the shared de-chunk resolver, so fsck and the drain agree
			// on what is verifiable.
			h, err := cache.LinkedNarinfoNarHash(ctx, dbClient, nr.ID, noneURL)
			if err != nil {
				return fmt.Errorf("resolve narinfo NarHash for nar_file %d: %w", nr.ID, err)
			}

			resolvable := h != nil

			var inconsistent bool
			if resolvable {
				inconsistent, err = chunkedNarHasInconsistentNarInfoURL(ctx, dbClient, nr, noneURL)
				if err != nil {
					return err
				}
			}

			d := decideChunkedResidue(
				resolvable, inconsistent, nr.DechunkResidueFlaggedAt, nr.ChunkingStartedAt, now, grace, chunkingTTL,
			)

			if err := fn(nr, noneURL, d); err != nil {
				return err
			}
		}

		if len(rows) < fsckEagerLoadBatchSize {
			break
		}
	}

	return nil
}

// collectChunkedResidueSuspects classifies chunked nar_files read-only and records
// the reportable issues into results: recoverable-but-inconsistent rows (which
// --repair normalizes) and reclaimable residue (un-de-chunkable rows already flagged
// past the grace window, which --repair purges). Crucially it never mutates and never
// reports an un-de-chunkable row that is not yet flagged or still within its grace
// window — those are not consistency issues, so dry-run / monitoring runs do not
// raise false positives on legitimately chunked or transient NARs.
func collectChunkedResidueSuspects(
	ctx context.Context,
	dbClient *database.Client,
	grace time.Duration,
	results *fsckResults,
) error {
	now := time.Now()

	return forEachChunkedResidue(ctx, dbClient, now, grace, cache.CDCChunkingLockTTL(),
		func(nr *ent.NarFile, _ nar.URL, d residueDecision) error {
			switch {
			case d.reclaim:
				results.reclaimableChunkedResidue = append(results.reclaimableChunkedResidue, nr)
			case d.normalizeURL:
				results.recoverableChunkedNarFiles = append(results.recoverableChunkedNarFiles, nr)
			}

			return nil
		})
}

// reconcileChunkedResidue applies the residue policy with mutations. It is the
// routine CDC janitor invoked under fsck --repair: it normalizes recoverable
// inconsistencies immediately, flags newly-detected un-de-chunkable rows, clears the
// flag on rows that became recoverable, and purges rows that have stayed
// un-de-chunkable past the grace window. Per-row failures are logged and skipped so
// one bad row does not abort the sweep.
func reconcileChunkedResidue(
	ctx context.Context,
	c *cache.Cache,
	dbClient *database.Client,
	grace time.Duration,
	logger *zerolog.Logger,
) (chunkedResidueStats, error) {
	now := time.Now()

	var stats chunkedResidueStats

	err := forEachChunkedResidue(ctx, dbClient, now, grace, cache.CDCChunkingLockTTL(),
		func(nr *ent.NarFile, noneURL nar.URL, d residueDecision) error {
			switch {
			case d.skip:
				return nil

			case d.normalizeURL || d.clearFlag:
				if d.normalizeURL {
					if err := c.NormalizeChunkedNarInfoURL(ctx, &noneURL); err != nil {
						logger.Error().Err(err).Int("nar_file_id", nr.ID).Str("hash", nr.Hash).
							Msg("failed to normalize recoverable chunked nar narinfo url")

						return nil
					}

					stats.normalized++

					logger.Info().Int("nar_file_id", nr.ID).Str("hash", nr.Hash).
						Msg("normalized recoverable inconsistent chunked nar to url=none")
				}

				if d.clearFlag {
					if err := dbClient.Ent().NarFile.UpdateOneID(nr.ID).
						ClearDechunkResidueFlaggedAt().Exec(ctx); err != nil {
						logger.Error().Err(err).Int("nar_file_id", nr.ID).
							Msg("failed to clear residue flag on recovered chunked nar")

						return nil
					}

					stats.unflagged++

					logger.Info().Int("nar_file_id", nr.ID).Str("hash", nr.Hash).
						Msg("cleared residue flag (chunked nar became recoverable)")
				}

			case d.setFlag:
				if err := dbClient.Ent().NarFile.UpdateOneID(nr.ID).
					SetDechunkResidueFlaggedAt(now).Exec(ctx); err != nil {
					logger.Error().Err(err).Int("nar_file_id", nr.ID).
						Msg("failed to flag un-de-chunkable chunked nar as residue")

					return nil
				}

				stats.flagged++

				logger.Info().Int("nar_file_id", nr.ID).Str("hash", nr.Hash).
					Msg("flagged un-de-chunkable chunked nar as residue (not purged on first detection)")

			case d.reclaim:
				if err := c.PurgeChunkedNar(ctx, &noneURL); err != nil {
					if errors.Is(err, cache.ErrMigrationInProgress) {
						// Another worker holds the migration lock; leave it for next run.
						return nil
					}

					logger.Error().Err(err).Int("nar_file_id", nr.ID).Str("hash", nr.Hash).
						Msg("failed to purge aged un-de-chunkable chunked nar")

					return nil
				}

				stats.purged++

				logger.Info().Int("nar_file_id", nr.ID).Str("hash", nr.Hash).
					Msg("purged aged un-de-chunkable chunked nar (re-fetches from upstream on next request)")
			}

			return nil
		})

	return stats, err
}

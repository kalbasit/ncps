package ncps

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/nix-community/go-nix/pkg/nixhash"
	"github.com/rs/zerolog"
	"github.com/urfave/cli/v3"
	"github.com/zeebo/blake3"
	"golang.org/x/term"

	entchunk "github.com/kalbasit/ncps/ent/chunk"
	entconfigentry "github.com/kalbasit/ncps/ent/configentry"
	entnarfile "github.com/kalbasit/ncps/ent/narfile"
	entnarfilechunk "github.com/kalbasit/ncps/ent/narfilechunk"
	entnarinfo "github.com/kalbasit/ncps/ent/narinfo"
	entnarinfonarfile "github.com/kalbasit/ncps/ent/narinfonarfile"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"

	"github.com/kalbasit/ncps/ent"
	"github.com/kalbasit/ncps/pkg/config"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/otel"
	"github.com/kalbasit/ncps/pkg/storage"
	"github.com/kalbasit/ncps/pkg/storage/chunk"
)

// ErrFsckIssuesFound is returned when fsck finds consistency issues.
var ErrFsckIssuesFound = errors.New("consistency issues found")

// fsckEagerLoadBatchSize bounds the page size of any fsck query that follows up
// with an Ent `With*(...)` eager-load or an `In(...)` predicate over the page's
// IDs. Kept well below PostgreSQL's 65535 extended-protocol parameter cap so the
// secondary `IN ($1...$N)` cannot exceed the driver limit on any supported engine.
const fsckEagerLoadBatchSize = 1000

// fsckResults holds the results of a fsck run.
type fsckResults struct {
	// narinfosWithoutNarFiles: narinfos in DB with no linked nar_file.
	narinfosWithoutNarFiles []*ent.NarInfo

	// orphanedNarFilesInDB: nar_files in DB not linked to any narinfo.
	orphanedNarFilesInDB []*ent.NarFile

	// narFilesMissingInStorage: nar_files in DB whose physical file is absent.
	narFilesMissingInStorage []*ent.NarFile

	// orphanedNarFilesInStorage: NAR files in storage with no DB record.
	orphanedNarFilesInStorage []nar.URL

	// cdcMode indicates whether CDC-related checks were performed.
	cdcMode bool

	// orphanedChunksInDB: chunks in DB not linked to any nar_file.
	orphanedChunksInDB []*ent.Chunk

	// narFilesWithChunkIssues: CDC nar_files with missing or incomplete chunks.
	narFilesWithChunkIssues []*ent.NarFile

	// narFilesWithSizeMismatch: CDC nar_files (total_chunks > 0) where file_size != narinfos.nar_size.
	narFilesWithSizeMismatch []*ent.NarFile

	// orphanedChunksInStorage: chunk files in storage with no DB record.
	orphanedChunksInStorage []string

	// verifyContent indicates whether content-hash verification was requested.
	verifyContent bool

	// narFilesWithCorruptChunks: CDC nar_files where at least one chunk's decompressed
	// content does not hash to its stored key (BLAKE3 hex).
	narFilesWithCorruptChunks []*ent.NarFile

	// narFilesWithHashMismatch: CDC nar_files whose assembled chunk stream does not
	// match the narinfo NarHash (SHA-256 in nix-base32).
	narFilesWithHashMismatch []*ent.NarFile

	// recoverableChunkedNarFiles: chunked nar_files with a resolvable narinfo NarHash
	// but an inconsistent (non-Compression:none) narinfo URL. fsck --repair normalizes
	// these in place without touching chunks.
	recoverableChunkedNarFiles []*ent.NarFile

	// reclaimableChunkedResidue: un-de-chunkable chunked nar_files that have been
	// flagged (dechunk_residue_flagged_at) longer ago than the grace window and are
	// still un-de-chunkable. fsck --repair purges these; the narinfo remains and
	// re-pulls from upstream on next access.
	reclaimableChunkedResidue []*ent.NarFile
}

func (r *fsckResults) totalIssues() int {
	return len(r.narinfosWithoutNarFiles) +
		len(r.orphanedNarFilesInDB) +
		len(r.narFilesMissingInStorage) +
		len(r.orphanedNarFilesInStorage) +
		len(r.orphanedChunksInDB) +
		len(r.narFilesWithChunkIssues) +
		len(r.narFilesWithSizeMismatch) +
		len(r.orphanedChunksInStorage) +
		len(r.narFilesWithCorruptChunks) +
		len(r.narFilesWithHashMismatch) +
		len(r.recoverableChunkedNarFiles) +
		len(r.reclaimableChunkedResidue)
}

// NarWalker is implemented by storage backends that support walking NAR files.
type NarWalker interface {
	WalkNars(ctx context.Context, fn func(narURL nar.URL) error) error
}

// ChunkWalker is implemented by chunk stores that support walking chunk files.
type ChunkWalker interface {
	WalkChunks(ctx context.Context, fn func(hash string) error) error
}

// cdcModeReason explains why fsck enabled (or did not enable) CDC-mode checks.
// Returning the reason rather than a bare bool keeps the decision unit-testable
// without capturing log output, and lets the caller distinguish the residue case.
type cdcModeReason int

const (
	// cdcModeOff means no CDC signal was found; fsck skips all chunk checks.
	cdcModeOff cdcModeReason = iota
	// cdcModeFromConfig means cdc_enabled == "true" in DB config (active/draining CDC).
	cdcModeFromConfig
	// cdcModeFromChunkedNarFiles means at least one nar_file has total_chunks > 0.
	cdcModeFromChunkedNarFiles
	// cdcModeFromChunkResidue means the chunks table is non-empty while signals 1
	// and 2 are both false — orphaned chunk residue left after CDC was disabled and
	// every NAR de-chunked. Without this signal such residue is undetectable.
	cdcModeFromChunkResidue
)

// enabled reports whether any CDC signal was detected.
func (r cdcModeReason) enabled() bool { return r != cdcModeOff }

// detectFsckCDCMode decides whether fsck should run CDC/chunk checks. Signals are
// evaluated in priority order and the first match wins:
//
//  1. cdc_enabled == "true" in DB config.
//  2. any nar_file has total_chunks > 0 (chunked data present).
//  3. the chunks table is non-empty (orphaned chunk residue: signals 1 and 2 are
//     both false here because orphaned chunks are referenced by no nar_file).
//
// Query errors are logged and treated as "signal absent" so detection degrades to
// the next signal rather than aborting the run.
func detectFsckCDCMode(ctx context.Context, dbClient *database.Client, logger zerolog.Logger) cdcModeReason {
	if dbClient == nil {
		return cdcModeOff
	}

	cdcConfig, dbErr := dbClient.Ent().ConfigEntry.Query().
		Where(entconfigentry.KeyEQ(config.KeyCDCEnabled)).
		Only(ctx)
	switch {
	case dbErr != nil && !ent.IsNotFound(dbErr):
		logger.Warn().Err(dbErr).Msg(
			"could not read cdc_enabled from DB config; CDC mode detection will fall back to data-based detection",
		)
	case dbErr == nil && cdcConfig.Value == configValueTrue:
		return cdcModeFromConfig
	}

	// Fallback: chunked data exists even though the config key is missing (e.g. wrong
	// DB URL or schema mismatch).
	hasChunked, checkErr := dbClient.Ent().NarFile.Query().
		Where(entnarfile.TotalChunksGT(0)).
		Exist(ctx)
	switch {
	case checkErr != nil:
		logger.Warn().Err(checkErr).Msg("could not check for chunked nar_files")
	case hasChunked:
		logger.Warn().Msg(
			"cdc_enabled not set in DB config but chunked nar_files exist; " +
				"enabling CDC mode automatically — verify --cache-database-url is correct",
		)

		return cdcModeFromChunkedNarFiles
	}

	// Residue: orphaned chunks can remain after CDC is disabled and all NARs are
	// de-chunked, when neither signal above fires. Without enabling CDC mode here the
	// chunk store is never initialized and the residue is never reclaimed.
	hasResidue, residueErr := dbClient.Ent().Chunk.Query().Exist(ctx)
	switch {
	case residueErr != nil:
		logger.Warn().Err(residueErr).Msg("could not check for orphaned chunk residue")
	case hasResidue:
		logger.Info().Msg(
			"chunk residue detected (orphaned chunks remain after CDC was disabled); " +
				"enabling CDC mode to reclaim them — run with --repair to delete",
		)

		return cdcModeFromChunkResidue
	}

	return cdcModeOff
}

func fsckCommand(
	flagSources flagSourcesFn,
	registerShutdown registerShutdownFn,
) *cli.Command {
	return &cli.Command{
		Name:  "fsck",
		Usage: "Check consistency between database and storage",
		Description: `Checks for consistency issues between the database and storage backend.

Detects:
  - Narinfos in the database with no linked nar_file records
  - Orphaned nar_file records in the database (not linked to any narinfo)
  - Nar_file records in the database whose physical file is missing from storage
  - NAR files in storage that have no corresponding database record
  - [CDC] Orphaned chunk records in the database (not linked to any nar_file)
  - [CDC] Chunk records in the database whose physical file is missing from storage
  - [CDC] Chunk files in storage that have no corresponding database record

Use --repair to automatically fix detected issues, or --dry-run to preview what would be fixed.`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "repair",
				Usage: "Automatically fix detected issues (delete orphaned records and files)",
			},
			&cli.BoolFlag{
				Name:  flagNameDryRun,
				Usage: "Show what would be fixed without making any changes",
			},
			&cli.DurationFlag{
				Name:  "verified-since",
				Usage: "Skip checking NARs that have been verified within this duration (e.g. 1h, 30m)",
			},
			&cli.BoolFlag{
				Name: "verify-content",
				Usage: "Read and hash each CDC chunk's decompressed content to detect corruption " +
					"(expensive: reads all chunk bytes from storage; use --verified-since to limit scope)",
			},
			&cli.DurationFlag{
				Name: "dechunk-residue-grace",
				Usage: "Grace window before an un-de-chunkable chunked NAR (CDC residue) is reclaimed: " +
					"fsck --repair flags it on first detection and only purges it on a later run once the " +
					"flag has aged past this window and the NAR is still un-de-chunkable",
				Sources: flagSources("cache.fsck.dechunk-residue-grace", "CACHE_FSCK_DECHUNK_RESIDUE_GRACE"),
				Value:   defaultDechunkResidueGrace,
			},

			// Storage Flags
			&cli.StringFlag{
				Name:    flagNameStorageLocal,
				Usage:   flagUsageStorageLocal,
				Sources: flagSources("cache.storage.local", "CACHE_STORAGE_LOCAL"),
			},
			&cli.StringFlag{
				Name:    flagNameS3Bucket,
				Usage:   flagUsageS3Bucket,
				Sources: flagSources("cache.storage.s3.bucket", "CACHE_STORAGE_S3_BUCKET"),
			},
			&cli.StringFlag{
				Name:    flagNameS3Endpoint,
				Usage:   flagUsageS3Endpoint,
				Sources: flagSources("cache.storage.s3.endpoint", "CACHE_STORAGE_S3_ENDPOINT"),
			},
			&cli.StringFlag{
				Name:    flagNameS3Region,
				Usage:   flagUsageS3Region,
				Sources: flagSources("cache.storage.s3.region", "CACHE_STORAGE_S3_REGION"),
			},
			&cli.StringFlag{
				Name:    flagNameS3AccessKeyID,
				Usage:   flagUsageS3AccessKeyID,
				Sources: flagSources("cache.storage.s3.access-key-id", "CACHE_STORAGE_S3_ACCESS_KEY_ID"),
			},
			&cli.StringFlag{
				Name:    flagNameS3SecretKey,
				Usage:   flagUsageS3SecretKey,
				Sources: flagSources("cache.storage.s3.secret-access-key", "CACHE_STORAGE_S3_SECRET_ACCESS_KEY"),
			},
			&cli.BoolFlag{
				Name:    flagNameS3ForcePathStyle,
				Usage:   flagUsageS3ForcePathStyle,
				Sources: flagSources("cache.storage.s3.force-path-style", "CACHE_STORAGE_S3_FORCE_PATH_STYLE"),
			},

			// Database Flags
			&cli.StringFlag{
				Name:     flagNameDBURL,
				Usage:    flagUsageDBURL,
				Sources:  flagSources("cache.database-url", "CACHE_DATABASE_URL"),
				Required: true,
			},
			&cli.IntFlag{
				Name:    flagNameDBMaxOpenConns,
				Usage:   flagUsageDBMaxOpenConns,
				Sources: flagSources("cache.database.pool.max-open-conns", "CACHE_DATABASE_POOL_MAX_OPEN_CONNS"),
			},
			&cli.IntFlag{
				Name:    flagNameDBMaxIdleConns,
				Usage:   flagUsageDBMaxIdleConns,
				Sources: flagSources("cache.database.pool.max-idle-conns", "CACHE_DATABASE_POOL_MAX_IDLE_CONNS"),
			},

			// Lock Backend Flags (optional)
			&cli.StringSliceFlag{
				Name:    flagNameRedisAddrs,
				Usage:   "Redis server addresses for distributed locking",
				Sources: flagSources("cache.redis.addrs", "CACHE_REDIS_ADDRS"),
			},
			&cli.StringFlag{
				Name:    flagNameRedisUsername,
				Usage:   flagUsageRedisUsername,
				Sources: flagSources("cache.redis.username", "CACHE_REDIS_USERNAME"),
			},
			&cli.StringFlag{
				Name:    flagNameRedisPassword,
				Usage:   flagUsageRedisPassword,
				Sources: flagSources("cache.redis.password", "CACHE_REDIS_PASSWORD"),
			},
			&cli.IntFlag{
				Name:    flagNameRedisDB,
				Usage:   flagUsageRedisDB,
				Sources: flagSources("cache.redis.db", "CACHE_REDIS_DB"),
			},
			&cli.BoolFlag{
				Name:    flagNameRedisTLS,
				Usage:   flagUsageRedisTLS,
				Sources: flagSources("cache.redis.use-tls", "CACHE_REDIS_USE_TLS"),
			},
			&cli.StringFlag{
				Name:    flagNameLockBackend,
				Usage:   flagUsageLockBackend,
				Sources: flagSources("cache.lock.backend", "CACHE_LOCK_BACKEND"),
				Value:   lockBackendLocal,
			},
			&cli.StringFlag{
				Name:    flagNameLockRedisKeyPrefix,
				Usage:   "Prefix for all Redis lock keys",
				Sources: flagSources("cache.lock.redis.key-prefix", "CACHE_LOCK_REDIS_KEY_PREFIX"),
				Value:   flagDefaultLockRedisKeyPrefix,
			},
			&cli.DurationFlag{
				Name:    flagNameLockDownloadTTL,
				Usage:   "TTL for download locks",
				Sources: flagSources("cache.lock.download-lock-ttl", "CACHE_LOCK_DOWNLOAD_TTL"),
				Value:   5 * time.Minute,
			},
			&cli.DurationFlag{
				Name:    flagNameLockLRUTTL,
				Usage:   "TTL for LRU lock",
				Sources: flagSources("cache.lock.lru-lock-ttl", "CACHE_LOCK_LRU_TTL"),
				Value:   30 * time.Minute,
			},
			&cli.IntFlag{
				Name:    flagNameLockMaxRetries,
				Usage:   flagUsageLockMaxRetries,
				Sources: flagSources("cache.lock.retry.max-attempts", "CACHE_LOCK_RETRY_MAX_ATTEMPTS"),
				Value:   3,
			},
			&cli.DurationFlag{
				Name:    flagNameLockInitialDelay,
				Usage:   flagUsageLockInitialDelay,
				Sources: flagSources("cache.lock.retry.initial-delay", "CACHE_LOCK_RETRY_INITIAL_DELAY"),
				Value:   100 * time.Millisecond,
			},
			&cli.DurationFlag{
				Name:    flagNameLockMaxDelay,
				Usage:   "Maximum retry delay for distributed locks",
				Sources: flagSources("cache.lock.retry.max-delay", "CACHE_LOCK_RETRY_MAX_DELAY"),
				Value:   2 * time.Second,
			},
			&cli.BoolFlag{
				Name:    flagNameLockJitter,
				Usage:   "Enable jitter in retry delays",
				Sources: flagSources("cache.lock.retry.jitter", "CACHE_LOCK_RETRY_JITTER"),
				Value:   true,
			},
			&cli.BoolFlag{
				Name:    flagNameLockAllowDegraded,
				Usage:   "Allow falling back to local locks if Redis is unavailable",
				Sources: flagSources("cache.lock.allow-degraded-mode", "CACHE_LOCK_ALLOW_DEGRADED_MODE"),
			},
			&cli.IntFlag{
				Name:    flagNameRedisPoolSize,
				Usage:   flagUsageRedisPoolSize,
				Sources: flagSources("cache.redis.pool-size", "CACHE_REDIS_POOL_SIZE"),
				Value:   10,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			logger := zerolog.Ctx(ctx).With().Str("cmd", "fsck").Logger()
			ctx = logger.WithContext(ctx)

			dryRun := cmd.Bool("dry-run")
			repair := cmd.Bool("repair")
			verifyContent := cmd.Bool("verify-content")

			verifiedSince := cmd.Duration("verified-since")

			dechunkResidueGrace := cmd.Duration("dechunk-residue-grace")
			if dechunkResidueGrace <= 0 {
				dechunkResidueGrace = defaultDechunkResidueGrace
			}

			// 1. Setup Database
			dbClient, err := createDatabaseClient(cmd)
			if err != nil {
				logger.Error().Err(err).Msg("error creating database client")

				return err
			}

			registerShutdown("database client", func(_ context.Context) error { return dbClient.Close() })

			// 2. Setup Lockers
			locker, rwLocker, err := getLockers(ctx, cmd)
			if err != nil {
				logger.Error().Err(err).Msg("error creating the lockers")

				return err
			}

			// 3. Setup OTel
			extraResourceAttrs, err := detectExtraResourceAttrs(ctx, cmd, dbClient, rwLocker)
			if err != nil {
				logger.Error().Err(err).Msg("error detecting extra resource attributes")

				return err
			}

			otelResource, err := otel.NewResource(
				ctx,
				cmd.Root().Name,
				Version,
				semconv.SchemaURL,
				extraResourceAttrs...,
			)
			if err != nil {
				logger.Error().Err(err).Msg("error creating a new otel resource")

				return err
			}

			otelShutdown, err := otel.SetupOTelSDK(
				ctx,
				cmd.Root().Bool("otel-enabled"),
				cmd.Root().String("otel-grpc-url"),
				otelResource,
			)
			if err != nil {
				return err
			}

			registerShutdown("open telemetry", otelShutdown)

			// 4. Setup Storage
			_, _, narStore, err := getStorageBackend(ctx, cmd)
			if err != nil {
				logger.Error().Err(err).Msg("error creating storage backend")

				return err
			}

			// 5. Detect CDC mode
			cdcMode := detectFsckCDCMode(ctx, dbClient, logger).enabled()

			var chunkStore chunk.Store

			if cdcMode {
				cs, csErr := getChunkStorageBackend(ctx, cmd, locker)
				if csErr != nil {
					logger.Error().Err(csErr).Msg("error creating chunk storage backend")

					return csErr
				}

				chunkStore = cs
			}

			// 6. Phase 1: Collect suspects
			logger.Info().Msg("phase 1: collecting suspects")

			suspects, err := collectFsckSuspects(ctx, dbClient, narStore, chunkStore, cdcMode, verifiedSince, verifyContent)
			if err != nil {
				return fmt.Errorf("error collecting suspects: %w", err)
			}

			// 7. Phase 2: Re-verify (double-check to handle in-flight operations)
			logger.Info().Msg("phase 2: re-verifying suspects")

			results, err := reVerifyFsckSuspects(ctx, dbClient, narStore, chunkStore, suspects)
			if err != nil {
				return fmt.Errorf("error re-verifying suspects: %w", err)
			}

			// 7b. Classify chunked-NAR residue (read-only) so the summary and exit code
			// reflect recoverable inconsistencies (which --repair normalizes) and
			// grace-elapsed reclaimable residue (which --repair purges). This never
			// mutates and never reports an un-de-chunkable row that is unflagged or still
			// within its grace window, so dry-run / monitoring runs raise no false
			// positives on transient or legitimately chunked NARs.
			if cdcMode {
				if err := collectChunkedResidueSuspects(ctx, dbClient, dechunkResidueGrace, results); err != nil {
					return fmt.Errorf("error collecting chunked-residue suspects: %w", err)
				}
			}

			// 8. Print summary
			printFsckSummary(results)

			// repairChunkedResidue mutates: it normalizes recoverable inconsistencies,
			// flags newly-detected un-de-chunkable rows, clears the flag on recovered rows,
			// and purges rows aged past the grace window. It is invoked once per run when
			// the operator has consented to mutation (either --repair or an interactive
			// "yes"); the guards below ensure it never runs twice.
			repairChunkedResidue := func() error {
				c, cacheErr := createCache(ctx, cmd, dbClient, locker, rwLocker, nil)
				if cacheErr != nil {
					return fmt.Errorf("error creating cache for chunked-residue repair: %w", cacheErr)
				}

				// Don't kick off lazy re-chunking while we reconcile residue.
				c.SetCDCLazyChunking(false, 0)

				stats, recErr := reconcileChunkedResidue(ctx, c, dbClient, dechunkResidueGrace, &logger)

				c.Close()

				if recErr != nil {
					return fmt.Errorf("error reconciling chunked residue: %w", recErr)
				}

				logger.Info().
					Int("normalized", stats.normalized).
					Int("flagged", stats.flagged).
					Int("unflagged", stats.unflagged).
					Int("purged", stats.purged).
					Msg("chunked-residue reconcile complete")

				return nil
			}

			// 8b. CDC residue janitor. Under --repair, reconcile regardless of other issue
			// counts: first-detection flagging, recoverable normalization, became-recoverable
			// unflagging, and grace-windowed reclamation are routine maintenance that may be
			// needed even when totalIssues() == 0 (e.g. a fresh un-de-chunkable row must be
			// flagged on this run so a later run can reclaim it). Non-repair runs only report
			// (above); they never mutate here. The interactive-approval path below covers the
			// !repair "yes" case.
			if repair && cdcMode && chunkStore != nil {
				if err := repairChunkedResidue(); err != nil {
					return err
				}
			}

			if results.totalIssues() == 0 {
				return nil
			}

			// 9. Decide what to do
			if dryRun {
				fmt.Println("\nRun with --repair to fix, or omit --dry-run to be prompted.")

				return ErrFsckIssuesFound
			}

			if !repair {
				if !term.IsTerminal(int(os.Stdin.Fd())) {
					return fmt.Errorf("%w: not a terminal and --repair not set", ErrFsckIssuesFound)
				}

				fmt.Print("\nRepair all issues? [y/N]: ")

				scanner := bufio.NewScanner(os.Stdin)
				if !scanner.Scan() {
					fmt.Println("Aborted (no input).")

					return ErrFsckIssuesFound
				}

				answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
				if answer != "y" && answer != "yes" {
					fmt.Println("Aborted.")

					return ErrFsckIssuesFound
				}
			}

			// 10. Phase 3: Repair
			logger.Info().Msg("phase 3: repairing issues")

			// Reconcile chunked residue on the interactive-approval path. Skipped when
			// --repair is set, because the janitor above already ran; this guard prevents a
			// double reconcile.
			if !repair && cdcMode && chunkStore != nil {
				if err := repairChunkedResidue(); err != nil {
					return err
				}
			}

			if err := repairFsckIssues(ctx, dbClient, narStore, chunkStore, results); err != nil {
				return fmt.Errorf("error repairing issues: %w", err)
			}

			fmt.Println("Repair complete.")

			return nil
		},
	}
}

// collectFsckSuspects runs all DB queries and storage walks to collect potential issues.
func collectFsckSuspects(
	ctx context.Context,
	dbClient *database.Client,
	narStore storage.NarStore,
	chunkStore chunk.Store,
	cdcMode bool,
	verifiedSince time.Duration,
	verifyContent bool,
) (*fsckResults, error) {
	logger := zerolog.Ctx(ctx)
	results := &fsckResults{cdcMode: cdcMode, verifyContent: verifyContent}

	// Setup progress tracking
	var checked, skipped, total, suspects atomic.Int64

	startTime := time.Now()

	// Start background progress ticker
	stopTicker := startProgressTicker(func() {
		c := checked.Load()
		t := total.Load()
		s := suspects.Load()
		sk := skipped.Load()
		logProgress(*logger, startTime, c, t).
			Int64("suspects", s).
			Int64("skipped", sk).
			Msg("phase 1: progress update")
	})
	defer stopTicker()

	// a. Narinfos without nar_files
	narinfosWithoutNarFiles, err := dbClient.Ent().NarInfo.Query().
		Where(entnarinfo.Not(entnarinfo.HasNarInfoNarFiles())).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("GetNarInfosWithoutNarFiles: %w", err)
	}

	results.narinfosWithoutNarFiles = narinfosWithoutNarFiles
	logger.Info().Int("count", len(narinfosWithoutNarFiles)).Msg("phase 1a: narinfos_without_nar_files found")

	// b. Orphaned nar_files in DB
	orphanedNarFiles, err := dbClient.Ent().NarFile.Query().
		Where(entnarfile.Not(entnarfile.HasNarInfoNarFiles())).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("GetOrphanedNarFiles: %w", err)
	}

	results.orphanedNarFilesInDB = orphanedNarFiles
	logger.Info().Int("count", len(orphanedNarFiles)).Msg("phase 1b: orphaned nar_files in DB found")

	// c. Nar_files missing from storage
	allNarFiles, err := dbClient.Ent().NarFile.Query().All(ctx)
	if err != nil {
		return nil, fmt.Errorf("GetAllNarFiles: %w", err)
	}
	// Items checked in phase 1c and 1f (if enabled)
	for _, nf := range allNarFiles {
		if nf.TotalChunks <= 0 || cdcMode {
			if !shouldCheckNar(nf, verifiedSince) {
				skipped.Add(1)

				continue
			}

			total.Add(1)
		}
	}

	presentNars := make(map[string]struct{})

	// Walk storage to build index of present NARs
	logger.Info().Msg("phase 1c: walking NAR storage (building index)")

	var storageNarCount atomic.Int64

	if narWalker, ok := narStore.(NarWalker); ok {
		if err := narWalker.WalkNars(ctx, func(narURL nar.URL) error {
			presentNars[narURL.String()] = struct{}{}

			storageNarCount.Add(1)

			return nil
		}); err != nil {
			return nil, fmt.Errorf("WalkNars: %w", err)
		}
	}

	storageCount := storageNarCount.Load()
	total.Add(storageCount)
	logger.Info().Int64("count", storageCount).Msg("phase 1c: indexed NAR files from storage")

	// Check for missing nar_files
	var nonChunkedCount int64

	logger.Info().Int("total", len(allNarFiles)).Msg("phase 1c: checking nar_files against storage")

	for _, nf := range allNarFiles {
		// Chunked nar_files live in chunk storage — not as whole NAR files.
		// They are verified separately via collectNarFilesWithChunkIssues.
		// Skip regardless of cdcMode detection to avoid false "missing from storage" reports.
		if nf.TotalChunks > 0 {
			continue
		}

		if !shouldCheckNar(nf, verifiedSince) {
			continue
		}

		nonChunkedCount++

		checked.Add(1)

		narURL, err := narFileRowToURL(nf.Hash, nf.Compression, nf.Query)
		if err != nil {
			return nil, fmt.Errorf("narFileRowToURL for nar_file %d: %w", nf.ID, err)
		}

		if _, exists := presentNars[narURL.String()]; !exists {
			suspects.Add(1)

			results.narFilesMissingInStorage = append(results.narFilesMissingInStorage, nf)
		} else {
			// Found and verified, update verified_at
			if _, err := dbClient.Ent().NarFile.UpdateOneID(nf.ID).
				SetVerifiedAt(time.Now()).
				Save(ctx); err != nil {
				logger.Warn().Err(err).Int("nar_file_id", nf.ID).Msg("failed to update verified_at")
			}
		}
	}

	logger.Info().Int("suspects", len(results.narFilesMissingInStorage)).Msg("phase 1c: done checking nar_files")

	// d. Orphaned NAR files in storage
	logger.Info().Int64("total", storageCount).Msg("phase 1d: checking storage NAR files against DB")

	narWalker, ok := narStore.(NarWalker)
	if ok {
		if err := narWalker.WalkNars(ctx, func(narURL nar.URL) error {
			checked.Add(1)

			exists, dbErr := dbClient.Ent().NarFile.Query().
				Where(
					entnarfile.HashEQ(narURL.Hash),
					entnarfile.CompressionEQ(narURL.Compression.String()),
					entnarfile.QueryEQ(narURL.Query.Encode()),
				).
				Exist(ctx)
			if dbErr != nil {
				return fmt.Errorf("DB lookup for NAR %s: %w", narURL, dbErr)
			}

			if !exists {
				suspects.Add(1)

				results.orphanedNarFilesInStorage = append(results.orphanedNarFilesInStorage, narURL)
			}

			return nil
		}); err != nil {
			return nil, fmt.Errorf("WalkNars: %w", err)
		}
	}

	logger.Info().Int("suspects", len(results.orphanedNarFilesInStorage)).Msg("phase 1d: done checking storage NAR files")

	if !cdcMode {
		return results, nil
	}

	// e. Orphaned chunks in DB
	orphanedChunks, err := dbClient.Ent().Chunk.Query().
		Where(entchunk.Not(entchunk.HasNarFileLinks())).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("GetOrphanedChunks: %w", err)
	}

	results.orphanedChunksInDB = orphanedChunks
	logger.Info().Int("count", len(orphanedChunks)).Msg("phase 1e: orphaned chunks in DB found")

	eligibleChunkedNarFiles := make(map[int]*ent.NarFile)

	for _, nf := range allNarFiles {
		if nf.TotalChunks <= 0 {
			continue
		}

		if shouldCheckNar(nf, verifiedSince) {
			eligibleChunkedNarFiles[nf.ID] = nf
		}
	}

	// f. NAR files with chunk issues (count mismatch or chunks missing from storage)
	logger.Info().Msg("phase 1f: checking NAR files with chunk issues")

	narFilesWithChunkIssues, err := collectNarFilesWithChunkIssues(
		ctx, dbClient, allNarFiles, chunkStore, &checked, verifiedSince,
	)
	if err != nil {
		return nil, err
	}

	results.narFilesWithChunkIssues = narFilesWithChunkIssues
	logger.Info().Int("count", len(narFilesWithChunkIssues)).Msg("phase 1f: NAR files with chunk issues found")

	// g. CDC NAR files with size mismatch (total_chunks > 0 but file_size != narinfos.nar_size)
	logger.Info().Msg("phase 1g: checking CDC NAR files with size mismatch")

	narFilesWithSizeMismatch, err := queryCDCNarFilesWithSizeMismatch(ctx, dbClient)
	if err != nil {
		return nil, fmt.Errorf("GetCDCNarFilesWithSizeMismatch: %w", err)
	}

	chunkIssuesByID := make(map[int]struct{}, len(narFilesWithChunkIssues))
	for _, nf := range narFilesWithChunkIssues {
		chunkIssuesByID[nf.ID] = struct{}{}
	}

	sizeMismatchByID := make(map[int]struct{}, len(narFilesWithSizeMismatch))
	for _, nf := range narFilesWithSizeMismatch {
		if _, ok := eligibleChunkedNarFiles[nf.ID]; !ok {
			continue
		}

		results.narFilesWithSizeMismatch = append(results.narFilesWithSizeMismatch, nf)
		sizeMismatchByID[nf.ID] = struct{}{}
	}

	for id := range eligibleChunkedNarFiles {
		if _, hasChunkIssues := chunkIssuesByID[id]; hasChunkIssues {
			continue
		}

		if _, hasSizeMismatch := sizeMismatchByID[id]; hasSizeMismatch {
			continue
		}

		if _, err := dbClient.Ent().NarFile.UpdateOneID(id).
			SetVerifiedAt(time.Now()).
			Save(ctx); err != nil {
			logger.Warn().Err(err).Int("nar_file_id", id).Msg("failed to update verified_at")
		}
	}

	logger.Info().
		Int("count", len(results.narFilesWithSizeMismatch)).
		Msg("phase 1g: CDC NAR files with size mismatch found")

	// h. Orphaned chunk files in storage (before content checks so we skip orphan hashes)
	logger.Info().Msg("phase 1h: checking orphaned chunk files in storage")

	// The total number of chunks in storage is not known beforehand, so we cannot
	// accurately report a percentage for phase 1h. We'll rely on the checked count.

	orphaned, err := collectOrphanedChunksInStorage(ctx, dbClient, chunkStore, &checked)
	if err != nil {
		return nil, err
	}

	results.orphanedChunksInStorage = orphaned
	logger.Info().Int("count", len(orphaned)).Msg("phase 1h: orphaned chunk files found")

	if !verifyContent {
		return results, nil
	}

	// Build a set of nar_file IDs that already have structural chunk issues so we
	// don't double-count them in the content checks.
	chunkIssueIDs := make(map[int]struct{}, len(results.narFilesWithChunkIssues))
	for _, nf := range results.narFilesWithChunkIssues {
		chunkIssueIDs[nf.ID] = struct{}{}
	}

	// i. Chunk content hash verification.
	logger.Info().Msg("phase 1i: verifying chunk content hashes")

	corruptNarFiles, err := collectNarFilesWithCorruptChunks(
		ctx, dbClient, chunkStore, allNarFiles, chunkIssueIDs, verifiedSince,
	)
	if err != nil {
		return nil, fmt.Errorf("collectNarFilesWithCorruptChunks: %w", err)
	}

	results.narFilesWithCorruptChunks = corruptNarFiles
	logger.Info().Int("count", len(corruptNarFiles)).Msg("phase 1i: NAR files with corrupt chunks found")

	// Build set of corrupt IDs to skip in the NAR hash check.
	corruptByID := make(map[int]struct{}, len(corruptNarFiles))
	for _, nf := range corruptNarFiles {
		corruptByID[nf.ID] = struct{}{}
	}

	// j. End-to-end NAR hash verification.
	logger.Info().Msg("phase 1j: verifying assembled NAR hashes")

	hashMismatchNarFiles, err := collectNarFilesWithHashMismatch(
		ctx, dbClient, chunkStore, allNarFiles, chunkIssueIDs, corruptByID, verifiedSince,
	)
	if err != nil {
		return nil, fmt.Errorf("collectNarFilesWithHashMismatch: %w", err)
	}

	results.narFilesWithHashMismatch = hashMismatchNarFiles
	logger.Info().Int("count", len(hashMismatchNarFiles)).Msg("phase 1j: NAR files with hash mismatch found")

	return results, nil
}

// queryCDCNarFilesWithSizeMismatch returns CDC nar_files (total_chunks > 0) whose
// stored file_size differs from the linked narinfo's declared nar_size. Mirrors
// the legacy GetCDCNarFilesWithSizeMismatch SQL.
//
// Implementation walks CDC nar_file rows in keyset-paginated batches of
// fsckEagerLoadBatchSize and, per page, resolves the linked narinfos with a
// bounded `NarFileIDIn(...)` + `WithNarinfo()` query. Eager-loading every CDC
// row in one shot would emit `WHERE nar_file_id IN ($1...$N)` and trip
// PostgreSQL's 65535 extended-protocol parameter cap once a cache grows past the
// threshold.
func queryCDCNarFilesWithSizeMismatch(
	ctx context.Context,
	dbClient *database.Client,
) ([]*ent.NarFile, error) {
	var (
		mismatched []*ent.NarFile
		lastID     int
	)

	for {
		page, err := dbClient.Ent().NarFile.Query().
			Where(
				entnarfile.TotalChunksGT(0),
				entnarfile.IDGT(lastID),
			).
			Order(ent.Asc(entnarfile.FieldID)).
			Limit(fsckEagerLoadBatchSize).
			All(ctx)
		if err != nil {
			return nil, fmt.Errorf("query CDC nar_files: %w", err)
		}

		if len(page) == 0 {
			break
		}

		ids := make([]int, len(page))
		for i, nf := range page {
			ids[i] = nf.ID
		}

		links, err := dbClient.Ent().NarInfoNarFile.Query().
			Where(entnarinfonarfile.NarFileIDIn(ids...)).
			WithNarinfo().
			All(ctx)
		if err != nil {
			return nil, fmt.Errorf("query CDC nar_file links: %w", err)
		}

		linksByNarFile := make(map[int][]*ent.NarInfoNarFile, len(page))
		for _, link := range links {
			linksByNarFile[link.NarFileID] = append(linksByNarFile[link.NarFileID], link)
		}

		for _, nf := range page {
			for _, link := range linksByNarFile[nf.ID] {
				ni := link.Edges.Narinfo
				if ni == nil || ni.NarSize == nil {
					continue
				}
				// file_size is uint64; nar_size is *int64. Cast both to int64
				// for comparison — file_size never exceeds int64 in practice
				// (real NARs are bounded by storage limits).
				//nolint:gosec // G115: NAR sizes are well below math.MaxInt64.
				if int64(nf.FileSize) != *ni.NarSize {
					mismatched = append(mismatched, nf)

					break
				}
			}
		}

		lastID = page[len(page)-1].ID

		if len(page) < fsckEagerLoadBatchSize {
			break
		}
	}

	return mismatched, nil
}

// reVerifyFsckSuspects re-checks each suspected issue to handle in-flight operations.
// Items that are no longer issues are silently removed from the results.
func reVerifyFsckSuspects(
	ctx context.Context,
	dbClient *database.Client,
	narStore storage.NarStore,
	chunkStore chunk.Store,
	suspects *fsckResults,
) (*fsckResults, error) {
	logger := zerolog.Ctx(ctx)
	results := &fsckResults{cdcMode: suspects.cdcMode, verifyContent: suspects.verifyContent}

	// Setup progress tracking for phase 2
	totalSuspects := suspects.totalIssues()

	var checked, remaining atomic.Int64

	remaining.Store(int64(totalSuspects))

	startTime := time.Now()

	logger.Info().Int("total", totalSuspects).Msg("phase 2: re-verifying suspects")

	// Start background progress ticker
	stopTicker := startProgressTicker(func() {
		c := checked.Load()
		t := int64(totalSuspects)
		r := remaining.Load()
		logProgress(*logger, startTime, c, t).
			Int64("remaining", r).
			Msg("phase 2: progress update")
	})
	defer stopTicker()

	// Re-verify: narinfos without nar_files
	for _, ni := range suspects.narinfosWithoutNarFiles {
		hasNarFile, err := dbClient.Ent().NarFile.Query().
			Where(entnarfile.HasNarInfoNarFilesWith(entnarinfonarfile.NarinfoIDEQ(ni.ID))).
			Exist(ctx)
		if err != nil {
			return nil, fmt.Errorf("re-verify GetNarFileByNarInfoID(%d): %w", ni.ID, err)
		}

		if !hasNarFile {
			results.narinfosWithoutNarFiles = append(results.narinfosWithoutNarFiles, ni)
		}

		checked.Add(1)
		remaining.Add(-1)
	}

	// Re-verify: orphaned nar_files in DB
	for _, nf := range suspects.orphanedNarFilesInDB {
		// Check if it's still orphaned by checking for narinfo link
		hasLink, err := dbClient.Ent().NarInfo.Query().
			Where(entnarinfo.HasNarInfoNarFilesWith(
				entnarinfonarfile.HasNarFileWith(
					entnarfile.HashEQ(nf.Hash),
					entnarfile.CompressionEQ(nf.Compression),
					entnarfile.QueryEQ(nf.Query),
				),
			)).
			Exist(ctx)
		if err != nil {
			return nil, fmt.Errorf("re-verify orphaned nar_file(%d): %w", nf.ID, err)
		}

		if !hasLink {
			results.orphanedNarFilesInDB = append(results.orphanedNarFilesInDB, nf)
		}

		checked.Add(1)
		remaining.Add(-1)
	}

	// Re-verify: nar_files missing from storage
	for _, nf := range suspects.narFilesMissingInStorage {
		narURL, err := narFileRowToURL(nf.Hash, nf.Compression, nf.Query)
		if err != nil {
			return nil, fmt.Errorf("narFileRowToURL for nar_file %d: %w", nf.ID, err)
		}

		if !narStore.HasNar(ctx, narURL) {
			results.narFilesMissingInStorage = append(results.narFilesMissingInStorage, nf)
		}

		checked.Add(1)
		remaining.Add(-1)
	}

	// Re-verify: orphaned NAR files in storage
	for _, narURL := range suspects.orphanedNarFilesInStorage {
		exists, err := dbClient.Ent().NarFile.Query().
			Where(
				entnarfile.HashEQ(narURL.Hash),
				entnarfile.CompressionEQ(narURL.Compression.String()),
				entnarfile.QueryEQ(narURL.Query.Encode()),
			).
			Exist(ctx)
		if err != nil {
			return nil, fmt.Errorf("re-verify orphaned NAR in storage (%s): %w", narURL, err)
		}

		if !exists {
			results.orphanedNarFilesInStorage = append(results.orphanedNarFilesInStorage, narURL)
		}

		checked.Add(1)
		remaining.Add(-1)
	}

	if !suspects.cdcMode {
		return results, nil
	}

	// Re-verify: orphaned chunks in DB
	recheckedOrphanedChunks, err := dbClient.Ent().Chunk.Query().
		Where(entchunk.Not(entchunk.HasNarFileLinks())).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("re-verify GetOrphanedChunks: %w", err)
	}

	recheckedMap := make(map[int]struct{}, len(recheckedOrphanedChunks))
	for _, rc := range recheckedOrphanedChunks {
		recheckedMap[rc.ID] = struct{}{}
	}

	for _, c := range suspects.orphanedChunksInDB {
		if _, ok := recheckedMap[c.ID]; ok {
			results.orphanedChunksInDB = append(results.orphanedChunksInDB, c)
		}

		checked.Add(1)
		remaining.Add(-1)
	}

	// Re-verify: NAR files with chunk issues
	if chunkStore != nil {
		for _, nf := range suspects.narFilesWithChunkIssues {
			broken, err := isNarFileChunkBroken(ctx, dbClient, chunkStore, nf)
			if err != nil {
				return nil, fmt.Errorf("re-verify narFilesWithChunkIssues(%d): %w", nf.ID, err)
			}

			if broken {
				results.narFilesWithChunkIssues = append(results.narFilesWithChunkIssues, nf)
			}

			checked.Add(1)
			remaining.Add(-1)
		}
	}

	// Re-verify: CDC NAR files with size mismatch
	if chunkStore != nil {
		recheckMismatch, err := queryCDCNarFilesWithSizeMismatch(ctx, dbClient)
		if err != nil {
			return nil, fmt.Errorf("re-verify GetCDCNarFilesWithSizeMismatch: %w", err)
		}

		recheckMismatchMap := make(map[int]struct{}, len(recheckMismatch))
		for _, nf := range recheckMismatch {
			recheckMismatchMap[nf.ID] = struct{}{}
		}

		for _, nf := range suspects.narFilesWithSizeMismatch {
			if _, ok := recheckMismatchMap[nf.ID]; ok {
				results.narFilesWithSizeMismatch = append(results.narFilesWithSizeMismatch, nf)
			}

			checked.Add(1)
			remaining.Add(-1)
		}
	}

	// Re-verify: orphaned chunk files in storage
	for _, hash := range suspects.orphanedChunksInStorage {
		exists, err := dbClient.Ent().Chunk.Query().
			Where(entchunk.HashEQ(hash)).
			Exist(ctx)
		if err != nil {
			return nil, fmt.Errorf("re-verify orphaned chunk in storage (%s): %w", hash, err)
		}

		if !exists {
			results.orphanedChunksInStorage = append(results.orphanedChunksInStorage, hash)
		}

		checked.Add(1)
		remaining.Add(-1)
	}

	if !suspects.verifyContent || chunkStore == nil {
		return results, nil
	}

	// Re-verify: NAR files with corrupt chunks (re-run content hash check per NAR).
	for _, nf := range suspects.narFilesWithCorruptChunks {
		broken, err := isNarFileContentCorrupt(ctx, dbClient, chunkStore, nf)
		if err != nil {
			return nil, fmt.Errorf("re-verify narFilesWithCorruptChunks(%d): %w", nf.ID, err)
		}

		if broken {
			results.narFilesWithCorruptChunks = append(results.narFilesWithCorruptChunks, nf)
		}

		checked.Add(1)
		remaining.Add(-1)
	}

	// Re-verify: NAR files with hash mismatch.
	for _, nf := range suspects.narFilesWithHashMismatch {
		mismatch, err := isNarFileHashMismatched(ctx, dbClient, chunkStore, nf)
		if err != nil {
			return nil, fmt.Errorf("re-verify narFilesWithHashMismatch(%d): %w", nf.ID, err)
		}

		if mismatch {
			results.narFilesWithHashMismatch = append(results.narFilesWithHashMismatch, nf)
		}

		checked.Add(1)
		remaining.Add(-1)
	}

	return results, nil
}

// printFsckSummary prints the fsck summary report.
//
// The table is built dynamically so column widths and border lines are always
// consistent, regardless of how many digits the counts have.
//
// Emoji are placed outside the right border to avoid terminal-width ambiguity
// (multi-byte emoji chars do not have a universally agreed display-column width
// so mixing them inside a Printf format field breaks alignment).
func printFsckSummary(r *fsckResults) {
	type fsckRow struct {
		label string
		count int
	}

	// Collect every data row so we can measure widths before printing.
	dataRows := []fsckRow{
		{"Narinfos without nar_files:", len(r.narinfosWithoutNarFiles)},
		{"Orphaned nar_files (DB only):", len(r.orphanedNarFilesInDB)},
		{"Nar_files missing from storage:", len(r.narFilesMissingInStorage)},
		{"Orphaned NAR files in storage:", len(r.orphanedNarFilesInStorage)},
	}

	if r.cdcMode {
		dataRows = append(
			dataRows,
			fsckRow{"Orphaned chunks (DB only):", len(r.orphanedChunksInDB)},
			fsckRow{"NAR files w/ chunk issues:", len(r.narFilesWithChunkIssues)},
			fsckRow{"CDC NARs w/ size mismatch:", len(r.narFilesWithSizeMismatch)},
			fsckRow{"Orphaned chunk files:", len(r.orphanedChunksInStorage)},
		)

		if r.verifyContent {
			dataRows = append(
				dataRows,
				fsckRow{"NAR files w/ corrupt chunks:", len(r.narFilesWithCorruptChunks)},
				fsckRow{"NAR files w/ hash mismatch:", len(r.narFilesWithHashMismatch)},
			)
		}

		dataRows = append(
			dataRows,
			fsckRow{"Recoverable chunked residue:", len(r.recoverableChunkedNarFiles)},
			fsckRow{"Reclaimable chunked residue:", len(r.reclaimableChunkedResidue)},
		)
	}

	total := r.totalIssues()
	dataRows = append(dataRows, fsckRow{"Total issues:", total})

	// Compute column widths from the actual data.
	maxLabel := 0
	maxCount := 1

	for _, row := range dataRows {
		if len(row.label) > maxLabel {
			maxLabel = len(row.label)
		}

		if d := len(fmt.Sprintf("%d", row.count)); d > maxCount {
			maxCount = d
		}
	}

	// Inner width = 2 (left pad) + maxLabel + 1 (gap) + maxCount + 2 (right pad).
	innerWidth := 2 + maxLabel + 1 + maxCount + 2

	sep := "╠" + strings.Repeat("═", innerWidth) + "╣"
	top := "╔" + strings.Repeat("═", innerWidth) + "╗"
	bot := "╚" + strings.Repeat("═", innerWidth) + "╝"

	titleStr := "ncps fsck summary"
	titlePad := innerWidth - len(titleStr)
	titleRow := "║" + strings.Repeat(" ", titlePad/2) + titleStr +
		strings.Repeat(" ", titlePad-titlePad/2) + "║"

	// row prints one data line. The emoji sits to the right of the closing border
	// so that all box characters are pure ASCII/single-width and borders are
	// guaranteed to align.
	row := func(label string, n int) {
		ic := "✅"
		if n > 0 {
			ic = "❌"
		}

		inner := fmt.Sprintf("  %-*s %*d  ", maxLabel, label, maxCount, n)
		fmt.Printf("║%s║ %s\n", inner, ic)
	}

	sectionHeader := func(title string) {
		inner := "  " + title + strings.Repeat(" ", innerWidth-2-len(title))
		fmt.Printf("║%s║\n", inner)
	}

	fmt.Println()
	fmt.Println(top)
	fmt.Println(titleRow)
	fmt.Println(sep)
	row("Narinfos without nar_files:", len(r.narinfosWithoutNarFiles))
	row("Orphaned nar_files (DB only):", len(r.orphanedNarFilesInDB))
	row("Nar_files missing from storage:", len(r.narFilesMissingInStorage))
	row("Orphaned NAR files in storage:", len(r.orphanedNarFilesInStorage))

	if r.cdcMode {
		fmt.Println(sep)
		sectionHeader("CDC checks")
		fmt.Println(sep)
		row("Orphaned chunks (DB only):", len(r.orphanedChunksInDB))
		row("NAR files w/ chunk issues:", len(r.narFilesWithChunkIssues))
		row("CDC NARs w/ size mismatch:", len(r.narFilesWithSizeMismatch))
		row("Orphaned chunk files:", len(r.orphanedChunksInStorage))

		if r.verifyContent {
			row("NAR files w/ corrupt chunks:", len(r.narFilesWithCorruptChunks))
			row("NAR files w/ hash mismatch:", len(r.narFilesWithHashMismatch))
		}

		row("Recoverable chunked residue:", len(r.recoverableChunkedNarFiles))
		row("Reclaimable chunked residue:", len(r.reclaimableChunkedResidue))
	}

	fmt.Println(sep)
	row("Total issues:", total)
	fmt.Println(bot)

	fmt.Println()

	if total == 0 {
		fmt.Println("✅ All checks passed.")
	} else {
		fmt.Printf("❌ %d issue(s) found.\n", total)
	}
}

// relinkNarInfoToBackingNarFile recreates a missing narinfo_nar_files link when the
// nar_file the narinfo's URL references is present in the database. It matches the
// nar_file by hash+query (any compression — the URL may advertise a different
// compression than the NAR is stored under, e.g. CDC residue). Returns true when a
// link was (re)created, false when no backing nar_file exists for the URL.
func relinkNarInfoToBackingNarFile(ctx context.Context, dbClient *database.Client, ni *ent.NarInfo) (bool, error) {
	if ni.URL == nil || *ni.URL == "" {
		return false, nil
	}

	u, err := nar.ParseURL(*ni.URL)
	if err != nil {
		// An unparseable URL cannot point us at a nar_file; leave it for deletion.
		return false, nil //nolint:nilerr // unparseable URL == no backing to relink to
	}

	// Prefer the nar_file whose compression exactly matches the URL, so linking is
	// deterministic when several nar_file rows share a hash with different
	// compressions. Fall back to any compression for CDC residue (the URL may
	// advertise a different compression than the NAR is actually stored under).
	nf, err := dbClient.Ent().NarFile.Query().
		Where(
			entnarfile.HashEQ(u.Hash),
			entnarfile.QueryEQ(u.Query.Encode()),
			entnarfile.CompressionEQ(u.Compression.String()),
		).
		First(ctx)
	if err != nil {
		if !database.IsNotFoundError(err) {
			return false, fmt.Errorf("lookup nar_file for narinfo url %q: %w", *ni.URL, err)
		}

		nf, err = dbClient.Ent().NarFile.Query().
			Where(entnarfile.HashEQ(u.Hash), entnarfile.QueryEQ(u.Query.Encode())).
			First(ctx)
		if err != nil {
			if database.IsNotFoundError(err) {
				return false, nil
			}

			return false, fmt.Errorf("lookup nar_file for narinfo url %q: %w", *ni.URL, err)
		}
	}

	if err := dbClient.Ent().NarInfoNarFile.Create().
		SetNarinfoID(ni.ID).
		SetNarFileID(nf.ID).
		OnConflictColumns(entnarinfonarfile.FieldNarinfoID, entnarinfonarfile.FieldNarFileID).
		Ignore().
		Exec(ctx); err != nil {
		return false, fmt.Errorf("create narinfo_nar_files link for narinfo(%d): %w", ni.ID, err)
	}

	return true, nil
}

// repairFsckIssues applies fixes for each category of issue, re-verifying each item before acting.
func repairFsckIssues(
	ctx context.Context,
	dbClient *database.Client,
	narStore storage.NarStore,
	chunkStore chunk.Store,
	results *fsckResults,
) error {
	logger := zerolog.Ctx(ctx)

	// a. Repair or delete narinfos without nar_files
	for _, ni := range results.narinfosWithoutNarFiles {
		// Re-verify before acting
		hasNarFile, err := dbClient.Ent().NarFile.Query().
			Where(entnarfile.HasNarInfoNarFilesWith(entnarinfonarfile.NarinfoIDEQ(ni.ID))).
			Exist(ctx)
		if err != nil {
			return fmt.Errorf("repair re-verify narinfo(%d): %w", ni.ID, err)
		}

		if hasNarFile {
			// Now has a nar_file, skip
			continue
		}

		// Before deleting, try to REPAIR. A known race — the narinfo_nar_files link
		// is created in the narinfo-write path, decoupled from the async CDC chunking
		// that finalizes the nar_file — can leave a perfectly valid, reachable narinfo
		// unlinked from an EXISTING nar_file. Deleting it would destroy live metadata
		// and orphan the NAR. If the nar_file the narinfo's URL references is present,
		// recreate the missing link instead.
		relinked, err := relinkNarInfoToBackingNarFile(ctx, dbClient, ni)
		if err != nil {
			return fmt.Errorf("repair relink narinfo(%d): %w", ni.ID, err)
		}

		if relinked {
			logger.Info().Int("narinfo_id", ni.ID).Str("hash", ni.Hash).
				Msg("repaired missing narinfo<->nar_file link")

			continue
		}

		// No nar_file backs this narinfo's URL anywhere: it is genuinely orphaned.
		if err := dbClient.Ent().NarInfo.DeleteOneID(ni.ID).Exec(ctx); err != nil {
			logger.Error().Err(err).Int("narinfo_id", ni.ID).Msg("failed to delete narinfo without nar_file")
		} else {
			logger.Info().Int("narinfo_id", ni.ID).Str("hash", ni.Hash).Msg("deleted narinfo without nar_file")
		}
	}

	// b. Delete orphaned nar_files in DB
	for _, nf := range results.orphanedNarFilesInDB {
		// Re-verify before deleting
		hasLink, err := dbClient.Ent().NarInfo.Query().
			Where(entnarinfo.HasNarInfoNarFilesWith(
				entnarinfonarfile.HasNarFileWith(
					entnarfile.HashEQ(nf.Hash),
					entnarfile.CompressionEQ(nf.Compression),
					entnarfile.QueryEQ(nf.Query),
				),
			)).
			Exist(ctx)
		if err != nil {
			return fmt.Errorf("repair re-verify nar_file(%d): %w", nf.ID, err)
		}

		if hasLink {
			// Now has a narinfo link, skip
			continue
		}

		if _, err := dbClient.Ent().NarFile.Delete().
			Where(
				entnarfile.HashEQ(nf.Hash),
				entnarfile.CompressionEQ(nf.Compression),
				entnarfile.QueryEQ(nf.Query),
			).
			Exec(ctx); err != nil {
			logger.Error().Err(err).Int("nar_file_id", nf.ID).Msg("failed to delete orphaned nar_file")
		} else {
			logger.Info().Int("nar_file_id", nf.ID).Str("hash", nf.Hash).Msg("deleted orphaned nar_file from DB")
		}
	}

	// c. Delete nar_file DB records missing from storage.
	// Snapshot which narinfos are already orphaned before our deletions so we can
	// distinguish pre-existing orphans (handled in section a) from narinfos that
	// become orphaned as a cascade of removing the missing nar_file record.
	existingOrphans, err := dbClient.Ent().NarInfo.Query().
		Where(entnarinfo.Not(entnarinfo.HasNarInfoNarFiles())).
		All(ctx)
	if err != nil {
		return fmt.Errorf("repair pre-check GetNarInfosWithoutNarFiles: %w", err)
	}

	existingOrphanIDs := make(map[int]struct{}, len(existingOrphans))
	for _, ni := range existingOrphans {
		existingOrphanIDs[ni.ID] = struct{}{}
	}

	for _, nf := range results.narFilesMissingInStorage {
		// Re-verify before deleting
		narURL, err := narFileRowToURL(nf.Hash, nf.Compression, nf.Query)
		if err != nil {
			return fmt.Errorf("narFileRowToURL for nar_file %d: %w", nf.ID, err)
		}

		if narStore.HasNar(ctx, narURL) {
			// File appeared, skip
			continue
		}

		if _, err := dbClient.Ent().NarFile.Delete().
			Where(
				entnarfile.HashEQ(nf.Hash),
				entnarfile.CompressionEQ(nf.Compression),
				entnarfile.QueryEQ(nf.Query),
			).
			Exec(ctx); err != nil {
			logger.Error().Err(err).Int("nar_file_id", nf.ID).Msg("failed to delete nar_file missing from storage")
		} else {
			logger.Info().
				Int("nar_file_id", nf.ID).
				Str("hash", nf.Hash).
				Msg("deleted nar_file DB record (missing from storage)")
		}
	}

	// Delete narinfos that became orphaned as a result of the nar_file deletions above.
	// These would otherwise only be caught on a second fsck run.
	newOrphans, err := dbClient.Ent().NarInfo.Query().
		Where(entnarinfo.Not(entnarinfo.HasNarInfoNarFiles())).
		All(ctx)
	if err != nil {
		return fmt.Errorf("repair post-check GetNarInfosWithoutNarFiles: %w", err)
	}

	for _, ni := range newOrphans {
		if _, alreadyOrphaned := existingOrphanIDs[ni.ID]; alreadyOrphaned {
			// Pre-existing orphan; handled in section a.
			continue
		}

		if err := dbClient.Ent().NarInfo.DeleteOneID(ni.ID).Exec(ctx); err != nil {
			logger.Error().Err(err).
				Int("narinfo_id", ni.ID).
				Msg("failed to delete narinfo orphaned by missing nar_file")
		} else {
			logger.Info().
				Int("narinfo_id", ni.ID).
				Str("hash", ni.Hash).
				Msg("deleted narinfo orphaned by nar_file missing from storage")
		}
	}

	// d. Delete orphaned NAR files from storage
	type narDeleter interface {
		DeleteNar(ctx context.Context, narURL nar.URL) error
	}

	nd, hasDeleter := narStore.(narDeleter)

	for _, narURL := range results.orphanedNarFilesInStorage {
		// Re-verify before deleting
		exists, err := dbClient.Ent().NarFile.Query().
			Where(
				entnarfile.HashEQ(narURL.Hash),
				entnarfile.CompressionEQ(narURL.Compression.String()),
				entnarfile.QueryEQ(narURL.Query.Encode()),
			).
			Exist(ctx)
		if err != nil {
			return fmt.Errorf("repair re-verify orphaned NAR (%s): %w", narURL, err)
		}

		if exists {
			// Now in DB, skip
			continue
		}

		if hasDeleter {
			if err := nd.DeleteNar(ctx, narURL); err != nil {
				logger.Error().Err(err).Str("nar_url", narURL.String()).Msg("failed to delete orphaned NAR from storage")
			} else {
				logger.Info().Str("nar_url", narURL.String()).Msg("deleted orphaned NAR from storage")
			}
		}
	}

	if !results.cdcMode || chunkStore == nil {
		return nil
	}

	// e. Delete orphaned chunks in DB
	recheckChunks, err := dbClient.Ent().Chunk.Query().
		Where(entchunk.Not(entchunk.HasNarFileLinks())).
		All(ctx)
	if err != nil {
		return fmt.Errorf("repair re-verify GetOrphanedChunks: %w", err)
	}

	recheckMap := make(map[int]struct{}, len(recheckChunks))
	for _, rc := range recheckChunks {
		recheckMap[rc.ID] = struct{}{}
	}

	for _, c := range results.orphanedChunksInDB {
		if _, ok := recheckMap[c.ID]; !ok {
			continue
		}

		if err := dbClient.Ent().Chunk.DeleteOneID(c.ID).Exec(ctx); err != nil {
			logger.Error().Err(err).Int("chunk_id", c.ID).Msg("failed to delete orphaned chunk from DB")
		} else {
			logger.Info().Int("chunk_id", c.ID).Str("hash", c.Hash).Msg("deleted orphaned chunk from DB")
		}
	}

	// f. Delete nar_files with chunk issues (broken CDC nar_files).
	if chunkStore != nil {
		if err := repairBrokenCDCNarFiles(ctx, dbClient, chunkStore, results.narFilesWithChunkIssues, logger); err != nil {
			return err
		}
	}

	// f2. Delete CDC nar_files with size mismatch (truncated artifacts).
	if chunkStore != nil {
		if err := repairSizeMismatchCDCNarFiles(
			ctx, dbClient, chunkStore, results.narFilesWithSizeMismatch, logger,
		); err != nil {
			return err
		}
	}

	// f3. Delete CDC nar_files with corrupt chunk content.
	if chunkStore != nil && results.verifyContent {
		verifyCorrupt := func(ctx context.Context, nf *ent.NarFile) (bool, error) {
			return isNarFileContentCorrupt(ctx, dbClient, chunkStore, nf)
		}

		if err := repairCDCNarFiles(
			ctx, dbClient, chunkStore, results.narFilesWithCorruptChunks, verifyCorrupt, logger,
		); err != nil {
			return err
		}
	}

	// f4. Delete CDC nar_files with assembled NAR hash mismatch.
	if chunkStore != nil && results.verifyContent {
		verifyHash := func(ctx context.Context, nf *ent.NarFile) (bool, error) {
			return isNarFileHashMismatched(ctx, dbClient, chunkStore, nf)
		}

		if err := repairCDCNarFiles(
			ctx, dbClient, chunkStore, results.narFilesWithHashMismatch, verifyHash, logger,
		); err != nil {
			return err
		}
	}

	// g. Delete orphaned chunk files from storage
	if chunkStore != nil {
		for _, hash := range results.orphanedChunksInStorage {
			// Re-verify before deleting
			exists, err := dbClient.Ent().Chunk.Query().
				Where(entchunk.HashEQ(hash)).
				Exist(ctx)
			if err != nil {
				return fmt.Errorf("repair re-verify orphaned chunk (%s): %w", hash, err)
			}

			if exists {
				// Now in DB, skip
				continue
			}

			if err := chunkStore.DeleteChunk(ctx, hash); err != nil {
				logger.Error().Err(err).Str("hash", hash).Msg("failed to delete orphaned chunk from storage")
			} else {
				logger.Info().Str("hash", hash).Msg("deleted orphaned chunk from storage")
			}
		}
	}

	// f. Repair narinfos advertising a non-producible compression (xz) whose backing
	// NAR is stored otherwise: rewrite to the servable none form so they serve via
	// transparent decompression (#1392).
	repairedCompression, err := repairNarInfoCompressionDesync(ctx, dbClient)
	if err != nil {
		return fmt.Errorf("repair narinfo compression desync: %w", err)
	}

	if repairedCompression > 0 {
		logger.Info().
			Int("count", repairedCompression).
			Msg("repaired narinfos advertising a non-producible compression")
	}

	return nil
}

// repairNarInfoCompressionDesync rewrites narinfos that advertise a
// non-producible compression (one ncps has no compressor for, i.e. xz) whose
// backing NAR is stored under a different compression, to the servable
// uncompressed form (URL nar/<nar_hash>.nar, Compression: none, FileHash/
// FileSize cleared). The uncompressed NAR is then served by transparent
// decompression of the stored bytes (#1392). Healthy narinfos (advertised
// compression matches a stored representation, or is directly producible: none /
// zstd) are left untouched, and the repair is idempotent. Returns the count
// rewritten.
func repairNarInfoCompressionDesync(ctx context.Context, dbClient *database.Client) (int, error) {
	// Only xz lacks a serve-time compressor; none and zstd are producible from any
	// stored representation, so a narinfo advertising them is already servable.
	//
	// Narrow the candidate set in SQL to just the desynced rows — advertised xz,
	// backed by at least one nar_file, but NOT backed by an xz nar_file — so a cache
	// with many healthy xz entries loads only the few desynced narinfos (~tens) and
	// does no per-row work for the healthy ones. repairOneNarInfoCompressionDesync
	// re-verifies precisely (by the advertised hash) before rewriting.
	nis, err := dbClient.Ent().NarInfo.Query().
		Where(
			entnarinfo.CompressionEQ(nar.CompressionTypeXz.String()),
			entnarinfo.HasNarInfoNarFiles(),
			entnarinfo.Not(entnarinfo.HasNarInfoNarFilesWith(
				entnarinfonarfile.HasNarFileWith(
					entnarfile.CompressionEQ(nar.CompressionTypeXz.String()),
				),
			)),
		).
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("query desynced xz-advertised narinfos: %w", err)
	}

	repaired := 0

	for _, ni := range nis {
		ok, err := repairOneNarInfoCompressionDesync(ctx, dbClient, ni)
		if err != nil {
			return repaired, err
		}

		if ok {
			repaired++
		}
	}

	return repaired, nil
}

func repairOneNarInfoCompressionDesync(ctx context.Context, dbClient *database.Client, ni *ent.NarInfo) (bool, error) {
	// Without an advertised URL we cannot reconcile the compression.
	if ni.URL == nil || *ni.URL == "" {
		return false, nil
	}

	advertised, err := nar.ParseURL(*ni.URL)
	if err != nil {
		return false, nil //nolint:nilerr // unparseable URL: nothing to reconcile here
	}

	// If the NAR is actually stored in the advertised (xz) compression, the narinfo
	// is correct — leave it.
	xzExists, err := dbClient.Ent().NarFile.Query().
		Where(
			entnarfile.HashEQ(advertised.Hash),
			entnarfile.CompressionEQ(nar.CompressionTypeXz.String()),
			entnarfile.QueryEQ(advertised.Query.Encode()),
		).
		Exist(ctx)
	if err != nil {
		return false, fmt.Errorf("check xz nar_file for narinfo(%d): %w", ni.ID, err)
	}

	if xzExists {
		return false, nil
	}

	// Confirm a backing NAR exists before rewriting, so we never advertise none for
	// bytes that are absent. A linked (non-xz) nar_file is enough; the serve path
	// produces none from any stored representation. Rows with no backing are left to
	// the orphan-narinfo repair path above.
	hasBacking, err := dbClient.Ent().NarFile.Query().
		Where(entnarfile.HasNarInfoNarFilesWith(entnarinfonarfile.NarinfoIDEQ(ni.ID))).
		Exist(ctx)
	if err != nil {
		return false, fmt.Errorf("check backing nar_file for narinfo(%d): %w", ni.ID, err)
	}

	if !hasBacking {
		return false, nil
	}

	// Reuse the advertised URL's hash (the same hash the backing nar_file is keyed
	// by); only the compression/extension changes. This matches the CDC narinfo
	// normalization (maybeCDCNormalizeNarInfoURL) and lets the resolve-by-hash serve
	// path (#1393) decompress the stored bytes for the none request.
	noneURL := nar.URL{Hash: advertised.Hash, Compression: nar.CompressionTypeNone, Query: advertised.Query}

	if _, err := dbClient.Ent().NarInfo.Update().
		Where(entnarinfo.IDEQ(ni.ID)).
		SetURL(noneURL.String()).
		SetCompression(nar.CompressionTypeNone.String()).
		ClearFileHash().
		ClearFileSize().
		Save(ctx); err != nil {
		return false, fmt.Errorf("rewrite narinfo(%d) to none: %w", ni.ID, err)
	}

	return true, nil
}

// repairBrokenCDCNarFiles deletes broken CDC nar_files, their orphaned narinfos, and orphaned chunks.
func repairBrokenCDCNarFiles(
	ctx context.Context,
	dbClient *database.Client,
	cs chunk.Store,
	narFilesWithChunkIssues []*ent.NarFile,
	logger *zerolog.Logger,
) error {
	verifyFn := func(ctx context.Context, nf *ent.NarFile) (bool, error) {
		return isNarFileChunkBroken(ctx, dbClient, cs, nf)
	}

	return repairCDCNarFiles(ctx, dbClient, cs, narFilesWithChunkIssues, verifyFn, logger)
}

// repairCDCNarFiles deletes CDC nar_files that are confirmed broken by reVerifyFn,
// then cascades to clean up orphaned narinfos and chunks.
func repairCDCNarFiles(
	ctx context.Context,
	dbClient *database.Client,
	cs chunk.Store,
	narFiles []*ent.NarFile,
	reVerifyFn func(ctx context.Context, nf *ent.NarFile) (bool, error),
	logger *zerolog.Logger,
) error {
	// Snapshot pre-existing narinfo orphans so we only sweep newly orphaned ones below.
	preExistingOrphans, err := dbClient.Ent().NarInfo.Query().
		Where(entnarinfo.Not(entnarinfo.HasNarInfoNarFiles())).
		All(ctx)
	if err != nil {
		return fmt.Errorf("repair CDC pre-check GetNarInfosWithoutNarFiles: %w", err)
	}

	preExistingOrphanIDs := make(map[int]struct{}, len(preExistingOrphans))
	for _, ni := range preExistingOrphans {
		preExistingOrphanIDs[ni.ID] = struct{}{}
	}

	for _, nf := range narFiles {
		broken, err := reVerifyFn(ctx, nf)
		if err != nil {
			return fmt.Errorf("repair re-verify nar_file(%d): %w", nf.ID, err)
		}

		if !broken {
			continue
		}

		if _, err := dbClient.Ent().NarFile.Delete().
			Where(
				entnarfile.HashEQ(nf.Hash),
				entnarfile.CompressionEQ(nf.Compression),
				entnarfile.QueryEQ(nf.Query),
			).
			Exec(ctx); err != nil {
			logger.Error().Err(err).Int("nar_file_id", nf.ID).Msg("failed to delete broken CDC nar_file")
		} else {
			logger.Info().Int("nar_file_id", nf.ID).Str("hash", nf.Hash).Msg("deleted broken CDC nar_file")
		}
	}

	// Delete narinfos orphaned by the CDC nar_file deletions above.
	newOrphans, err := dbClient.Ent().NarInfo.Query().
		Where(entnarinfo.Not(entnarinfo.HasNarInfoNarFiles())).
		All(ctx)
	if err != nil {
		return fmt.Errorf("repair CDC post-check GetNarInfosWithoutNarFiles: %w", err)
	}

	for _, ni := range newOrphans {
		if _, alreadyOrphaned := preExistingOrphanIDs[ni.ID]; alreadyOrphaned {
			continue
		}

		if delErr := dbClient.Ent().NarInfo.DeleteOneID(ni.ID).Exec(ctx); delErr != nil {
			logger.Error().Err(delErr).Int("narinfo_id", ni.ID).
				Msg("failed to delete narinfo orphaned by broken CDC nar_file")
		} else {
			logger.Info().Int("narinfo_id", ni.ID).Str("hash", ni.Hash).
				Msg("deleted narinfo orphaned by broken CDC nar_file")
		}
	}

	// Clean up newly-orphaned chunks after nar_file deletions.
	orphanedChunks, err := dbClient.Ent().Chunk.Query().
		Where(entchunk.Not(entchunk.HasNarFileLinks())).
		All(ctx)
	if err != nil {
		return fmt.Errorf("repair post-CDC GetOrphanedChunks: %w", err)
	}

	for _, c := range orphanedChunks {
		if err := cs.DeleteChunk(ctx, c.Hash); err != nil {
			logger.Error().Err(err).Str("hash", c.Hash).Msg("failed to delete orphaned chunk from storage after CDC repair")
		} else {
			logger.Info().Str("hash", c.Hash).Msg("deleted orphaned chunk from storage after CDC repair")
		}

		if err := dbClient.Ent().Chunk.DeleteOneID(c.ID).Exec(ctx); err != nil {
			logger.Error().Err(err).Int("chunk_id", c.ID).Msg("failed to delete orphaned chunk DB record after CDC repair")
		} else {
			logger.Info().Int("chunk_id", c.ID).Str("hash", c.Hash).Msg("deleted orphaned chunk DB record after CDC repair")
		}
	}

	return nil
}

// repairSizeMismatchCDCNarFiles deletes CDC nar_files whose file_size does not match the
// linked narinfo's nar_size (truncated artifacts). Re-verification is done via a fresh
// GetCDCNarFilesWithSizeMismatch query rather than isNarFileChunkBroken, because
// size-mismatched rows may otherwise pass the chunk-count check.
func repairSizeMismatchCDCNarFiles(
	ctx context.Context,
	dbClient *database.Client,
	cs chunk.Store,
	narFilesWithSizeMismatch []*ent.NarFile,
	logger *zerolog.Logger,
) error {
	if len(narFilesWithSizeMismatch) == 0 {
		return nil
	}

	// Snapshot pre-existing narinfo orphans so we only sweep newly orphaned ones below.
	preExistingOrphans, err := dbClient.Ent().NarInfo.Query().
		Where(entnarinfo.Not(entnarinfo.HasNarInfoNarFiles())).
		All(ctx)
	if err != nil {
		return fmt.Errorf("repair size-mismatch pre-check GetNarInfosWithoutNarFiles: %w", err)
	}

	preExistingOrphanIDs := make(map[int]struct{}, len(preExistingOrphans))
	for _, ni := range preExistingOrphans {
		preExistingOrphanIDs[ni.ID] = struct{}{}
	}

	// Re-verify: re-run the mismatch query and build a set for O(1) lookup.
	recheckMismatch, err := queryCDCNarFilesWithSizeMismatch(ctx, dbClient)
	if err != nil {
		return fmt.Errorf("repair size-mismatch re-verify GetCDCNarFilesWithSizeMismatch: %w", err)
	}

	recheckSet := make(map[int]struct{}, len(recheckMismatch))
	for _, nf := range recheckMismatch {
		recheckSet[nf.ID] = struct{}{}
	}

	for _, nf := range narFilesWithSizeMismatch {
		if _, stillMismatched := recheckSet[nf.ID]; !stillMismatched {
			continue
		}

		if _, err := dbClient.Ent().NarFile.Delete().
			Where(
				entnarfile.HashEQ(nf.Hash),
				entnarfile.CompressionEQ(nf.Compression),
				entnarfile.QueryEQ(nf.Query),
			).
			Exec(ctx); err != nil {
			logger.Error().Err(err).Int("nar_file_id", nf.ID).Msg("failed to delete size-mismatched CDC nar_file")
		} else {
			logger.Info().Int("nar_file_id", nf.ID).Str("hash", nf.Hash).Msg("deleted size-mismatched CDC nar_file")
		}
	}

	// Delete narinfos orphaned by the deletions above.
	newOrphans, err := dbClient.Ent().NarInfo.Query().
		Where(entnarinfo.Not(entnarinfo.HasNarInfoNarFiles())).
		All(ctx)
	if err != nil {
		return fmt.Errorf("repair size-mismatch post-check GetNarInfosWithoutNarFiles: %w", err)
	}

	for _, ni := range newOrphans {
		if _, alreadyOrphaned := preExistingOrphanIDs[ni.ID]; alreadyOrphaned {
			continue
		}

		if delErr := dbClient.Ent().NarInfo.DeleteOneID(ni.ID).Exec(ctx); delErr != nil {
			logger.Error().Err(delErr).Int("narinfo_id", ni.ID).
				Msg("failed to delete narinfo orphaned by size-mismatched CDC nar_file")
		} else {
			logger.Info().Int("narinfo_id", ni.ID).Str("hash", ni.Hash).
				Msg("deleted narinfo orphaned by size-mismatched CDC nar_file")
		}
	}

	// Clean up newly-orphaned chunks after nar_file deletions.
	orphanedChunks, err := dbClient.Ent().Chunk.Query().
		Where(entchunk.Not(entchunk.HasNarFileLinks())).
		All(ctx)
	if err != nil {
		return fmt.Errorf("repair size-mismatch GetOrphanedChunks: %w", err)
	}

	for _, c := range orphanedChunks {
		if err := cs.DeleteChunk(ctx, c.Hash); err != nil {
			logger.Error().Err(err).Str("hash", c.Hash).
				Msg("failed to delete orphaned chunk from storage after size-mismatch repair")
		} else {
			logger.Info().Str("hash", c.Hash).
				Msg("deleted orphaned chunk from storage after size-mismatch repair")
		}

		if err := dbClient.Ent().Chunk.DeleteOneID(c.ID).Exec(ctx); err != nil {
			logger.Error().Err(err).Int("chunk_id", c.ID).
				Msg("failed to delete orphaned chunk DB record after size-mismatch repair")
		} else {
			logger.Info().Int("chunk_id", c.ID).Str("hash", c.Hash).
				Msg("deleted orphaned chunk DB record after size-mismatch repair")
		}
	}

	return nil
}

// chunksForNarFile returns the chunk rows linked to nf via nar_file_chunks,
// ordered by chunk_index. Mirrors the legacy GetChunksByNarFileID SQL.
//
// Implementation walks nar_file_chunks rows in keyset-paginated batches of
// fsckEagerLoadBatchSize and, per page, fetches chunks with a bounded
// `IDIn(...)` query. A single oversized CDC NAR with > 65535 chunks would
// otherwise emit `WHERE id IN ($1...$M)` via `WithChunk()` and trip
// PostgreSQL's 65535 extended-protocol parameter cap.
func chunksForNarFile(
	ctx context.Context,
	dbClient *database.Client,
	narFileID int,
) ([]*ent.Chunk, error) {
	var (
		chunks    []*ent.Chunk
		lastIndex = -1
	)

	for {
		links, err := dbClient.Ent().NarFileChunk.Query().
			Where(
				entnarfilechunk.NarFileIDEQ(narFileID),
				entnarfilechunk.ChunkIndexGT(lastIndex),
			).
			Order(ent.Asc(entnarfilechunk.FieldChunkIndex)).
			Limit(fsckEagerLoadBatchSize).
			All(ctx)
		if err != nil {
			return nil, fmt.Errorf("query nar_file_chunks for nar_file %d: %w", narFileID, err)
		}

		if len(links) == 0 {
			break
		}

		ids := make([]int, len(links))
		for i, link := range links {
			ids[i] = link.ChunkID
		}

		rows, err := dbClient.Ent().Chunk.Query().
			Where(entchunk.IDIn(ids...)).
			All(ctx)
		if err != nil {
			return nil, fmt.Errorf("query chunks for nar_file %d: %w", narFileID, err)
		}

		byID := make(map[int]*ent.Chunk, len(rows))
		for _, ch := range rows {
			byID[ch.ID] = ch
		}

		for _, link := range links {
			if ch, ok := byID[link.ChunkID]; ok {
				chunks = append(chunks, ch)
			}
		}

		lastIndex = links[len(links)-1].ChunkIndex

		if len(links) < fsckEagerLoadBatchSize {
			break
		}
	}

	return chunks, nil
}

// isNarFileChunkBroken returns true if the nar_file's chunks are incomplete or missing from storage.
func isNarFileChunkBroken(
	ctx context.Context, dbClient *database.Client, cs chunk.Store, nf *ent.NarFile,
) (bool, error) {
	chunks, err := chunksForNarFile(ctx, dbClient, nf.ID)
	if err != nil {
		return false, fmt.Errorf("GetChunksByNarFileID(%d): %w", nf.ID, err)
	}

	if int64(len(chunks)) != nf.TotalChunks {
		return true, nil
	}

	for _, c := range chunks {
		exists, err := cs.HasChunk(ctx, c.Hash)
		if err != nil {
			return false, fmt.Errorf("HasChunk(%s): %w", c.Hash, err)
		}

		if !exists {
			return true, nil
		}
	}

	return false, nil
}

// collectNarFilesWithChunkIssues returns CDC nar_files whose chunks are incomplete or missing from storage.
func collectNarFilesWithChunkIssues(
	ctx context.Context,
	dbClient *database.Client,
	allNarFiles []*ent.NarFile,
	cs chunk.Store,
	checked *atomic.Int64,
	verifiedSince time.Duration,
) ([]*ent.NarFile, error) {
	if cs == nil {
		return nil, nil
	}

	var broken []*ent.NarFile

	for _, nf := range allNarFiles {
		if nf.TotalChunks <= 0 {
			continue
		}

		if !shouldCheckNar(nf, verifiedSince) {
			continue
		}

		if checked != nil {
			checked.Add(1)
		}

		isBroken, err := isNarFileChunkBroken(ctx, dbClient, cs, nf)
		if err != nil {
			return nil, err
		}

		if isBroken {
			broken = append(broken, nf)
		}
	}

	return broken, nil
}

// collectOrphanedChunksInStorage returns all chunk files in storage that have no DB record.
func collectOrphanedChunksInStorage(
	ctx context.Context,
	dbClient *database.Client,
	chunkStore chunk.Store,
	checked *atomic.Int64,
) ([]string, error) {
	if chunkStore == nil {
		return nil, nil
	}

	var orphaned []string

	if err := chunkStore.WalkChunks(ctx, func(hash string) error {
		if checked != nil {
			checked.Add(1)
		}

		exists, dbErr := dbClient.Ent().Chunk.Query().
			Where(entchunk.HashEQ(hash)).
			Exist(ctx)
		if dbErr != nil {
			return fmt.Errorf("DB lookup for chunk %s: %w", hash, dbErr)
		}

		if !exists {
			orphaned = append(orphaned, hash)
		}

		return nil
	}); err != nil {
		return nil, fmt.Errorf("WalkChunks: %w", err)
	}

	return orphaned, nil
}

// collectNarFilesWithCorruptChunks returns CDC nar_files where at least one chunk's
// decompressed content does not BLAKE3-hash to its stored key.
// NAR files already in chunkIssueIDs (structurally broken) are skipped.
func collectNarFilesWithCorruptChunks(
	ctx context.Context,
	dbClient *database.Client,
	cs chunk.Store,
	allNarFiles []*ent.NarFile,
	chunkIssueIDs map[int]struct{},
	verifiedSince time.Duration,
) ([]*ent.NarFile, error) {
	if cs == nil {
		return nil, nil
	}

	var corrupt []*ent.NarFile

	for _, nf := range allNarFiles {
		if nf.TotalChunks <= 0 {
			continue
		}

		if _, hasIssue := chunkIssueIDs[nf.ID]; hasIssue {
			continue
		}

		if !shouldCheckNar(nf, verifiedSince) {
			continue
		}

		isCorrupt, err := isNarFileContentCorrupt(ctx, dbClient, cs, nf)
		if err != nil {
			return nil, fmt.Errorf("isNarFileContentCorrupt(%d): %w", nf.ID, err)
		}

		if isCorrupt {
			corrupt = append(corrupt, nf)
		}
	}

	return corrupt, nil
}

// isNarFileContentCorrupt returns true if any chunk's decompressed content does not
// BLAKE3-hash to its stored key.
func isNarFileContentCorrupt(
	ctx context.Context, dbClient *database.Client, cs chunk.Store, nf *ent.NarFile,
) (bool, error) {
	chunks, err := chunksForNarFile(ctx, dbClient, nf.ID)
	if err != nil {
		return false, fmt.Errorf("GetChunksByNarFileID(%d): %w", nf.ID, err)
	}

	for _, c := range chunks {
		r, err := cs.GetChunk(ctx, c.Hash)
		if err != nil {
			return false, fmt.Errorf("GetChunk(%s): %w", c.Hash, err)
		}

		h := blake3.New()
		_, readErr := io.Copy(h, r)
		closeErr := r.Close()

		if readErr != nil {
			return false, fmt.Errorf("reading chunk %s: %w", c.Hash, readErr)
		}

		if closeErr != nil {
			return false, fmt.Errorf("closing chunk %s: %w", c.Hash, closeErr)
		}

		if hex.EncodeToString(h.Sum(nil)) != c.Hash {
			return true, nil
		}
	}

	return false, nil
}

// collectNarFilesWithHashMismatch returns CDC nar_files whose assembled chunk stream does not
// match the narinfo NarHash (SHA-256 in nix-base32). NAR files in chunkIssueIDs or corruptByID
// are skipped.
func collectNarFilesWithHashMismatch(
	ctx context.Context,
	dbClient *database.Client,
	cs chunk.Store,
	allNarFiles []*ent.NarFile,
	chunkIssueIDs map[int]struct{},
	corruptByID map[int]struct{},
	verifiedSince time.Duration,
) ([]*ent.NarFile, error) {
	if cs == nil {
		return nil, nil
	}

	var mismatched []*ent.NarFile

	for _, nf := range allNarFiles {
		if nf.TotalChunks <= 0 {
			continue
		}

		if _, hasIssue := chunkIssueIDs[nf.ID]; hasIssue {
			continue
		}

		if _, isCorrupt := corruptByID[nf.ID]; isCorrupt {
			continue
		}

		if !shouldCheckNar(nf, verifiedSince) {
			continue
		}

		isMismatched, err := isNarFileHashMismatched(ctx, dbClient, cs, nf)
		if err != nil {
			return nil, fmt.Errorf("isNarFileHashMismatched(%d): %w", nf.ID, err)
		}

		if isMismatched {
			mismatched = append(mismatched, nf)
		}
	}

	return mismatched, nil
}

// isNarFileHashMismatched returns true if the SHA-256 of all assembled chunk bytes
// does not match the narinfo's NarHash.
func isNarFileHashMismatched(
	ctx context.Context, dbClient *database.Client, cs chunk.Store, nf *ent.NarFile,
) (bool, error) {
	// Look up the linked narinfo via the join table and read its nar_hash.
	ni, err := dbClient.Ent().NarInfo.Query().
		Where(entnarinfo.HasNarInfoNarFilesWith(entnarinfonarfile.NarFileIDEQ(nf.ID))).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return false, nil
		}

		return false, fmt.Errorf("GetNarInfoNarHashByNarFileID(%d): %w", nf.ID, err)
	}

	if ni.NarHash == nil || *ni.NarHash == "" {
		return false, nil
	}

	expectedHash, err := nixhash.ParseAny(*ni.NarHash, nil)
	if err != nil {
		return false, fmt.Errorf("parsing NarHash %q: %w", *ni.NarHash, err)
	}

	chunks, err := chunksForNarFile(ctx, dbClient, nf.ID)
	if err != nil {
		return false, fmt.Errorf("GetChunksByNarFileID(%d): %w", nf.ID, err)
	}

	h := sha256.New()

	for _, c := range chunks {
		r, err := cs.GetChunk(ctx, c.Hash)
		if err != nil {
			return false, fmt.Errorf("GetChunk(%s): %w", c.Hash, err)
		}

		_, copyErr := io.Copy(h, r)
		closeErr := r.Close()

		if copyErr != nil {
			return false, fmt.Errorf("hashing chunk %s: %w", c.Hash, copyErr)
		}

		if closeErr != nil {
			return false, fmt.Errorf("closing chunk %s: %w", c.Hash, closeErr)
		}
	}

	return !bytes.Equal(h.Sum(nil), expectedHash.Digest()), nil
}

// narFileRowToURL converts nar_file fields into a nar.URL.
func narFileRowToURL(hash, compression, query string) (nar.URL, error) {
	parsedQuery, err := url.ParseQuery(query)
	if err != nil {
		return nar.URL{}, fmt.Errorf("parsing query %q: %w", query, err)
	}

	return nar.URL{
		Hash:        hash,
		Compression: nar.CompressionTypeFromString(compression),
		Query:       parsedQuery,
	}, nil
}

// startProgressTicker starts a goroutine that logs progress every 30 seconds.
// It returns a function that should be called to stop the ticker.
func startProgressTicker(logFn func()) (stop func()) {
	done := make(chan struct{})

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				logFn()
			}
		}
	}()

	return func() { close(done) }
}

// logProgress logs common progress fields (percent, rate).
//
//nolint:zerologlint
func logProgress(
	logger zerolog.Logger,
	startTime time.Time,
	checked int64,
	total int64,
) *zerolog.Event {
	elapsed := time.Since(startTime).Seconds()
	evt := logger.Info().
		Int64("checked", checked).
		Int64("total", total)

	if total > 0 && checked <= total {
		pct := float64(checked) / float64(total) * 100
		evt = evt.Str("percent", fmt.Sprintf("%.1f%%", pct))
	}

	if elapsed > 0 && checked > 0 {
		rate := float64(checked) / elapsed
		evt = evt.Str("rate", fmt.Sprintf("%.0f/s", rate))
	}

	return evt
}

func shouldCheckNar(nf *ent.NarFile, verifiedSince time.Duration) bool {
	if verifiedSince <= 0 {
		return true
	}

	verifiedAt := nf.VerifiedAt

	if verifiedAt == nil {
		return true
	}

	return time.Since(*verifiedAt) > verifiedSince
}

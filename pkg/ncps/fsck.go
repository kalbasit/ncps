package ncps

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"github.com/urfave/cli/v3"
	"golang.org/x/term"

	semconv "go.opentelemetry.io/otel/semconv/v1.39.0"

	"github.com/kalbasit/ncps/pkg/config"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/otel"
	"github.com/kalbasit/ncps/pkg/storage"
	"github.com/kalbasit/ncps/pkg/storage/chunk"
)

// ErrFsckIssuesFound is returned when fsck finds consistency issues.
var ErrFsckIssuesFound = errors.New("consistency issues found")

// fsckResults holds the results of a fsck run.
type fsckResults struct {
	// narinfosWithoutNarFiles: narinfos in DB with no linked nar_file.
	narinfosWithoutNarFiles []database.NarInfo

	// orphanedNarFilesInDB: nar_files in DB not linked to any narinfo.
	orphanedNarFilesInDB []database.NarFile

	// narFilesMissingInStorage: nar_files in DB whose physical file is absent.
	narFilesMissingInStorage []database.NarFile

	// orphanedNarFilesInStorage: NAR files in storage with no DB record.
	orphanedNarFilesInStorage []nar.URL

	// cdcMode indicates whether CDC-related checks were performed.
	cdcMode bool

	// orphanedChunksInDB: chunks in DB not linked to any nar_file.
	orphanedChunksInDB []database.GetOrphanedChunksRow

	// narFilesWithChunkIssues: CDC nar_files with missing or incomplete chunks.
	narFilesWithChunkIssues []database.NarFile

	// orphanedChunksInStorage: chunk files in storage with no DB record.
	orphanedChunksInStorage []string
}

func (r *fsckResults) totalIssues() int {
	return len(r.narinfosWithoutNarFiles) +
		len(r.orphanedNarFilesInDB) +
		len(r.narFilesMissingInStorage) +
		len(r.orphanedNarFilesInStorage) +
		len(r.orphanedChunksInDB) +
		len(r.narFilesWithChunkIssues) +
		len(r.orphanedChunksInStorage)
}

// NarWalker is implemented by storage backends that support walking NAR files.
type NarWalker interface {
	WalkNars(ctx context.Context, fn func(narURL nar.URL) error) error
}

// ChunkWalker is implemented by chunk stores that support walking chunk files.
type ChunkWalker interface {
	WalkChunks(ctx context.Context, fn func(hash string) error) error
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
				Name:  "dry-run",
				Usage: "Show what would be fixed without making any changes",
			},
			&cli.DurationFlag{
				Name:  "verified-since",
				Usage: "Skip checking NARs that have been verified within this duration",
			},

			// Storage Flags
			&cli.StringFlag{
				Name:    "cache-storage-local",
				Usage:   "The local data path used for configuration and cache storage (use this OR S3 storage)",
				Sources: flagSources("cache.storage.local", "CACHE_STORAGE_LOCAL"),
			},
			&cli.StringFlag{
				Name:    "cache-storage-s3-bucket",
				Usage:   "S3 bucket name for storage (use this OR --cache-storage-local for local storage)",
				Sources: flagSources("cache.storage.s3.bucket", "CACHE_STORAGE_S3_BUCKET"),
			},
			&cli.StringFlag{
				Name:    "cache-storage-s3-endpoint",
				Usage:   "S3-compatible endpoint URL with scheme",
				Sources: flagSources("cache.storage.s3.endpoint", "CACHE_STORAGE_S3_ENDPOINT"),
			},
			&cli.StringFlag{
				Name:    "cache-storage-s3-region",
				Usage:   "S3 region (optional)",
				Sources: flagSources("cache.storage.s3.region", "CACHE_STORAGE_S3_REGION"),
			},
			&cli.StringFlag{
				Name:    "cache-storage-s3-access-key-id",
				Usage:   "S3 access key ID",
				Sources: flagSources("cache.storage.s3.access-key-id", "CACHE_STORAGE_S3_ACCESS_KEY_ID"),
			},
			&cli.StringFlag{
				Name:    "cache-storage-s3-secret-access-key",
				Usage:   "S3 secret access key",
				Sources: flagSources("cache.storage.s3.secret-access-key", "CACHE_STORAGE_S3_SECRET_ACCESS_KEY"),
			},
			&cli.BoolFlag{
				Name:    "cache-storage-s3-force-path-style",
				Usage:   "Force path-style S3 addressing",
				Sources: flagSources("cache.storage.s3.force-path-style", "CACHE_STORAGE_S3_FORCE_PATH_STYLE"),
			},

			// Database Flags
			&cli.StringFlag{
				Name:     "cache-database-url",
				Usage:    "The URL of the database",
				Sources:  flagSources("cache.database-url", "CACHE_DATABASE_URL"),
				Required: true,
			},
			&cli.IntFlag{
				Name:    "cache-database-pool-max-open-conns",
				Usage:   "Maximum number of open connections to the database",
				Sources: flagSources("cache.database.pool.max-open-conns", "CACHE_DATABASE_POOL_MAX_OPEN_CONNS"),
			},
			&cli.IntFlag{
				Name:    "cache-database-pool-max-idle-conns",
				Usage:   "Maximum number of idle connections in the pool",
				Sources: flagSources("cache.database.pool.max-idle-conns", "CACHE_DATABASE_POOL_MAX_IDLE_CONNS"),
			},

			// Lock Backend Flags (optional)
			&cli.StringSliceFlag{
				Name:    "cache-redis-addrs",
				Usage:   "Redis server addresses for distributed locking",
				Sources: flagSources("cache.redis.addrs", "CACHE_REDIS_ADDRS"),
			},
			&cli.StringFlag{
				Name:    "cache-redis-username",
				Usage:   "Redis username",
				Sources: flagSources("cache.redis.username", "CACHE_REDIS_USERNAME"),
			},
			&cli.StringFlag{
				Name:    "cache-redis-password",
				Usage:   "Redis password",
				Sources: flagSources("cache.redis.password", "CACHE_REDIS_PASSWORD"),
			},
			&cli.IntFlag{
				Name:    "cache-redis-db",
				Usage:   "Redis database number",
				Sources: flagSources("cache.redis.db", "CACHE_REDIS_DB"),
			},
			&cli.BoolFlag{
				Name:    "cache-redis-use-tls",
				Usage:   "Use TLS for Redis connections",
				Sources: flagSources("cache.redis.use-tls", "CACHE_REDIS_USE_TLS"),
			},
			&cli.StringFlag{
				Name:    "cache-lock-backend",
				Usage:   "Lock backend to use: 'local' (single instance) or 'redis' (distributed)",
				Sources: flagSources("cache.lock.backend", "CACHE_LOCK_BACKEND"),
				Value:   "local",
			},
			&cli.StringFlag{
				Name:    "cache-lock-redis-key-prefix",
				Usage:   "Prefix for all Redis lock keys",
				Sources: flagSources("cache.lock.redis.key-prefix", "CACHE_LOCK_REDIS_KEY_PREFIX"),
				Value:   "ncps:lock:",
			},
			&cli.DurationFlag{
				Name:    "cache-lock-download-ttl",
				Usage:   "TTL for download locks",
				Sources: flagSources("cache.lock.download-lock-ttl", "CACHE_LOCK_DOWNLOAD_TTL"),
				Value:   5 * time.Minute,
			},
			&cli.DurationFlag{
				Name:    "cache-lock-lru-ttl",
				Usage:   "TTL for LRU lock",
				Sources: flagSources("cache.lock.lru-lock-ttl", "CACHE_LOCK_LRU_TTL"),
				Value:   30 * time.Minute,
			},
			&cli.IntFlag{
				Name:    "cache-lock-retry-max-attempts",
				Usage:   "Maximum number of retry attempts for distributed locks",
				Sources: flagSources("cache.lock.retry.max-attempts", "CACHE_LOCK_RETRY_MAX_ATTEMPTS"),
				Value:   3,
			},
			&cli.DurationFlag{
				Name:    "cache-lock-retry-initial-delay",
				Usage:   "Initial retry delay for distributed locks",
				Sources: flagSources("cache.lock.retry.initial-delay", "CACHE_LOCK_RETRY_INITIAL_DELAY"),
				Value:   100 * time.Millisecond,
			},
			&cli.DurationFlag{
				Name:    "cache-lock-retry-max-delay",
				Usage:   "Maximum retry delay for distributed locks",
				Sources: flagSources("cache.lock.retry.max-delay", "CACHE_LOCK_RETRY_MAX_DELAY"),
				Value:   2 * time.Second,
			},
			&cli.BoolFlag{
				Name:    "cache-lock-retry-jitter",
				Usage:   "Enable jitter in retry delays",
				Sources: flagSources("cache.lock.retry.jitter", "CACHE_LOCK_RETRY_JITTER"),
				Value:   true,
			},
			&cli.BoolFlag{
				Name:    "cache-lock-allow-degraded-mode",
				Usage:   "Allow falling back to local locks if Redis is unavailable",
				Sources: flagSources("cache.lock.allow-degraded-mode", "CACHE_LOCK_ALLOW_DEGRADED_MODE"),
			},
			&cli.IntFlag{
				Name:    "cache-redis-pool-size",
				Usage:   "Redis connection pool size",
				Sources: flagSources("cache.redis.pool-size", "CACHE_REDIS_POOL_SIZE"),
				Value:   10,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			logger := zerolog.Ctx(ctx).With().Str("cmd", "fsck").Logger()
			ctx = logger.WithContext(ctx)

			dryRun := cmd.Bool("dry-run")
			repair := cmd.Bool("repair")
			verifiedSince := cmd.Duration("verified-since")

			// 1. Setup Database
			db, err := createDatabaseQuerier(cmd)
			if err != nil {
				logger.Error().Err(err).Msg("error creating database querier")

				return err
			}

			// 2. Setup Lockers
			locker, rwLocker, err := getLockers(ctx, cmd)
			if err != nil {
				logger.Error().Err(err).Msg("error creating the lockers")

				return err
			}

			// 3. Setup OTel
			extraResourceAttrs, err := detectExtraResourceAttrs(ctx, cmd, db, rwLocker)
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
			cdcMode := false

			cdcConfig, dbErr := db.GetConfigByKey(ctx, config.KeyCDCEnabled)
			if dbErr != nil {
				logger.Warn().Err(dbErr).Msg(
					"could not read cdc_enabled from DB config; CDC mode detection will fall back to data-based detection",
				)
			} else if cdcConfig.Value == configValueTrue {
				cdcMode = true
			}

			// Fallback: if CDC not detected from config, check if any nar_file has
			// total_chunks > 0. This handles cases where the DB config key is missing
			// but chunked data already exists (e.g. wrong DB URL or schema mismatch).
			if !cdcMode {
				hasChunked, checkErr := db.HasAnyChunkedNarFiles(ctx)
				if checkErr != nil {
					logger.Warn().Err(checkErr).Msg("could not check for chunked nar_files")
				} else if hasChunked {
					logger.Warn().Msg(
						"cdc_enabled not set in DB config but chunked nar_files exist; " +
							"enabling CDC mode automatically — verify --cache-database-url is correct",
					)

					cdcMode = true
				}
			}

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

			suspects, err := collectFsckSuspects(ctx, db, narStore, chunkStore, cdcMode, verifiedSince)
			if err != nil {
				return fmt.Errorf("error collecting suspects: %w", err)
			}

			// 7. Phase 2: Re-verify (double-check to handle in-flight operations)
			logger.Info().Msg("phase 2: re-verifying suspects")

			results, err := reVerifyFsckSuspects(ctx, db, narStore, chunkStore, suspects)
			if err != nil {
				return fmt.Errorf("error re-verifying suspects: %w", err)
			}

			// 8. Print summary
			printFsckSummary(results)

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

			if err := repairFsckIssues(ctx, db, narStore, chunkStore, results); err != nil {
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
	db database.Querier,
	narStore storage.NarStore,
	chunkStore chunk.Store,
	cdcMode bool,
	verifiedSince time.Duration,
) (*fsckResults, error) {
	logger := zerolog.Ctx(ctx)
	results := &fsckResults{cdcMode: cdcMode}

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
	narinfosWithoutNarFiles, err := db.GetNarInfosWithoutNarFiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("GetNarInfosWithoutNarFiles: %w", err)
	}

	results.narinfosWithoutNarFiles = narinfosWithoutNarFiles
	logger.Info().Int("count", len(narinfosWithoutNarFiles)).Msg("phase 1a: narinfos_without_nar_files found")

	// b. Orphaned nar_files in DB
	orphanedNarFiles, err := db.GetOrphanedNarFiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("GetOrphanedNarFiles: %w", err)
	}

	results.orphanedNarFilesInDB = orphanedNarFiles
	logger.Info().Int("count", len(orphanedNarFiles)).Msg("phase 1b: orphaned nar_files in DB found")

	// c. Nar_files missing from storage
	allNarFiles, err := db.GetAllNarFiles(ctx)
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
			// Convert GetAllNarFilesRow to NarFile
			results.narFilesMissingInStorage = append(results.narFilesMissingInStorage, database.NarFile{
				ID:                nf.ID,
				Hash:              nf.Hash,
				Compression:       nf.Compression,
				Query:             nf.Query,
				FileSize:          nf.FileSize,
				TotalChunks:       nf.TotalChunks,
				ChunkingStartedAt: nf.ChunkingStartedAt,
				CreatedAt:         nf.CreatedAt,
				UpdatedAt:         nf.UpdatedAt,
				LastAccessedAt:    nf.LastAccessedAt,
			})
		} else {
			// Found and verified, update verified_at
			if err := db.UpdateNarFileVerifiedAt(ctx, nf.ID); err != nil {
				logger.Warn().Err(err).Int64("nar_file_id", nf.ID).Msg("failed to update verified_at")
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

			_, dbErr := db.GetNarFileByHashAndCompressionAndQuery(
				ctx,
				database.GetNarFileByHashAndCompressionAndQueryParams{
					Hash:        narURL.Hash,
					Compression: narURL.Compression.String(),
					Query:       narURL.Query.Encode(),
				},
			)
			if dbErr != nil {
				if database.IsNotFoundError(dbErr) {
					suspects.Add(1)
					results.orphanedNarFilesInStorage = append(results.orphanedNarFilesInStorage, narURL)
				} else {
					return fmt.Errorf("DB lookup for NAR %s: %w", narURL, dbErr)
				}
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
	orphanedChunks, err := db.GetOrphanedChunks(ctx)
	if err != nil {
		return nil, fmt.Errorf("GetOrphanedChunks: %w", err)
	}

	results.orphanedChunksInDB = orphanedChunks
	logger.Info().Int("count", len(orphanedChunks)).Msg("phase 1e: orphaned chunks in DB found")

	// f. NAR files with chunk issues (count mismatch or chunks missing from storage)
	logger.Info().Msg("phase 1f: checking NAR files with chunk issues")

	narFilesWithChunkIssues, err := collectNarFilesWithChunkIssues(
		ctx, db, allNarFiles, chunkStore, &checked, verifiedSince,
	)
	if err != nil {
		return nil, err
	}

	results.narFilesWithChunkIssues = narFilesWithChunkIssues
	logger.Info().Int("count", len(narFilesWithChunkIssues)).Msg("phase 1f: NAR files with chunk issues found")

	// g. Orphaned chunk files in storage
	logger.Info().Msg("phase 1g: checking orphaned chunk files in storage")

	// The total number of chunks in storage is not known beforehand, so we cannot
	// accurately report a percentage for phase 1g. We'll rely on the checked count.

	orphaned, err := collectOrphanedChunksInStorage(ctx, db, chunkStore, &checked)
	if err != nil {
		return nil, err
	}

	results.orphanedChunksInStorage = orphaned
	logger.Info().Int("count", len(orphaned)).Msg("phase 1g: orphaned chunk files found")

	return results, nil
}

// reVerifyFsckSuspects re-checks each suspected issue to handle in-flight operations.
// Items that are no longer issues are silently removed from the results.
func reVerifyFsckSuspects(
	ctx context.Context,
	db database.Querier,
	narStore storage.NarStore,
	chunkStore chunk.Store,
	suspects *fsckResults,
) (*fsckResults, error) {
	logger := zerolog.Ctx(ctx)
	results := &fsckResults{cdcMode: suspects.cdcMode}

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
		_, err := db.GetNarFileByNarInfoID(ctx, ni.ID)
		if err != nil {
			if database.IsNotFoundError(err) {
				results.narinfosWithoutNarFiles = append(results.narinfosWithoutNarFiles, ni)
			} else {
				return nil, fmt.Errorf("re-verify GetNarFileByNarInfoID(%d): %w", ni.ID, err)
			}
		}

		checked.Add(1)
		remaining.Add(-1)
	}

	// Re-verify: orphaned nar_files in DB
	for _, nf := range suspects.orphanedNarFilesInDB {
		// Check if it's still orphaned by checking for narinfo link
		_, err := db.GetNarInfoURLByNarFileHash(ctx, database.GetNarInfoURLByNarFileHashParams{
			Hash:        nf.Hash,
			Compression: nf.Compression,
			Query:       nf.Query,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) || database.IsNotFoundError(err) {
				results.orphanedNarFilesInDB = append(results.orphanedNarFilesInDB, nf)
			} else {
				return nil, fmt.Errorf("re-verify orphaned nar_file(%d): %w", nf.ID, err)
			}
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
		_, err := db.GetNarFileByHashAndCompressionAndQuery(
			ctx,
			database.GetNarFileByHashAndCompressionAndQueryParams{
				Hash:        narURL.Hash,
				Compression: narURL.Compression.String(),
				Query:       narURL.Query.Encode(),
			},
		)
		if err != nil {
			if database.IsNotFoundError(err) {
				results.orphanedNarFilesInStorage = append(results.orphanedNarFilesInStorage, narURL)
			} else {
				return nil, fmt.Errorf("re-verify orphaned NAR in storage (%s): %w", narURL, err)
			}
		}

		checked.Add(1)
		remaining.Add(-1)
	}

	if !suspects.cdcMode {
		return results, nil
	}

	// Re-verify: orphaned chunks in DB
	recheckedOrphanedChunks, err := db.GetOrphanedChunks(ctx)
	if err != nil {
		return nil, fmt.Errorf("re-verify GetOrphanedChunks: %w", err)
	}

	recheckedMap := make(map[int64]struct{}, len(recheckedOrphanedChunks))
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
			broken, err := isNarFileChunkBroken(ctx, db, chunkStore, nf)
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

	// Re-verify: orphaned chunk files in storage
	for _, hash := range suspects.orphanedChunksInStorage {
		_, err := db.GetChunkByHash(ctx, hash)
		if err != nil {
			if database.IsNotFoundError(err) {
				results.orphanedChunksInStorage = append(results.orphanedChunksInStorage, hash)
			} else {
				return nil, fmt.Errorf("re-verify orphaned chunk in storage (%s): %w", hash, err)
			}
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
		dataRows = append(dataRows,
			fsckRow{"Orphaned chunks (DB only):", len(r.orphanedChunksInDB)},
			fsckRow{"NAR files w/ chunk issues:", len(r.narFilesWithChunkIssues)},
			fsckRow{"Orphaned chunk files:", len(r.orphanedChunksInStorage)},
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
		row("Orphaned chunk files:", len(r.orphanedChunksInStorage))
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

// repairFsckIssues applies fixes for each category of issue, re-verifying each item before acting.
func repairFsckIssues(
	ctx context.Context,
	db database.Querier,
	narStore storage.NarStore,
	chunkStore chunk.Store,
	results *fsckResults,
) error {
	logger := zerolog.Ctx(ctx)

	// a. Delete narinfos without nar_files
	for _, ni := range results.narinfosWithoutNarFiles {
		// Re-verify before deleting
		_, err := db.GetNarFileByNarInfoID(ctx, ni.ID)
		if err == nil {
			// Now has a nar_file, skip
			continue
		}

		if !database.IsNotFoundError(err) {
			return fmt.Errorf("repair re-verify narinfo(%d): %w", ni.ID, err)
		}

		if _, err := db.DeleteNarInfoByID(ctx, ni.ID); err != nil {
			logger.Error().Err(err).Int64("narinfo_id", ni.ID).Msg("failed to delete narinfo without nar_file")
		} else {
			logger.Info().Int64("narinfo_id", ni.ID).Str("hash", ni.Hash).Msg("deleted narinfo without nar_file")
		}
	}

	// b. Delete orphaned nar_files in DB
	for _, nf := range results.orphanedNarFilesInDB {
		// Re-verify before deleting
		_, err := db.GetNarInfoURLByNarFileHash(ctx, database.GetNarInfoURLByNarFileHashParams{
			Hash:        nf.Hash,
			Compression: nf.Compression,
			Query:       nf.Query,
		})
		if err == nil {
			// Now has a narinfo link, skip
			continue
		}

		if !errors.Is(err, sql.ErrNoRows) && !database.IsNotFoundError(err) {
			return fmt.Errorf("repair re-verify nar_file(%d): %w", nf.ID, err)
		}

		if _, err := db.DeleteNarFileByHash(ctx, database.DeleteNarFileByHashParams{
			Hash:        nf.Hash,
			Compression: nf.Compression,
			Query:       nf.Query,
		}); err != nil {
			logger.Error().Err(err).Int64("nar_file_id", nf.ID).Msg("failed to delete orphaned nar_file")
		} else {
			logger.Info().Int64("nar_file_id", nf.ID).Str("hash", nf.Hash).Msg("deleted orphaned nar_file from DB")
		}
	}

	// c. Delete nar_file DB records missing from storage.
	// Snapshot which narinfos are already orphaned before our deletions so we can
	// distinguish pre-existing orphans (handled in section a) from narinfos that
	// become orphaned as a cascade of removing the missing nar_file record.
	existingOrphans, err := db.GetNarInfosWithoutNarFiles(ctx)
	if err != nil {
		return fmt.Errorf("repair pre-check GetNarInfosWithoutNarFiles: %w", err)
	}

	existingOrphanIDs := make(map[int64]struct{}, len(existingOrphans))
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

		if _, err := db.DeleteNarFileByHash(ctx, database.DeleteNarFileByHashParams{
			Hash:        nf.Hash,
			Compression: nf.Compression,
			Query:       nf.Query,
		}); err != nil {
			logger.Error().Err(err).Int64("nar_file_id", nf.ID).Msg("failed to delete nar_file missing from storage")
		} else {
			logger.Info().
				Int64("nar_file_id", nf.ID).
				Str("hash", nf.Hash).
				Msg("deleted nar_file DB record (missing from storage)")
		}
	}

	// Delete narinfos that became orphaned as a result of the nar_file deletions above.
	// These would otherwise only be caught on a second fsck run.
	newOrphans, err := db.GetNarInfosWithoutNarFiles(ctx)
	if err != nil {
		return fmt.Errorf("repair post-check GetNarInfosWithoutNarFiles: %w", err)
	}

	for _, ni := range newOrphans {
		if _, alreadyOrphaned := existingOrphanIDs[ni.ID]; alreadyOrphaned {
			// Pre-existing orphan; handled in section a.
			continue
		}

		if _, err := db.DeleteNarInfoByID(ctx, ni.ID); err != nil {
			logger.Error().Err(err).
				Int64("narinfo_id", ni.ID).
				Msg("failed to delete narinfo orphaned by missing nar_file")
		} else {
			logger.Info().
				Int64("narinfo_id", ni.ID).
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
		_, err := db.GetNarFileByHashAndCompressionAndQuery(ctx, database.GetNarFileByHashAndCompressionAndQueryParams{
			Hash:        narURL.Hash,
			Compression: narURL.Compression.String(),
			Query:       narURL.Query.Encode(),
		})
		if err == nil {
			// Now in DB, skip
			continue
		}

		if !database.IsNotFoundError(err) {
			return fmt.Errorf("repair re-verify orphaned NAR (%s): %w", narURL, err)
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
	recheckChunks, err := db.GetOrphanedChunks(ctx)
	if err != nil {
		return fmt.Errorf("repair re-verify GetOrphanedChunks: %w", err)
	}

	recheckMap := make(map[int64]struct{}, len(recheckChunks))
	for _, rc := range recheckChunks {
		recheckMap[rc.ID] = struct{}{}
	}

	for _, c := range results.orphanedChunksInDB {
		if _, ok := recheckMap[c.ID]; !ok {
			continue
		}

		if err := db.DeleteChunkByID(ctx, c.ID); err != nil {
			logger.Error().Err(err).Int64("chunk_id", c.ID).Msg("failed to delete orphaned chunk from DB")
		} else {
			logger.Info().Int64("chunk_id", c.ID).Str("hash", c.Hash).Msg("deleted orphaned chunk from DB")
		}
	}

	// f. Delete nar_files with chunk issues (broken CDC nar_files).
	if chunkStore != nil {
		if err := repairBrokenCDCNarFiles(ctx, db, chunkStore, results.narFilesWithChunkIssues, logger); err != nil {
			return err
		}
	}

	// g. Delete orphaned chunk files from storage
	if chunkStore != nil {
		for _, hash := range results.orphanedChunksInStorage {
			// Re-verify before deleting
			_, err := db.GetChunkByHash(ctx, hash)
			if err == nil {
				// Now in DB, skip
				continue
			}

			if !database.IsNotFoundError(err) {
				return fmt.Errorf("repair re-verify orphaned chunk (%s): %w", hash, err)
			}

			if err := chunkStore.DeleteChunk(ctx, hash); err != nil {
				logger.Error().Err(err).Str("hash", hash).Msg("failed to delete orphaned chunk from storage")
			} else {
				logger.Info().Str("hash", hash).Msg("deleted orphaned chunk from storage")
			}
		}
	}

	return nil
}

// repairBrokenCDCNarFiles deletes broken CDC nar_files, their orphaned narinfos, and orphaned chunks.
func repairBrokenCDCNarFiles(
	ctx context.Context,
	db database.Querier,
	cs chunk.Store,
	narFilesWithChunkIssues []database.NarFile,
	logger *zerolog.Logger,
) error {
	// Snapshot pre-existing narinfo orphans so we only sweep newly orphaned ones below.
	cdcPreExistingOrphans, err := db.GetNarInfosWithoutNarFiles(ctx)
	if err != nil {
		return fmt.Errorf("repair CDC pre-check GetNarInfosWithoutNarFiles: %w", err)
	}

	cdcPreExistingOrphanIDs := make(map[int64]struct{}, len(cdcPreExistingOrphans))
	for _, ni := range cdcPreExistingOrphans {
		cdcPreExistingOrphanIDs[ni.ID] = struct{}{}
	}

	for _, nf := range narFilesWithChunkIssues {
		broken, err := isNarFileChunkBroken(ctx, db, cs, nf)
		if err != nil {
			return fmt.Errorf("repair re-verify narFilesWithChunkIssues(%d): %w", nf.ID, err)
		}

		if !broken {
			continue
		}

		if _, err := db.DeleteNarFileByHash(ctx, database.DeleteNarFileByHashParams{
			Hash:        nf.Hash,
			Compression: nf.Compression,
			Query:       nf.Query,
		}); err != nil {
			logger.Error().Err(err).Int64("nar_file_id", nf.ID).Msg("failed to delete broken CDC nar_file")
		} else {
			logger.Info().Int64("nar_file_id", nf.ID).Str("hash", nf.Hash).Msg("deleted broken CDC nar_file")
		}
	}

	// Delete narinfos orphaned by the CDC nar_file deletions above.
	cdcNewOrphans, err := db.GetNarInfosWithoutNarFiles(ctx)
	if err != nil {
		return fmt.Errorf("repair CDC post-check GetNarInfosWithoutNarFiles: %w", err)
	}

	for _, ni := range cdcNewOrphans {
		if _, alreadyOrphaned := cdcPreExistingOrphanIDs[ni.ID]; alreadyOrphaned {
			continue
		}

		if _, delErr := db.DeleteNarInfoByID(ctx, ni.ID); delErr != nil {
			logger.Error().Err(delErr).Int64("narinfo_id", ni.ID).
				Msg("failed to delete narinfo orphaned by broken CDC nar_file")
		} else {
			logger.Info().Int64("narinfo_id", ni.ID).Str("hash", ni.Hash).
				Msg("deleted narinfo orphaned by broken CDC nar_file")
		}
	}

	// Clean up newly-orphaned chunks after nar_file deletions.
	orphanedChunks, err := db.GetOrphanedChunks(ctx)
	if err != nil {
		return fmt.Errorf("repair post-CDC GetOrphanedChunks: %w", err)
	}

	for _, c := range orphanedChunks {
		if err := cs.DeleteChunk(ctx, c.Hash); err != nil {
			logger.Error().Err(err).Str("hash", c.Hash).Msg("failed to delete orphaned chunk from storage after CDC repair")
		} else {
			logger.Info().Str("hash", c.Hash).Msg("deleted orphaned chunk from storage after CDC repair")
		}

		if err := db.DeleteChunkByID(ctx, c.ID); err != nil {
			logger.Error().Err(err).Int64("chunk_id", c.ID).Msg("failed to delete orphaned chunk DB record after CDC repair")
		} else {
			logger.Info().Int64("chunk_id", c.ID).Str("hash", c.Hash).Msg("deleted orphaned chunk DB record after CDC repair")
		}
	}

	return nil
}

// isNarFileChunkBroken returns true if the nar_file's chunks are incomplete or missing from storage.
func isNarFileChunkBroken(ctx context.Context, db database.Querier, cs chunk.Store, nf database.NarFile) (bool, error) {
	chunks, err := db.GetChunksByNarFileID(ctx, nf.ID)
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
	db database.Querier,
	allNarFiles []database.GetAllNarFilesRow,
	cs chunk.Store,
	checked *atomic.Int64,
	verifiedSince time.Duration,
) ([]database.NarFile, error) {
	if cs == nil {
		return nil, nil
	}

	var broken []database.NarFile

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

		narFile := database.NarFile{
			ID:                nf.ID,
			Hash:              nf.Hash,
			Compression:       nf.Compression,
			Query:             nf.Query,
			FileSize:          nf.FileSize,
			TotalChunks:       nf.TotalChunks,
			ChunkingStartedAt: nf.ChunkingStartedAt,
			CreatedAt:         nf.CreatedAt,
			UpdatedAt:         nf.UpdatedAt,
			LastAccessedAt:    nf.LastAccessedAt,
		}

		isBroken, err := isNarFileChunkBroken(ctx, db, cs, narFile)
		if err != nil {
			return nil, err
		}

		if isBroken {
			broken = append(broken, narFile)
		} else {
			// Verified, update verified_at
			if err := db.UpdateNarFileVerifiedAt(ctx, nf.ID); err != nil {
				zerolog.Ctx(ctx).Warn().Err(err).Int64("nar_file_id", nf.ID).Msg("failed to update verified_at")
			}
		}
	}

	return broken, nil
}

// collectOrphanedChunksInStorage returns all chunk files in storage that have no DB record.
func collectOrphanedChunksInStorage(
	ctx context.Context,
	db database.Querier,
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
		_, dbErr := db.GetChunkByHash(ctx, hash)
		if dbErr != nil {
			if database.IsNotFoundError(dbErr) {
				orphaned = append(orphaned, hash)
			} else {
				return fmt.Errorf("DB lookup for chunk %s: %w", hash, dbErr)
			}
		}

		return nil
	}); err != nil {
		return nil, fmt.Errorf("WalkChunks: %w", err)
	}

	return orphaned, nil
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

func shouldCheckNar(nf database.GetAllNarFilesRow, verifiedSince time.Duration) bool {
	if verifiedSince <= 0 {
		return true
	}

	verifiedAt := nf.VerifiedAt

	if !verifiedAt.Valid {
		return true
	}

	return time.Since(verifiedAt.Time) > verifiedSince
}

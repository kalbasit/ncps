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
	"time"

	"github.com/rs/zerolog"
	"github.com/urfave/cli/v3"

	semconv "go.opentelemetry.io/otel/semconv/v1.39.0"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/otel"
	"github.com/kalbasit/ncps/pkg/storage"
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

	// chunksMissingInStorage: chunks in DB whose physical file is absent.
	chunksMissingInStorage []database.Chunk

	// orphanedChunksInStorage: chunk files in storage with no DB record.
	orphanedChunksInStorage []string
}

func (r *fsckResults) totalIssues() int {
	return len(r.narinfosWithoutNarFiles) +
		len(r.orphanedNarFilesInDB) +
		len(r.narFilesMissingInStorage) +
		len(r.orphanedNarFilesInStorage) +
		len(r.orphanedChunksInDB) +
		len(r.chunksMissingInStorage) +
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

// hasChunker is implemented by chunk stores that support HasChunk.
type hasChunker interface {
	HasChunk(ctx context.Context, hash string) (bool, error)
}

// chunkDeleter is implemented by chunk stores that support DeleteChunk.
type chunkDeleter interface {
	DeleteChunk(ctx context.Context, hash string) error
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

			cdcConfig, dbErr := db.GetConfigByKey(ctx, "cdc.enabled")
			if dbErr == nil && cdcConfig.Value == configValueTrue {
				cdcMode = true
			}

			var chunkStore ChunkWalker

			if cdcMode {
				cs, csErr := getChunkStorageBackend(ctx, cmd, locker)
				if csErr != nil {
					logger.Error().Err(csErr).Msg("error creating chunk storage backend")

					return csErr
				}

				chunkStore, _ = cs.(ChunkWalker)
			}

			// 6. Phase 1: Collect suspects
			logger.Info().Msg("phase 1: collecting suspects")

			suspects, err := collectFsckSuspects(ctx, db, narStore, chunkStore, cdcMode)
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
				fmt.Println("All checks passed.")

				return nil
			}

			// 9. Decide what to do
			if dryRun {
				fmt.Println("\nRun with --repair to fix, or omit --dry-run to be prompted.")

				return ErrFsckIssuesFound
			}

			if !repair {
				fmt.Print("\nRepair all issues? [y/N]: ")

				scanner := bufio.NewScanner(os.Stdin)
				if scanner.Scan() {
					answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
					if answer != "y" && answer != "yes" {
						fmt.Println("Aborted.")

						return ErrFsckIssuesFound
					}
				} else {
					fmt.Println("Aborted (no input).")

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
	chunkStore ChunkWalker,
	cdcMode bool,
) (*fsckResults, error) {
	results := &fsckResults{cdcMode: cdcMode}

	// a. Narinfos without nar_files
	narinfosWithoutNarFiles, err := db.GetNarInfosWithoutNarFiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("GetNarInfosWithoutNarFiles: %w", err)
	}

	results.narinfosWithoutNarFiles = narinfosWithoutNarFiles

	// b. Orphaned nar_files in DB
	orphanedNarFiles, err := db.GetOrphanedNarFiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("GetOrphanedNarFiles: %w", err)
	}

	results.orphanedNarFilesInDB = orphanedNarFiles

	// c. Nar_files missing from storage
	allNarFiles, err := db.GetAllNarFiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("GetAllNarFiles: %w", err)
	}

	for _, nf := range allNarFiles {
		narURL, err := narFileRowToURL(nf.Hash, nf.Compression, nf.Query)
		if err != nil {
			return nil, fmt.Errorf("narFileRowToURL for nar_file %d: %w", nf.ID, err)
		}

		if !narStore.HasNar(ctx, narURL) {
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
		}
	}

	// d. Orphaned NAR files in storage
	narWalker, ok := narStore.(NarWalker)
	if ok {
		if err := narWalker.WalkNars(ctx, func(narURL nar.URL) error {
			_, dbErr := db.GetNarFileByHashAndCompressionAndQuery(ctx, database.GetNarFileByHashAndCompressionAndQueryParams{
				Hash:        narURL.Hash,
				Compression: narURL.Compression.String(),
				Query:       narURL.Query.Encode(),
			})
			if dbErr != nil {
				if database.IsNotFoundError(dbErr) {
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

	if !cdcMode {
		return results, nil
	}

	// e. Orphaned chunks in DB
	orphanedChunks, err := db.GetOrphanedChunks(ctx)
	if err != nil {
		return nil, fmt.Errorf("GetOrphanedChunks: %w", err)
	}

	results.orphanedChunksInDB = orphanedChunks

	// f. Chunks missing from storage
	missing, err := collectChunksMissingFromStorage(ctx, db, chunkStore)
	if err != nil {
		return nil, err
	}

	results.chunksMissingInStorage = missing

	// g. Orphaned chunk files in storage
	orphaned, err := collectOrphanedChunksInStorage(ctx, db, chunkStore)
	if err != nil {
		return nil, err
	}

	results.orphanedChunksInStorage = orphaned

	return results, nil
}

// reVerifyFsckSuspects re-checks each suspected issue to handle in-flight operations.
// Items that are no longer issues are silently removed from the results.
func reVerifyFsckSuspects(
	ctx context.Context,
	db database.Querier,
	narStore storage.NarStore,
	chunkStore ChunkWalker,
	suspects *fsckResults,
) (*fsckResults, error) {
	results := &fsckResults{cdcMode: suspects.cdcMode}

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
	}

	// Re-verify: orphaned NAR files in storage
	for _, narURL := range suspects.orphanedNarFilesInStorage {
		_, err := db.GetNarFileByHashAndCompressionAndQuery(ctx, database.GetNarFileByHashAndCompressionAndQueryParams{
			Hash:        narURL.Hash,
			Compression: narURL.Compression.String(),
			Query:       narURL.Query.Encode(),
		})
		if err != nil {
			if database.IsNotFoundError(err) {
				results.orphanedNarFilesInStorage = append(results.orphanedNarFilesInStorage, narURL)
			} else {
				return nil, fmt.Errorf("re-verify orphaned NAR in storage (%s): %w", narURL, err)
			}
		}
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
	}

	// Re-verify: chunks missing from storage
	hc, _ := chunkStore.(hasChunker)

	for _, c := range suspects.chunksMissingInStorage {
		if hc == nil {
			break
		}

		exists, err := hc.HasChunk(ctx, c.Hash)
		if err != nil {
			return nil, fmt.Errorf("re-verify HasChunk(%s): %w", c.Hash, err)
		}

		if !exists {
			results.chunksMissingInStorage = append(results.chunksMissingInStorage, c)
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
	}

	return results, nil
}

// printFsckSummary prints the fsck summary report.
func printFsckSummary(r *fsckResults) {
	fmt.Println()
	fmt.Println("ncps fsck summary")
	fmt.Println("=================")
	fmt.Printf("Narinfos without nar_files:         %d\n", len(r.narinfosWithoutNarFiles))
	fmt.Printf("Orphaned nar_files (DB only):       %d\n", len(r.orphanedNarFilesInDB))
	fmt.Printf("Nar_files missing from storage:     %d\n", len(r.narFilesMissingInStorage))
	fmt.Printf("Orphaned NAR files in storage:      %d\n", len(r.orphanedNarFilesInStorage))

	if r.cdcMode {
		fmt.Printf("[CDC] Orphaned chunks (DB only):    %d\n", len(r.orphanedChunksInDB))
		fmt.Printf("[CDC] Chunks missing from storage:  %d\n", len(r.chunksMissingInStorage))
		fmt.Printf("[CDC] Orphaned chunk files:         %d\n", len(r.orphanedChunksInStorage))
	}

	fmt.Println("-----------------")
	fmt.Printf("Total issues:                       %d\n", r.totalIssues())
}

// repairFsckIssues applies fixes for each category of issue, re-verifying each item before acting.
func repairFsckIssues(
	ctx context.Context,
	db database.Querier,
	narStore storage.NarStore,
	chunkStore ChunkWalker,
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

	// c. Delete nar_file DB records missing from storage
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

	// f. Delete chunk DB records missing from storage
	if hc, ok := chunkStore.(hasChunker); ok {
		for _, c := range results.chunksMissingInStorage {
			exists, err := hc.HasChunk(ctx, c.Hash)
			if err != nil {
				return fmt.Errorf("repair re-verify HasChunk(%s): %w", c.Hash, err)
			}

			if exists {
				// File appeared, skip
				continue
			}

			if err := db.DeleteChunkByID(ctx, c.ID); err != nil {
				logger.Error().Err(err).Int64("chunk_id", c.ID).Msg("failed to delete chunk DB record missing from storage")
			} else {
				logger.Info().
					Int64("chunk_id", c.ID).
					Str("hash", c.Hash).
					Msg("deleted chunk DB record (missing from storage)")
			}
		}
	}

	// g. Delete orphaned chunk files from storage
	if cd, ok := chunkStore.(chunkDeleter); ok {
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

			if err := cd.DeleteChunk(ctx, hash); err != nil {
				logger.Error().Err(err).Str("hash", hash).Msg("failed to delete orphaned chunk from storage")
			} else {
				logger.Info().Str("hash", hash).Msg("deleted orphaned chunk from storage")
			}
		}
	}

	return nil
}

// collectChunksMissingFromStorage returns all DB chunks that are absent from storage.
func collectChunksMissingFromStorage(
	ctx context.Context,
	db database.Querier,
	chunkStore ChunkWalker,
) ([]database.Chunk, error) {
	if chunkStore == nil {
		return nil, nil
	}

	hc, ok := chunkStore.(hasChunker)
	if !ok {
		return nil, nil
	}

	allChunks, err := db.GetAllChunks(ctx)
	if err != nil {
		return nil, fmt.Errorf("GetAllChunks: %w", err)
	}

	var missing []database.Chunk

	for _, c := range allChunks {
		exists, checkErr := hc.HasChunk(ctx, c.Hash)
		if checkErr != nil {
			return nil, fmt.Errorf("HasChunk(%s): %w", c.Hash, checkErr)
		}

		if !exists {
			missing = append(missing, c)
		}
	}

	return missing, nil
}

// collectOrphanedChunksInStorage returns all chunk files in storage that have no DB record.
func collectOrphanedChunksInStorage(
	ctx context.Context,
	db database.Querier,
	chunkStore ChunkWalker,
) ([]string, error) {
	if chunkStore == nil {
		return nil, nil
	}

	var orphaned []string

	if err := chunkStore.WalkChunks(ctx, func(hash string) error {
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

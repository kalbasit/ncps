package ncps

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"github.com/urfave/cli/v3"
	"golang.org/x/sync/errgroup"

	semconv "go.opentelemetry.io/otel/semconv/v1.39.0"

	"github.com/kalbasit/ncps/pkg/cache"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/otel"
	"github.com/kalbasit/ncps/pkg/storage"
)

// ErrUnmigratedNarinfosFound is returned when there are unmigrated narinfos.
var ErrUnmigratedNarinfosFound = errors.New("unmigrated narinfos found")

func migrateNarToChunksCommand(
	flagSources flagSourcesFn,
	registerShutdown registerShutdownFn,
) *cli.Command {
	return &cli.Command{
		Name:  "migrate-nar-to-chunks",
		Usage: "Migrate NAR files from storage to content-defined chunks",
		Description: `Migrates NAR files from traditional storage (filesystem/S3) to content-defined chunks.
This requires CDC to be enabled and a chunk store configured.
Once a NAR is successfully migrated to chunks and verified, it is deleted from the original storage.`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "dry-run",
				Usage: "Simulate migration without writing to chunk store or deleting from storage",
			},

			&cli.StringFlag{
				Name:    "cache-temp-path",
				Usage:   "The path to the temporary directory that is used by the cache to download NAR files",
				Sources: flagSources("cache.temp-path", "CACHE_TEMP_PATH"),
				Value:   os.TempDir(),
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

			// Lock Backend Flags (optional - for coordination with running instances)
			&cli.StringSliceFlag{
				Name:    "cache-redis-addrs",
				Usage:   "Redis server addresses for distributed locking (enables coordination with running ncps instances)",
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
				Name: "cache-lock-backend",
				Usage: "Lock backend to use: 'local' (single instance), 'redis' (distributed), " +
					"or 'postgres' (distributed, requires PostgreSQL)",
				Sources: flagSources("cache.lock.backend", "CACHE_LOCK_BACKEND"),
				Value:   "local",
			},
			&cli.StringFlag{
				Name:    "cache-lock-redis-key-prefix",
				Usage:   "Prefix for all Redis lock keys (only used when Redis is configured)",
				Sources: flagSources("cache.lock.redis.key-prefix", "CACHE_LOCK_REDIS_KEY_PREFIX"),
				Value:   "ncps:lock:",
			},
			&cli.StringFlag{
				Name:    "cache-lock-postgres-key-prefix",
				Usage:   "Prefix for all PostgreSQL advisory lock keys (only used when PostgreSQL is configured as lock backend)",
				Sources: flagSources("cache.lock.postgres.key-prefix", "CACHE_LOCK_POSTGRES_KEY_PREFIX"),
				Value:   "ncps:lock:",
			},
			&cli.DurationFlag{
				Name:    "cache-lock-download-ttl",
				Usage:   "TTL for download locks (per-hash locks)",
				Sources: flagSources("cache.lock.download-lock-ttl", "CACHE_LOCK_DOWNLOAD_TTL"),
				Value:   5 * time.Minute,
			},
			&cli.DurationFlag{
				Name:    "cache-lock-lru-ttl",
				Usage:   "TTL for LRU lock (global exclusive lock)",
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
				Usage:   "Maximum retry delay for distributed locks (exponential backoff caps at this)",
				Sources: flagSources("cache.lock.retry.max-delay", "CACHE_LOCK_RETRY_MAX_DELAY"),
				Value:   2 * time.Second,
			},
			&cli.BoolFlag{
				Name:    "cache-lock-retry-jitter",
				Usage:   "Enable jitter in retry delays to prevent thundering herd",
				Sources: flagSources("cache.lock.retry.jitter", "CACHE_LOCK_RETRY_JITTER"),
				Value:   true,
			},
			&cli.BoolFlag{
				Name:    "cache-lock-allow-degraded-mode",
				Usage:   "Allow falling back to local locks if Redis is unavailable (WARNING: breaks HA guarantees)",
				Sources: flagSources("cache.lock.allow-degraded-mode", "CACHE_LOCK_ALLOW_DEGRADED_MODE"),
			},
			&cli.IntFlag{
				Name:    "cache-redis-pool-size",
				Usage:   "Redis connection pool size",
				Sources: flagSources("cache.redis.pool-size", "CACHE_REDIS_POOL_SIZE"),
				Value:   10,
			},

			// CDC Flags
			&cli.BoolFlag{
				Name:    "cache-cdc-enabled",
				Usage:   "Enable Content-Defined Chunking (CDC) for NAR storage",
				Sources: flagSources("cache.cdc.enabled", "CACHE_CDC_ENABLED"),
				Value:   true,
			},
			&cli.Uint32Flag{
				Name:    "cache-cdc-min",
				Usage:   "Minimum chunk size for CDC in bytes",
				Sources: flagSources("cache.cdc.min", "CACHE_CDC_MIN"),
				Value:   64 * 1024, // 64KB
			},
			&cli.Uint32Flag{
				Name:    "cache-cdc-avg",
				Usage:   "Average chunk size for CDC in bytes",
				Sources: flagSources("cache.cdc.avg", "CACHE_CDC_AVG"),
				Value:   256 * 1024, // 256KB
			},
			&cli.Uint32Flag{
				Name:    "cache-cdc-max",
				Usage:   "Maximum chunk size for CDC in bytes",
				Sources: flagSources("cache.cdc.max", "CACHE_CDC_MAX"),
				Value:   1024 * 1024, // 1MB
			},

			&cli.IntFlag{
				Name:    "concurrency",
				Usage:   "Number of concurrent migration workers",
				Value:   10,
				Sources: flagSources("concurrency", "CONCURRENCY"),
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			logger := zerolog.Ctx(ctx).With().Str("cmd", "migrate-nar-to-chunks").Logger()
			ctx = logger.WithContext(ctx)

			dryRun := cmd.Bool("dry-run")

			// 1. Setup Database
			db, err := createDatabaseQuerier(cmd)
			if err != nil {
				return fmt.Errorf("error creating database querier: %w", err)
			}

			// 2. Setup Lockers
			locker, rwLocker, err := getLockers(ctx, cmd, db)
			if err != nil {
				return fmt.Errorf("error creating lockers: %w", err)
			}

			// 3. Setup OTel
			extraResourceAttrs, err := detectExtraResourceAttrs(ctx, cmd, db, rwLocker)
			if err != nil {
				return fmt.Errorf("error detecting extra resource attributes: %w", err)
			}

			otelResource, err := otel.NewResource(
				ctx,
				cmd.Root().Name,
				Version,
				semconv.SchemaURL,
				extraResourceAttrs...,
			)
			if err != nil {
				return fmt.Errorf("error creating otel resource: %w", err)
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

			// 4. Setup Cache (needed for migration logic)
			// We recreate the cache to reuse its MigrateNarToChunks logic
			c, err := createCache(ctx, cmd, db, locker, rwLocker, nil)
			if err != nil {
				return fmt.Errorf("error creating cache: %w", err)
			}
			defer c.Close()

			// 5. Setup Storage
			_, narInfoStore, narStore, err := getStorageBackend(ctx, cmd)
			if err != nil {
				return fmt.Errorf("error creating storage backend: %w", err)
			}

			// 6. Safety Check: Ensure all narinfos are migrated
			var unmigratedHashesCount int32

			err = narInfoStore.WalkNarInfos(ctx, func(hash string) error {
				ni, err := db.GetNarInfoByHash(ctx, hash)
				if err != nil {
					if database.IsNotFoundError(err) {
						atomic.AddInt32(&unmigratedHashesCount, 1)

						return nil
					}

					return fmt.Errorf("failed to fetch narinfo from database: %w", err)
				}

				if !ni.URL.Valid {
					atomic.AddInt32(&unmigratedHashesCount, 1)
				}

				return nil
			})
			if err != nil {
				return fmt.Errorf("failed to check for unmigrated narinfos: %w", err)
			}

			if unmigratedHashesCount > 0 {
				return fmt.Errorf("%w (%d); please run 'migrate-narinfo' first", ErrUnmigratedNarinfosFound, unmigratedHashesCount)
			}

			// 6. Migrate
			logger.Info().Msg("starting migration of NARs to chunks")

			startTime := time.Now()

			var (
				totalFound     int32
				totalProcessed int32
				totalSucceeded int32
				totalFailed    int32
				totalSkipped   int32
			)

			g, ctx := errgroup.WithContext(ctx)
			g.SetLimit(cmd.Int("concurrency"))

			// Progress reporter
			progressTicker := time.NewTicker(5 * time.Second)
			defer progressTicker.Stop()

			progressDone := make(chan struct{})
			defer close(progressDone)

			go func() {
				for {
					select {
					case <-progressTicker.C:
						elapsed := time.Since(startTime)
						found := atomic.LoadInt32(&totalFound)
						processed := atomic.LoadInt32(&totalProcessed)
						succeeded := atomic.LoadInt32(&totalSucceeded)
						failed := atomic.LoadInt32(&totalFailed)
						skipped := atomic.LoadInt32(&totalSkipped)

						var rate float64
						if durationInSeconds := elapsed.Seconds(); durationInSeconds > 0 {
							rate = float64(processed) / durationInSeconds
						}

						logger.Info().
							Int32("found", found).
							Int32("processed", processed).
							Int32("succeeded", succeeded).
							Int32("failed", failed).
							Int32("skipped", skipped).
							Str("elapsed", elapsed.Round(time.Second).String()).
							Float64("rate", rate).
							Msg("migration progress")
					case <-progressDone:
						return
					}
				}
			}()

			hashes, err := db.GetNarInfoHashesToChunk(ctx)
			if err != nil {
				return fmt.Errorf("failed to fetch candidate hashes from database: %w", err)
			}

			for _, row := range hashes {
				hash := row.Hash

				atomic.AddInt32(&totalFound, 1)

				g.Go(func() error {
					log := logger.With().Str("narinfo_hash", hash).Logger()

					if !row.URL.Valid {
						log.Error().Msg("narinfo record has no URL")
						atomic.AddInt32(&totalFailed, 1)
						RecordMigrationObject(ctx, MigrationTypeNarToChunks, MigrationOperationMigrate, MigrationResultFailure)

						return nil
					}

					narURL, err := nar.ParseURL(row.URL.String)
					if err != nil {
						log.Error().Err(err).Str("url", row.URL.String).Msg("failed to parse nar URL")
						atomic.AddInt32(&totalFailed, 1)
						RecordMigrationObject(ctx, MigrationTypeNarToChunks, MigrationOperationMigrate, MigrationResultFailure)

						return nil
					}

					// double check if already chunked (might have been migrated by background task)
					hasChunks, err := c.HasNarInChunks(ctx, narURL)
					if err != nil {
						log.Error().Err(err).Msg("failed to check if nar is already in chunks")
						atomic.AddInt32(&totalFailed, 1)
						RecordMigrationObject(ctx, MigrationTypeNarToChunks, MigrationOperationMigrate, MigrationResultFailure)

						return nil
					}

					if hasChunks {
						log.Debug().Msg("nar already in chunks, skipping")
						atomic.AddInt32(&totalSkipped, 1)
						RecordMigrationObject(ctx, MigrationTypeNarToChunks, MigrationOperationMigrate, MigrationResultSkipped)

						// Try to delete original if it still exists (cleanup)
						if !dryRun {
							if err := narStore.DeleteNar(ctx, narURL); err != nil && !errors.Is(err, storage.ErrNotFound) {
								log.Warn().Err(err).Msg("failed to delete original nar (already in chunks)")
							}
						}

						return nil
					}

					atomic.AddInt32(&totalProcessed, 1)

					if dryRun {
						log.Info().Msg("[DRY-RUN] would migrate nar to chunks and delete original")
						atomic.AddInt32(&totalSucceeded, 1)
						RecordMigrationObject(ctx, MigrationTypeNarToChunks, MigrationOperationMigrate, MigrationResultSuccess)

						return nil
					}

					opStartTime := time.Now()
					err = c.MigrateNarToChunks(ctx, narURL)

					RecordMigrationDuration(
						ctx,
						MigrationTypeNarToChunks,
						MigrationOperationMigrate,
						time.Since(opStartTime).Seconds(),
					)

					if err != nil && !errors.Is(err, cache.ErrNarAlreadyChunked) {
						log.Error().Err(err).Msg("failed to migrate nar to chunks")
						atomic.AddInt32(&totalFailed, 1)
						RecordMigrationObject(ctx, MigrationTypeNarToChunks, MigrationOperationMigrate, MigrationResultFailure)

						return nil
					}

					// 2. Delete original NAR if migration was successful
					if err := narStore.DeleteNar(ctx, narURL); err != nil && !errors.Is(err, storage.ErrNotFound) {
						log.Warn().Err(err).Msg("failed to delete original nar after migration")
					}

					if errors.Is(err, cache.ErrNarAlreadyChunked) {
						atomic.AddInt32(&totalSkipped, 1)
						RecordMigrationObject(ctx, MigrationTypeNarToChunks, MigrationOperationMigrate, MigrationResultSkipped)
					} else {
						atomic.AddInt32(&totalSucceeded, 1)
						RecordMigrationObject(ctx, MigrationTypeNarToChunks, MigrationOperationMigrate, MigrationResultSuccess)
					}

					return nil
				})
			}

			if err := g.Wait(); err != nil {
				return err
			}

			// Final summary
			duration := time.Since(startTime)
			logger.Info().
				Int32("found", atomic.LoadInt32(&totalFound)).
				Int32("processed", atomic.LoadInt32(&totalProcessed)).
				Int32("succeeded", atomic.LoadInt32(&totalSucceeded)).
				Int32("failed", atomic.LoadInt32(&totalFailed)).
				Int32("skipped", atomic.LoadInt32(&totalSkipped)).
				Str("duration", duration.Round(time.Millisecond).String()).
				Msg("migration completed")

			RecordMigrationBatchSize(ctx, MigrationTypeNarToChunks, int64(atomic.LoadInt32(&totalFound)))

			return nil
		},
	}
}

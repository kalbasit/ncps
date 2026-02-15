package ncps

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"github.com/urfave/cli/v3"
	"golang.org/x/sync/errgroup"

	semconv "go.opentelemetry.io/otel/semconv/v1.39.0"

	"github.com/kalbasit/ncps/pkg/cache"
	"github.com/kalbasit/ncps/pkg/otel"
)

// ErrStorageIterationNotSupported is returned when the storage backend does not support iteration.
var ErrStorageIterationNotSupported = errors.New("storage backend does not support iteration")

// ErrMigrationFailed is returned when one or more NarInfos fail to migrate.
var ErrMigrationFailed = errors.New("narinfos failed to migrate")

type NarInfoWalker interface {
	WalkNarInfos(ctx context.Context, fn func(hash string) error) error
}

func migrateNarInfoCommand(
	flagSources flagSourcesFn,
	registerShutdown registerShutdownFn,
) *cli.Command {
	return &cli.Command{
		Name:  "migrate-narinfo",
		Usage: "Migrate NarInfo files from storage to the database",
		Description: `Migrates NarInfo metadata from storage (filesystem/S3) to the database.

This command uses distributed locking to coordinate with running ncps instances when a
Redis lock backend is configured. This allows safe migration while the cache is serving
requests. Without Redis, the command uses in-memory locking (no coordination with other instances).

For production deployments with multiple ncps instances, configure --cache-redis-addrs
to enable safe concurrent migration.`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "dry-run",
				Usage: "Simulate migration without writing to DB or deleting from storage",
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

			&cli.IntFlag{
				Name:    "concurrency",
				Usage:   "Number of concurrent migration workers",
				Value:   10,
				Sources: flagSources("concurrency", "CONCURRENCY"),
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
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			logger := zerolog.Ctx(ctx).With().Str("cmd", "migrate-narinfo").Logger()
			ctx = logger.WithContext(ctx)

			dryRun := cmd.Bool("dry-run")

			// 1. Setup Database
			db, err := createDatabaseQuerier(cmd)
			if err != nil {
				logger.Error().Err(err).Msg("error creating database querier")

				return err
			}

			// 2. Setup Lockers
			locker, rwLocker, err := getLockers(ctx, cmd, db)
			if err != nil {
				logger.Error().Err(err).Msg("error creating the lockers")

				return err
			}

			// 3. Setup OTel
			extraResourceAttrs, err := detectExtraResourceAttrs(ctx, cmd, db, rwLocker)
			if err != nil {
				logger.
					Error().
					Err(err).
					Msg("error detecting extra resource attributes")

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
				logger.
					Error().
					Err(err).
					Msg("error creating a new otel resource")

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
			_, narInfoStore, _, err := getStorageBackend(ctx, cmd)
			if err != nil {
				logger.Error().Err(err).Msg("error creating storage backend")

				return err
			}

			// 5. Migrate
			logger.Info().Msg("starting migration")

			startTime := time.Now()

			unmigratedHashes, err := db.GetUnmigratedNarInfoHashes(ctx)
			if err != nil {
				return fmt.Errorf("failed to fetch unmigrated hashes from database: %w", err)
			}

			migratedHashes, err := db.GetMigratedNarInfoHashes(ctx)
			if err != nil {
				return fmt.Errorf("failed to fetch migrated hashes from database: %w", err)
			}

			totalToProcess := int64(len(unmigratedHashes) + len(migratedHashes))

			var (
				totalProcessed int32
				totalSucceeded int32
				totalFailed    int32
				totalSkipped   int32
			)

			g, ctx := errgroup.WithContext(ctx)
			g.SetLimit(cmd.Int("concurrency"))

			// Start progress reporter
			progressTicker := time.NewTicker(5 * time.Second)
			defer progressTicker.Stop()

			progressDone := make(chan struct{})
			defer close(progressDone)

			go func() {
				for {
					select {
					case <-progressTicker.C:
						elapsed := time.Since(startTime)
						processed := atomic.LoadInt32(&totalProcessed)
						succeeded := atomic.LoadInt32(&totalSucceeded)
						failed := atomic.LoadInt32(&totalFailed)
						skipped := atomic.LoadInt32(&totalSkipped)

						var rate float64
						if durationInSeconds := elapsed.Seconds(); durationInSeconds > 0 {
							rate = float64(processed) / durationInSeconds
						}

						var percent float64
						if totalToProcess > 0 {
							percent = float64(processed) / float64(totalToProcess) * 100
						}

						logger.Info().
							Int64("total", totalToProcess).
							Int32("processed", processed).
							Int32("succeeded", succeeded).
							Int32("failed", failed).
							Int32("skipped", skipped).
							Str("percent", fmt.Sprintf("%.2f%%", percent)).
							Str("elapsed", elapsed.Round(time.Second).String()).
							Float64("rate", rate).
							Msg("migration progress")
					case <-progressDone:
						return
					}
				}
			}()

			// Process unmigrated hashes
			for _, hash := range unmigratedHashes {
				g.Go(func() error {
					atomic.AddInt32(&totalProcessed, 1)

					log := logger.With().Str("hash", hash).Logger()
					log.Debug().Msg("processing unmigrated narinfo")

					ctxWithLog := log.WithContext(ctx)

					opStartTime := time.Now()

					defer func() {
						RecordMigrationDuration(
							ctxWithLog,
							MigrationTypeNarInfoToDB,
							MigrationOperationMigrate,
							time.Since(opStartTime).Seconds(),
						)
					}()

					// Fetch narinfo from storage
					ni, err := narInfoStore.GetNarInfo(ctxWithLog, hash)
					if err != nil {
						log.Error().Err(err).Msg("failed to get narinfo from store")
						atomic.AddInt32(&totalFailed, 1)
						RecordMigrationObject(ctxWithLog, MigrationTypeNarInfoToDB, MigrationOperationMigrate, MigrationResultFailure)

						return nil
					}

					if dryRun {
						log.Info().Msg("[DRY-RUN] would migrate and delete")
						atomic.AddInt32(&totalSucceeded, 1)
						RecordMigrationObject(ctxWithLog, MigrationTypeNarInfoToDB, MigrationOperationMigrate, MigrationResultSuccess)

						return nil
					}

					// Use the shared migration function from pkg/cache
					// Pass narInfoStore to enable deletion after migration
					if err := cache.MigrateNarInfo(ctxWithLog, locker, db, narInfoStore, hash, ni); err != nil {
						log.Error().Err(err).Msg("failed to migrate narinfo")
						atomic.AddInt32(&totalFailed, 1)
						RecordMigrationObject(ctxWithLog, MigrationTypeNarInfoToDB, MigrationOperationMigrate, MigrationResultFailure)

						return nil
					}

					atomic.AddInt32(&totalSucceeded, 1)
					RecordMigrationObject(ctxWithLog, MigrationTypeNarInfoToDB, MigrationOperationMigrate, MigrationResultSuccess)

					return nil
				})
			}

			// Process migrated hashes (only cleanup from storage)
			for _, hash := range migratedHashes {
				g.Go(func() error {
					atomic.AddInt32(&totalProcessed, 1)

					log := logger.With().Str("hash", hash).Logger()
					ctxWithLog := log.WithContext(ctx)
					log.Debug().Msg("narinfo already migrated, ensuring it's deleted from storage")

					if dryRun {
						log.Debug().Msg("[DRY-RUN] would ensure deleted from storage")
						atomic.AddInt32(&totalSkipped, 1)
						RecordMigrationObject(ctxWithLog, MigrationTypeNarInfoToDB, MigrationOperationDelete, MigrationResultSkipped)

						return nil
					}

					if err := narInfoStore.DeleteNarInfo(ctxWithLog, hash); err != nil {
						log.Debug().Err(err).Msg("failed to delete from store (already migrated)")
						// We don't count this as a failure of the migration itself, but more as a skip since it's already migrated
						atomic.AddInt32(&totalSkipped, 1)
						RecordMigrationObject(ctxWithLog, MigrationTypeNarInfoToDB, MigrationOperationDelete, MigrationResultSkipped)
					} else {
						atomic.AddInt32(&totalSkipped, 1)
						RecordMigrationObject(ctxWithLog, MigrationTypeNarInfoToDB, MigrationOperationDelete, MigrationResultSkipped)
					}

					return nil
				})
			}

			if err := g.Wait(); err != nil {
				return err
			}

			// Final summary
			duration := time.Since(startTime)
			processed := atomic.LoadInt32(&totalProcessed)
			succeeded := atomic.LoadInt32(&totalSucceeded)
			failed := atomic.LoadInt32(&totalFailed)
			skipped := atomic.LoadInt32(&totalSkipped)

			var rate float64
			if durationInSeconds := duration.Seconds(); durationInSeconds > 0 {
				rate = float64(processed) / durationInSeconds
			}

			// Record batch size metric
			RecordMigrationBatchSize(ctx, MigrationTypeNarInfoToDB, totalToProcess)

			logger.Info().
				Int64("total", totalToProcess).
				Int32("processed", processed).
				Int32("succeeded", succeeded).
				Int32("failed", failed).
				Int32("skipped", skipped).
				Str("duration", duration.Round(time.Millisecond).String()).
				Float64("rate", rate).
				Msg("migration completed")

			if failed > 0 {
				return fmt.Errorf("%d %w", failed, ErrMigrationFailed)
			}

			return nil
		},
	}
}

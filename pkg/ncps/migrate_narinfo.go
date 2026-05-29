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

	entnarinfo "github.com/kalbasit/ncps/ent/narinfo"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"

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
requests. Without Redis, the command uses in-memory locking (no coordination with other instances).`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  flagNameDryRun,
				Usage: "Simulate migration without writing to DB or deleting from storage",
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

			// Lock Backend Flags (optional - for coordination with running instances)
			&cli.StringSliceFlag{
				Name:    flagNameRedisAddrs,
				Usage:   "Redis server addresses for distributed locking (enables coordination with running ncps instances)",
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

			&cli.IntFlag{
				Name:    "concurrency",
				Usage:   "Number of concurrent migration workers",
				Value:   10,
				Sources: flagSources("concurrency", "CONCURRENCY"),
			},

			&cli.StringFlag{
				Name:    flagNameLockBackend,
				Usage:   flagUsageLockBackend,
				Sources: flagSources("cache.lock.backend", "CACHE_LOCK_BACKEND"),
				Value:   lockBackendLocal,
			},
			&cli.StringFlag{
				Name:    flagNameLockRedisKeyPrefix,
				Usage:   flagUsageLockRedisKeyPrefix,
				Sources: flagSources("cache.lock.redis.key-prefix", "CACHE_LOCK_REDIS_KEY_PREFIX"),
				Value:   flagDefaultLockRedisKeyPrefix,
			},
			&cli.DurationFlag{
				Name:    flagNameLockDownloadTTL,
				Usage:   flagUsageLockDownloadTTL,
				Sources: flagSources("cache.lock.download-lock-ttl", "CACHE_LOCK_DOWNLOAD_TTL"),
				Value:   5 * time.Minute,
			},
			&cli.DurationFlag{
				Name:    flagNameLockLRUTTL,
				Usage:   flagUsageLockLRUTTL,
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
				Usage:   flagUsageLockMaxDelay,
				Sources: flagSources("cache.lock.retry.max-delay", "CACHE_LOCK_RETRY_MAX_DELAY"),
				Value:   2 * time.Second,
			},
			&cli.BoolFlag{
				Name:    flagNameLockJitter,
				Usage:   flagUsageLockJitter,
				Sources: flagSources("cache.lock.retry.jitter", "CACHE_LOCK_RETRY_JITTER"),
				Value:   true,
			},
			&cli.BoolFlag{
				Name:    flagNameLockAllowDegraded,
				Usage:   flagUsageLockAllowDegraded,
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
			logger := zerolog.Ctx(ctx).With().Str("cmd", "migrate-narinfo").Logger()
			ctx = logger.WithContext(ctx)

			dryRun := cmd.Bool("dry-run")

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

			unmigratedHashes, err := dbClient.Ent().NarInfo.Query().
				Where(entnarinfo.URLIsNil()).
				Select(entnarinfo.FieldHash).
				Strings(ctx)
			if err != nil {
				return fmt.Errorf("failed to fetch unmigrated hashes from database: %w", err)
			}

			migratedHashes, err := dbClient.Ent().NarInfo.Query().
				Where(entnarinfo.URLNotNil()).
				Select(entnarinfo.FieldHash).
				Strings(ctx)
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
					if err := cache.MigrateNarInfo(ctxWithLog, locker, dbClient, narInfoStore, hash, ni); err != nil {
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

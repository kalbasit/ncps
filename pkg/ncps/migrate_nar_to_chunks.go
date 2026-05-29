package ncps

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"github.com/urfave/cli/v3"
	"golang.org/x/sync/errgroup"

	entnarfile "github.com/kalbasit/ncps/ent/narfile"
	entnarinfo "github.com/kalbasit/ncps/ent/narinfo"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"

	"github.com/kalbasit/ncps/ent"
	"github.com/kalbasit/ncps/pkg/cache"
	"github.com/kalbasit/ncps/pkg/config"
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
				Name:  flagNameDryRun,
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
			dbClient, err := createDatabaseClient(cmd)
			if err != nil {
				return fmt.Errorf("error creating database client: %w", err)
			}

			registerShutdown("database client", func(_ context.Context) error { return dbClient.Close() })

			// 2a. Load CDC configuration from database (must happen after DB creation)
			// CDC is required for migrate-nar-to-chunks command
			locker, rwLocker, err := getLockers(ctx, cmd)
			if err != nil {
				return fmt.Errorf("error creating lockers: %w", err)
			}

			cfg := config.New(dbClient, rwLocker)

			cdcEnabledStr, err := cfg.GetCDCEnabled(ctx)
			if err != nil {
				if errors.Is(err, config.ErrConfigNotFound) {
					//nolint:err113 // no need to define package level error for this.
					return errors.New(
						"migrate-nar-to-chunks command requires CDC to be enabled in the database; " +
							"please run 'ncps serve' with CDC enabled first",
					)
				}

				return fmt.Errorf("error loading CDC enabled flag from database: %w", err)
			}

			if cdcEnabledStr != configValueTrue {
				//nolint:err113 // no need to define package level error for this.
				return errors.New("migrate-nar-to-chunks command requires CDC to be enabled in the database")
			}

			// 3. Setup OTel
			extraResourceAttrs, err := detectExtraResourceAttrs(ctx, cmd, dbClient, rwLocker)
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
			// Note: createCache will load CDC values from the database since we don't have CDC flags
			c, err := createCache(ctx, cmd, dbClient, locker, rwLocker, nil)
			if err != nil {
				return fmt.Errorf("error creating cache: %w", err)
			}
			defer c.Close()

			// Ensure lazy chunking is disabled during the migration
			c.SetCDCLazyChunking(false, 0)

			// 5. Setup Storage
			_, narInfoStore, narStore, err := getStorageBackend(ctx, cmd)
			if err != nil {
				return fmt.Errorf("error creating storage backend: %w", err)
			}

			// 6. Safety Check: Ensure all narinfos are migrated
			var unmigratedHashesCount int32

			err = narInfoStore.WalkNarInfos(ctx, func(hash string) error {
				ni, err := dbClient.Ent().NarInfo.Query().
					Where(entnarinfo.HashEQ(hash)).
					Only(ctx)
				if err != nil {
					if ent.IsNotFound(err) {
						atomic.AddInt32(&unmigratedHashesCount, 1)

						return nil
					}

					return fmt.Errorf("failed to fetch narinfo from database: %w", err)
				}

				if ni.URL == nil {
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

			totalToChunkInt, err := dbClient.Ent().NarFile.Query().
				Where(entnarfile.TotalChunksEQ(0)).
				Count(ctx)
			if err != nil {
				return fmt.Errorf("failed to fetch total count of NAR files to chunk: %w", err)
			}

			totalToChunk := int64(totalToChunkInt)

			var (
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
						processed := atomic.LoadInt32(&totalProcessed)
						succeeded := atomic.LoadInt32(&totalSucceeded)
						failed := atomic.LoadInt32(&totalFailed)
						skipped := atomic.LoadInt32(&totalSkipped)

						var rate float64
						if durationInSeconds := elapsed.Seconds(); durationInSeconds > 0 {
							rate = float64(processed) / durationInSeconds
						}

						var percent float64
						if totalToChunk > 0 {
							percent = float64(processed) / float64(totalToChunk) * 100
						}

						logger.Info().
							Int64("total", totalToChunk).
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

			narFiles, err := dbClient.Ent().NarFile.Query().
				Where(entnarfile.TotalChunksEQ(0)).
				Order(ent.Asc(entnarfile.FieldID)).
				Select(
					entnarfile.FieldID,
					entnarfile.FieldHash,
					entnarfile.FieldCompression,
					entnarfile.FieldQuery,
					entnarfile.FieldFileSize,
				).
				All(ctx)
			if err != nil {
				return fmt.Errorf("failed to fetch candidate NAR files from database: %w", err)
			}

			for _, row := range narFiles {
				g.Go(func() error {
					log := logger.With().Str("nar_hash", row.Hash).Logger()

					narURL := nar.URL{
						Hash:        row.Hash,
						Compression: nar.CompressionType(row.Compression),
						Query:       make(map[string][]string),
					}

					if row.Query != "" {
						q, err := url.ParseQuery(row.Query)
						if err != nil {
							log.Error().Err(err).Str("query", row.Query).Msg("failed to parse nar query")
							atomic.AddInt32(&totalFailed, 1)
							RecordMigrationObject(ctx, MigrationTypeNarToChunks, MigrationOperationMigrate, MigrationResultFailure)

							return nil
						}

						narURL.Query = q
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

					// Save original narURL before migration (which may normalize compression to "none")
					originalNarURL := narURL

					opStartTime := time.Now()
					err = c.MigrateNarToChunks(ctx, &narURL)

					if errors.Is(err, cache.ErrMigrationInProgress) {
						// no need to do anything, another instance is already migrating this nar.
						return nil
					}

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
					// Use originalNarURL to delete the file with the correct compression path.
					if err := narStore.DeleteNar(ctx, originalNarURL); err != nil && !errors.Is(err, storage.ErrNotFound) {
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

			duration := time.Since(startTime)
			processed := atomic.LoadInt32(&totalProcessed)
			succeeded := atomic.LoadInt32(&totalSucceeded)
			failed := atomic.LoadInt32(&totalFailed)
			skipped := atomic.LoadInt32(&totalSkipped)

			var rate float64
			if durationInSeconds := duration.Seconds(); durationInSeconds > 0 {
				rate = float64(processed) / durationInSeconds
			}

			logger.Info().
				Int64("total", totalToChunk).
				Int32("processed", processed).
				Int32("succeeded", succeeded).
				Int32("failed", failed).
				Int32("skipped", skipped).
				Str("duration", duration.Round(time.Millisecond).String()).
				Float64("rate", rate).
				Msg("migration completed")

			RecordMigrationBatchSize(ctx, MigrationTypeNarToChunks, totalToChunk)

			return nil
		},
	}
}

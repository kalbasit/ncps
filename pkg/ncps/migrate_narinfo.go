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

	"github.com/kalbasit/ncps/pkg/cache"
	"github.com/kalbasit/ncps/pkg/lock"
	"github.com/kalbasit/ncps/pkg/lock/local"
	"github.com/kalbasit/ncps/pkg/lock/redis"
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

			// 2. Setup Storage
			_, narInfoStore, _, err := getStorageBackend(ctx, cmd)
			if err != nil {
				logger.Error().Err(err).Msg("error creating storage backend")

				return err
			}

			walker, ok := narInfoStore.(NarInfoWalker)
			if !ok {
				return ErrStorageIterationNotSupported
			}

			// 3. Setup Lock Backend (optional - for coordination with running instances)
			locker, err := createLocker(ctx, cmd)
			if err != nil {
				logger.Error().Err(err).Msg("error creating lock backend")

				return err
			}

			redisAddrs := cmd.StringSlice("cache-redis-addrs")
			if len(redisAddrs) > 0 {
				logger.Info().
					Strs("redis_addrs", redisAddrs).
					Msg("using Redis for distributed locking (can coordinate with running ncps instances)")
			} else {
				logger.Info().Msg("using in-memory locking (no coordination with other instances)")
			}

			// 4. Setup Migrated Hashes Map
			logger.Info().Msg("fetching existing narinfo hashes from the database")

			migratedHashes, err := db.GetMigratedNarInfoHashes(ctx)
			if err != nil {
				return fmt.Errorf("failed to fetch migrated hashes from database: %w", err)
			}

			migratedHashesMap := make(map[string]struct{}, len(migratedHashes))
			for _, hash := range migratedHashes {
				migratedHashesMap[hash] = struct{}{}
			}

			logger.Info().Int("count", len(migratedHashesMap)).Msg("loaded migrated hashes from database")

			// 5. Migrate
			logger.Info().Msg("starting migration")

			startTime := time.Now()

			var totalFound int32

			var totalProcessed int32

			var totalSucceeded int32

			var totalFailed int32

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
						found := atomic.LoadInt32(&totalFound)
						processed := atomic.LoadInt32(&totalProcessed)
						succeeded := atomic.LoadInt32(&totalSucceeded)
						failed := atomic.LoadInt32(&totalFailed)

						var rate float64
						if durationInSeconds := elapsed.Seconds(); durationInSeconds > 0 {
							rate = float64(processed) / durationInSeconds
						}

						logger.Info().
							Int32("found", found).
							Int32("processed", processed).
							Int32("succeeded", succeeded).
							Int32("failed", failed).
							Str("elapsed", elapsed.Round(time.Second).String()).
							Float64("rate", rate).
							Msg("migration progress")
					case <-progressDone:
						return
					}
				}
			}()

			err = walker.WalkNarInfos(ctx, func(hash string) error {
				atomic.AddInt32(&totalFound, 1)

				if _, ok := migratedHashesMap[hash]; ok {
					// Already migrated - only delete from storage if not dry-run
					g.Go(func() error {
						atomic.AddInt32(&totalProcessed, 1)

						log := logger.With().Str("hash", hash).Logger()
						ctxWithLog := log.WithContext(ctx)
						log.Info().Msg("narinfo already migrated, deleting from storage")

						if dryRun {
							log.Info().Msg("[DRY-RUN] would delete from storage")
							atomic.AddInt32(&totalSucceeded, 1)
							RecordMigrationNarInfo(ctxWithLog, MigrationOperationDelete, MigrationResultSuccess)

							return nil
						}

						if err := narInfoStore.DeleteNarInfo(ctxWithLog, hash); err != nil {
							log.Error().Err(err).Msg("failed to delete from store")
							atomic.AddInt32(&totalFailed, 1)
							RecordMigrationNarInfo(ctxWithLog, MigrationOperationDelete, MigrationResultFailure)
						} else {
							atomic.AddInt32(&totalSucceeded, 1)
							RecordMigrationNarInfo(ctxWithLog, MigrationOperationDelete, MigrationResultSuccess)
						}

						return nil
					})

					return nil
				}

				g.Go(func() error {
					atomic.AddInt32(&totalProcessed, 1)

					log := logger.With().Str("hash", hash).Logger()
					log.Info().Msg("processing narinfo")

					ctxWithLog := log.WithContext(ctx)

					opStartTime := time.Now()

					defer func() {
						RecordMigrationDuration(ctxWithLog, MigrationOperationMigrate, time.Since(opStartTime).Seconds())
					}()

					// Fetch narinfo from storage
					ni, err := narInfoStore.GetNarInfo(ctxWithLog, hash)
					if err != nil {
						log.Error().Err(err).Msg("failed to get narinfo from store")
						atomic.AddInt32(&totalFailed, 1)
						RecordMigrationNarInfo(ctxWithLog, MigrationOperationMigrate, MigrationResultFailure)

						return nil
					}

					if dryRun {
						log.Info().Msg("[DRY-RUN] would migrate and delete")
						atomic.AddInt32(&totalSucceeded, 1)
						RecordMigrationNarInfo(ctxWithLog, MigrationOperationMigrate, MigrationResultSuccess)

						return nil
					}

					// Use the shared migration function from pkg/cache
					// Pass narInfoStore to enable deletion after migration
					if err := cache.MigrateNarInfo(ctxWithLog, locker, db, narInfoStore, hash, ni); err != nil {
						log.Error().Err(err).Msg("failed to migrate narinfo")
						atomic.AddInt32(&totalFailed, 1)
						RecordMigrationNarInfo(ctxWithLog, MigrationOperationMigrate, MigrationResultFailure)

						return nil
					}

					atomic.AddInt32(&totalSucceeded, 1)
					RecordMigrationNarInfo(ctxWithLog, MigrationOperationMigrate, MigrationResultSuccess)

					return nil
				})

				return nil
			})
			if err != nil {
				return err
			}

			if err := g.Wait(); err != nil {
				return err
			}

			// Final summary
			duration := time.Since(startTime)
			found := atomic.LoadInt32(&totalFound)
			processed := atomic.LoadInt32(&totalProcessed)
			succeeded := atomic.LoadInt32(&totalSucceeded)
			failed := atomic.LoadInt32(&totalFailed)

			var rate float64
			if durationInSeconds := duration.Seconds(); durationInSeconds > 0 {
				rate = float64(processed) / durationInSeconds
			}

			// Record batch size metric
			RecordMigrationBatchSize(ctx, int64(found))

			logger.Info().
				Int32("found", found).
				Int32("processed", processed).
				Int32("succeeded", succeeded).
				Int32("failed", failed).
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

// createLocker creates a lock.Locker based on configuration.
// Returns an in-memory locker by default, or a Redis locker if Redis addresses are provided.
func createLocker(ctx context.Context, cmd *cli.Command) (lock.Locker, error) {
	redisAddrs := cmd.StringSlice("cache-redis-addrs")

	if len(redisAddrs) == 0 {
		// Use in-memory locker (no coordination with other instances)
		return local.NewLocker(), nil
	}

	// Use Redis locker for distributed coordination
	redisCfg := redis.Config{
		Addrs:     redisAddrs,
		Username:  cmd.String("cache-redis-username"),
		Password:  cmd.String("cache-redis-password"),
		DB:        cmd.Int("cache-redis-db"),
		UseTLS:    cmd.Bool("cache-redis-use-tls"),
		KeyPrefix: "ncps:lock:", // Default prefix used by the cache
	}

	// Use default retry config and enable degraded mode for migration
	// (migration can continue even if some Redis nodes are down)
	retryCfg := lock.RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     2 * time.Second,
	}

	redisLocker, err := redis.NewLocker(ctx, redisCfg, retryCfg, true)
	if err != nil {
		return nil, fmt.Errorf("failed to create Redis locker: %w", err)
	}

	return redisLocker, nil
}

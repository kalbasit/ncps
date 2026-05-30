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
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"

	"github.com/kalbasit/ncps/ent"
	"github.com/kalbasit/ncps/pkg/cache"
	"github.com/kalbasit/ncps/pkg/config"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/otel"
)

// ErrChunksToNarFailures is returned when one or more NARs failed to migrate
// back to whole files.
var ErrChunksToNarFailures = errors.New("one or more nars failed to migrate to whole files")

func migrateChunksToNarCommand(
	flagSources flagSourcesFn,
	registerShutdown registerShutdownFn,
) *cli.Command {
	return &cli.Command{
		Name:  "migrate-chunks-to-nar",
		Usage: "Migrate CDC-chunked NARs back to whole files (reverse of migrate-nar-to-chunks)",
		Description: `Reconstructs CDC-chunked NARs into whole files so a deployment can exit CDC.
For each chunked nar_file, the NAR is reassembled from its chunks, verified against the linked
narinfo's recorded NarHash, written to the NAR store as a whole file, and the record is flipped to
the whole-file representation. Chunks left unreferenced by any nar_file are then reclaimed.
NARs whose narinfo has no recorded NarHash are left chunked (skipped) rather than de-chunked unverified.`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  flagNameDryRun,
				Usage: "Report which NARs would be de-chunked without writing whole files, mutating records, or deleting chunks",
			},
			&cli.BoolFlag{
				Name: "force-reclaim",
				Usage: "Immediately delete chunks left unreferenced by the migration instead of leaving them for the GC. " +
					"Only use when traffic is drained (e.g. a maintenance window): deleting a chunk while a client is " +
					"mid-stream from chunks would truncate that transfer.",
			},

			&cli.StringFlag{
				Name:    flagNameCacheTempPath,
				Usage:   "The path to the temporary directory that is used by the cache to reconstruct NAR files",
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
				Usage:   flagUsageRedisAddrs,
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
				Name:    flagNameConcurrency,
				Usage:   flagUsageConcurrency,
				Value:   10,
				Sources: flagSources("concurrency", "CONCURRENCY"),
			},
		},
		Action: migrateChunksToNarAction(registerShutdown),
	}
}

//nolint:gocognit,cyclop // mirrors migrate-nar-to-chunks; the per-NAR state machine is inherently branchy.
func migrateChunksToNarAction(registerShutdown registerShutdownFn) cli.ActionFunc {
	return func(ctx context.Context, cmd *cli.Command) error {
		logger := zerolog.Ctx(ctx).With().Str("cmd", "migrate-chunks-to-nar").Logger()
		ctx = logger.WithContext(ctx)

		dryRun := cmd.Bool("dry-run")
		forceReclaim := cmd.Bool("force-reclaim")

		dbClient, err := createDatabaseClient(cmd)
		if err != nil {
			return fmt.Errorf("error creating database client: %w", err)
		}

		registerShutdown("database client", func(_ context.Context) error { return dbClient.Close() })

		locker, rwLocker, err := getLockers(ctx, cmd)
		if err != nil {
			return fmt.Errorf("error creating lockers: %w", err)
		}

		cfg := config.New(dbClient, rwLocker)

		cdcEnabledStr, err := cfg.GetCDCEnabled(ctx)
		if err != nil {
			if errors.Is(err, config.ErrConfigNotFound) {
				//nolint:err113 // no need to define a package-level error for this one-off message.
				return errors.New(
					"migrate-chunks-to-nar command requires CDC to have been enabled in the database; " +
						"there is nothing chunked to migrate back",
				)
			}

			return fmt.Errorf("error loading CDC enabled flag from database: %w", err)
		}

		if cdcEnabledStr != configValueTrue {
			//nolint:err113 // no need to define a package-level error for this one-off message.
			return errors.New("migrate-chunks-to-nar command requires CDC to have been enabled in the database")
		}

		extraResourceAttrs, err := detectExtraResourceAttrs(ctx, cmd, dbClient, rwLocker)
		if err != nil {
			return fmt.Errorf("error detecting extra resource attributes: %w", err)
		}

		otelResource, err := otel.NewResource(ctx, cmd.Root().Name, Version, semconv.SchemaURL, extraResourceAttrs...)
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

		// createCache wires the NAR store and the chunk store (loading CDC config from
		// the database), which is everything MigrateChunksToNar needs.
		c, err := createCache(ctx, cmd, dbClient, locker, rwLocker, nil)
		if err != nil {
			return fmt.Errorf("error creating cache: %w", err)
		}
		defer c.Close()

		// Don't kick off lazy re-chunking while we are de-chunking.
		c.SetCDCLazyChunking(false, 0)

		logger.Info().Bool("dry_run", dryRun).Msg("starting migration of chunked NARs to whole files")

		startTime := time.Now()

		narFiles, err := dbClient.Ent().NarFile.Query().
			Where(entnarfile.TotalChunksGT(0)).
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
			return fmt.Errorf("failed to fetch chunked NAR files from database: %w", err)
		}

		total := int64(len(narFiles))

		var (
			totalProcessed int32
			totalSucceeded int32
			totalFailed    int32
			totalSkipped   int32
		)

		g, ctx := errgroup.WithContext(ctx)
		g.SetLimit(cmd.Int("concurrency"))

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
						RecordMigrationObject(ctx, MigrationTypeChunksToNar, MigrationOperationMigrate, MigrationResultFailure)

						return nil
					}

					narURL.Query = q
				}

				atomic.AddInt32(&totalProcessed, 1)

				if dryRun {
					log.Info().Msg("[DRY-RUN] would reconstruct, verify, and de-chunk nar to a whole file")
					atomic.AddInt32(&totalSucceeded, 1)
					RecordMigrationObject(ctx, MigrationTypeChunksToNar, MigrationOperationMigrate, MigrationResultSuccess)

					return nil
				}

				opStartTime := time.Now()
				err := c.MigrateChunksToNar(ctx, &narURL, forceReclaim)
				RecordMigrationDuration(
					ctx,
					MigrationTypeChunksToNar,
					MigrationOperationMigrate,
					time.Since(opStartTime).Seconds(),
				)

				switch {
				case errors.Is(err, cache.ErrMigrationInProgress):
					// Another instance is handling this hash; not our failure.
					atomic.AddInt32(&totalSkipped, 1)
					RecordMigrationObject(ctx, MigrationTypeChunksToNar, MigrationOperationMigrate, MigrationResultSkipped)
				case errors.Is(err, cache.ErrNarAlreadyWholeFile):
					atomic.AddInt32(&totalSkipped, 1)
					RecordMigrationObject(ctx, MigrationTypeChunksToNar, MigrationOperationMigrate, MigrationResultSkipped)
				case errors.Is(err, cache.ErrNoNarHashToVerify):
					log.Warn().Msg("no narinfo NarHash to verify against; leaving nar chunked")
					atomic.AddInt32(&totalSkipped, 1)
					RecordMigrationObject(ctx, MigrationTypeChunksToNar, MigrationOperationMigrate, MigrationResultSkipped)
				case err != nil:
					log.Error().Err(err).Msg("failed to migrate chunks to whole nar")
					atomic.AddInt32(&totalFailed, 1)
					RecordMigrationObject(ctx, MigrationTypeChunksToNar, MigrationOperationMigrate, MigrationResultFailure)
				default:
					atomic.AddInt32(&totalSucceeded, 1)
					RecordMigrationObject(ctx, MigrationTypeChunksToNar, MigrationOperationMigrate, MigrationResultSuccess)
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

		logger.Info().
			Int64("total", total).
			Int32("processed", processed).
			Int32("succeeded", succeeded).
			Int32("failed", failed).
			Int32("skipped", skipped).
			Str("duration", duration.Round(time.Millisecond).String()).
			Msg("migration completed")

		RecordMigrationBatchSize(ctx, MigrationTypeChunksToNar, total)

		if failed > 0 {
			return fmt.Errorf("%w (%d failed)", ErrChunksToNarFailures, failed)
		}

		return nil
	}
}

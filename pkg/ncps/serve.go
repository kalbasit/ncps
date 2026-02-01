package ncps

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog"
	"github.com/sysbot/go-netrc"
	"github.com/urfave/cli/v3"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"golang.org/x/sync/errgroup"

	s3config "github.com/kalbasit/ncps/pkg/s3"
	localstorage "github.com/kalbasit/ncps/pkg/storage/local"
	storageS3 "github.com/kalbasit/ncps/pkg/storage/s3"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"

	"github.com/kalbasit/ncps/pkg/analytics"
	"github.com/kalbasit/ncps/pkg/cache"
	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/config"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/helper"
	"github.com/kalbasit/ncps/pkg/lock"
	"github.com/kalbasit/ncps/pkg/lock/local"
	"github.com/kalbasit/ncps/pkg/lock/postgres"
	"github.com/kalbasit/ncps/pkg/lock/redis"
	"github.com/kalbasit/ncps/pkg/maxprocs"
	"github.com/kalbasit/ncps/pkg/otel"
	"github.com/kalbasit/ncps/pkg/prometheus"
	"github.com/kalbasit/ncps/pkg/server"
	"github.com/kalbasit/ncps/pkg/storage"
	"github.com/kalbasit/ncps/pkg/storage/chunk"
)

var (
	// ErrCacheMaxSizeRequired is returned if --cache-lru-schedule was given but not --cache-max-size.
	ErrCacheMaxSizeRequired = errors.New("--cache-max-size is required when --cache-lru-schedule is specified")

	// ErrStorageConfigRequired is returned if neither local nor S3 storage is configured.
	ErrStorageConfigRequired = errors.New("either --cache-storage-local or --cache-storage-s3-bucket is required")

	ErrS3ConfigIncomplete = errors.New(
		"S3 requires --cache-storage-s3-endpoint, --cache-storage-s3-access-key-id, and --cache-storage-s3-secret-access-key",
	)

	// ErrStorageConflict is returned if both local and S3 storage are configured.
	ErrStorageConflict = errors.New("cannot use both --cache-storage-local and --cache-storage-s3-bucket")

	// ErrUpstreamCacheRequired is returned if no upstream cache is configured.
	ErrUpstreamCacheRequired = errors.New("at least one --cache-upstream-url is required")

	// ErrRedisAddrsRequired is returned when Redis backend is selected but no addresses are provided.
	ErrRedisAddrsRequired = errors.New("--cache-lock-backend=redis requires --cache-redis-addrs to be set")

	// ErrUnknownLockBackend is returned when an unknown lock backend is specified.
	ErrUnknownLockBackend = errors.New("unknown lock backend")
)

const (
	lockBackendLocal    = "local"
	lockBackendRedis    = "redis"
	lockBackendPostgres = "postgres"
)

// parseNetrcFile parses the netrc file and returns the parsed netrc object.
func parseNetrcFile(netrcPath string) (*netrc.Netrc, error) {
	file, err := os.Open(netrcPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	n, err := netrc.Parse(file)
	if err != nil {
		return nil, fmt.Errorf("error parsing netrc file: %w", err)
	}

	return n, nil
}

func serveCommand(
	userDirs userDirectories,
	flagSources flagSourcesFn,
	registerShutdown registerShutdownFn,
) *cli.Command {
	return &cli.Command{
		Name:    "serve",
		Aliases: []string{"s"},
		Usage:   "serve the nix binary cache over http",
		Action:  serveAction(registerShutdown),
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "cache-allow-delete-verb",
				Usage:   "Whether to allow the DELETE verb to delete narInfo and nar files",
				Sources: flagSources("cache.allow-delete-verb", "CACHE_ALLOW_DELETE_VERB"),
			},
			&cli.BoolFlag{
				Name:    "cache-allow-put-verb",
				Usage:   "Whether to allow the PUT verb to push narInfo and nar files directly",
				Sources: flagSources("cache.allow-put-verb", "CACHE_ALLOW_PUT_VERB"),
			},
			&cli.StringFlag{
				Name:     "cache-hostname",
				Usage:    "The hostname of the cache server",
				Sources:  flagSources("cache.hostname", "CACHE_HOSTNAME"),
				Required: true,
			},
			&cli.StringFlag{
				Name:    "cache-storage-local",
				Usage:   "The local data path used for configuration and cache storage (use this OR S3 storage)",
				Sources: flagSources("cache.storage.local", "CACHE_STORAGE_LOCAL"),
			},
			// S3 Storage flags
			&cli.StringFlag{
				Name:    "cache-storage-s3-bucket",
				Usage:   "S3 bucket name for storage (use this OR --cache-storage-local for local storage)",
				Sources: flagSources("cache.storage.s3.bucket", "CACHE_STORAGE_S3_BUCKET"),
			},
			&cli.StringFlag{
				Name:    "cache-storage-s3-endpoint",
				Usage:   "S3-compatible endpoint URL with scheme (e.g., https://s3.amazonaws.com or http://minio.example.com:9000)",
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
				Usage:   "Force path-style S3 addressing (bucket/key vs key.bucket) - required for MinIO, optional for AWS S3",
				Sources: flagSources("cache.storage.s3.force-path-style", "CACHE_STORAGE_S3_FORCE_PATH_STYLE"),
			},
			// CDC Flags
			&cli.BoolFlag{
				Name:    "cache-cdc-enabled",
				Usage:   "Enable Content-Defined Chunking (CDC) for deduplication (experimental)",
				Sources: flagSources("cache.cdc.enabled", "CACHE_CDC_ENABLED"),
			},
			&cli.IntFlag{
				Name:    "cache-cdc-min",
				Usage:   "Minimum chunk size for CDC in bytes",
				Sources: flagSources("cache.cdc.min", "CACHE_CDC_MIN"),
				Value:   65536, // 64KB
			},
			&cli.IntFlag{
				Name:    "cache-cdc-avg",
				Usage:   "Average chunk size for CDC in bytes",
				Sources: flagSources("cache.cdc.avg", "CACHE_CDC_AVG"),
				Value:   262144, // 256KB
			},
			&cli.IntFlag{
				Name:    "cache-cdc-max",
				Usage:   "Maximum chunk size for CDC in bytes",
				Sources: flagSources("cache.cdc.max", "CACHE_CDC_MAX"),
				Value:   1048576, // 1MB
			},
			&cli.StringFlag{
				Name:     "cache-database-url",
				Usage:    "The URL of the database",
				Sources:  flagSources("cache.database-url", "CACHE_DATABASE_URL"),
				Required: true,
			},
			&cli.IntFlag{
				Name:    "cache-database-pool-max-open-conns",
				Usage:   "Maximum number of open connections to the database (0 = use database-specific defaults)",
				Sources: flagSources("cache.database.pool.max-open-conns", "CACHE_DATABASE_POOL_MAX_OPEN_CONNS"),
			},
			&cli.IntFlag{
				Name:    "cache-database-pool-max-idle-conns",
				Usage:   "Maximum number of idle connections in the pool (0 = use database-specific defaults)",
				Sources: flagSources("cache.database.pool.max-idle-conns", "CACHE_DATABASE_POOL_MAX_IDLE_CONNS"),
			},
			&cli.StringFlag{
				Name: "cache-max-size",
				//nolint:lll
				Usage:   "The maximum size of the store. It can be given with units such as 5K, 10G etc. Supported units: B, K, M, G, T",
				Sources: flagSources("cache.max-size", "CACHE_MAX_SIZE"),
				Validator: func(s string) error {
					_, err := helper.ParseSize(s)

					return err
				},
			},
			&cli.StringFlag{
				Name: "cache-lru-schedule",
				//nolint:lll
				Usage:   "The cron spec for cleaning the store. Refer to https://pkg.go.dev/github.com/robfig/cron/v3#hdr-Usage for documentation",
				Sources: flagSources("cache.lru.schedule", "CACHE_LRU_SCHEDULE"),
				Validator: func(s string) error {
					_, err := cron.ParseStandard(s)

					return err
				},
			},
			&cli.StringFlag{
				Name:    "cache-lru-schedule-timezone",
				Usage:   "The name of the timezone to use for the cron",
				Sources: flagSources("cache.lru.timezone", "CACHE_LRU_SCHEDULE_TZ"),
				Value:   "Local",
			},
			&cli.StringFlag{
				Name: "cache-secret-key-path",
				Usage: "The path to the secret key used for signing cached paths. " +
					"If set, it will be stored in the database if different.",
				Sources: flagSources("cache.secret-key-path", "CACHE_SECRET_KEY_PATH"),
			},
			&cli.BoolFlag{
				Name:    "cache-sign-narinfo",
				Usage:   "Whether to sign narInfo files or passthru as-is from upstream",
				Sources: flagSources("cache.sign-narinfo", "CACHE_SIGN_NARINFO"),
				Value:   true,
			},
			&cli.StringFlag{
				Name:    "cache-temp-path",
				Usage:   "The path to the temporary directory that is used by the cache to download NAR files",
				Sources: flagSources("cache.temp-path", "CACHE_TEMP_PATH"),
				Value:   os.TempDir(),
			},
			&cli.StringSliceFlag{
				Name:    "cache-upstream-url",
				Usage:   "Set to URL (with scheme) for each upstream cache",
				Sources: flagSources("cache.upstream.urls", "CACHE_UPSTREAM_URLS"),
				// TODO: Once --upstream-cache is removed, mark this as required and
				// remove the custom validation block below.
				// Required: true,
			},
			&cli.StringSliceFlag{
				Name:    "cache-upstream-public-key",
				Usage:   "Set to host:public-key for each upstream cache",
				Sources: flagSources("cache.upstream.public-keys", "CACHE_UPSTREAM_PUBLIC_KEYS"),
			},
			&cli.DurationFlag{
				Name:    "cache-upstream-dialer-timeout",
				Usage:   "Timeout for establishing TCP connections to upstream caches (e.g., 3s, 5s, 10s)",
				Sources: flagSources("cache.upstream.dialer-timeout", "CACHE_UPSTREAM_DIALER_TIMEOUT"),
				Value:   3 * time.Second,
			},
			&cli.DurationFlag{
				Name:    "cache-upstream-response-header-timeout",
				Usage:   "Timeout for waiting for upstream server's response headers (e.g., 3s, 5s, 10s)",
				Sources: flagSources("cache.upstream.response-header-timeout", "CACHE_UPSTREAM_RESPONSE_HEADER_TIMEOUT"),
				Value:   3 * time.Second,
			},
			&cli.StringFlag{
				Name:    "netrc-file",
				Usage:   "Path to netrc file for upstream authentication",
				Sources: flagSources("cache.netrc-file", "NETRC_FILE"),
				Value:   filepath.Join(userDirs.homeDir, ".netrc"),
			},
			&cli.StringFlag{
				Name:    "server-addr",
				Usage:   "The address of the server",
				Sources: flagSources("server.addr", "SERVER_ADDR"),
				Value:   ":8501",
			},

			// Redis Configuration (optional - for distributed locking in HA deployments)
			&cli.StringSliceFlag{
				Name:    "cache-redis-addrs",
				Usage:   "Redis server addresses for distributed locking (e.g., localhost:6379). If not set, local locks are used.",
				Sources: flagSources("cache.redis.addrs", "CACHE_REDIS_ADDRS"),
			},
			&cli.StringFlag{
				Name:    "cache-redis-username",
				Usage:   "Redis username for authentication (for Redis ACL)",
				Sources: flagSources("cache.redis.username", "CACHE_REDIS_USERNAME"),
			},
			&cli.StringFlag{
				Name:    "cache-redis-password",
				Usage:   "Redis password for authentication",
				Sources: flagSources("cache.redis.password", "CACHE_REDIS_PASSWORD"),
			},
			&cli.IntFlag{
				Name:    "cache-redis-db",
				Usage:   "Redis database number (0-15)",
				Sources: flagSources("cache.redis.db", "CACHE_REDIS_DB"),
				Value:   0,
			},
			&cli.BoolFlag{
				Name:    "cache-redis-use-tls",
				Usage:   "Use TLS for Redis connection",
				Sources: flagSources("cache.redis.use-tls", "CACHE_REDIS_USE_TLS"),
			},
			&cli.IntFlag{
				Name:    "cache-redis-pool-size",
				Usage:   "Redis connection pool size",
				Sources: flagSources("cache.redis.pool-size", "CACHE_REDIS_POOL_SIZE"),
				Value:   10,
			},

			&cli.StringFlag{
				Name: "cache-lock-backend",
				Usage: "Lock backend to use: 'local' (single instance), 'redis' (distributed), " +
					"or 'postgres' (distributed, requires PostgreSQL)",
				Sources: flagSources("cache.lock.backend", "CACHE_LOCK_BACKEND"),
				Value:   "local",
			},
			// Lock Configuration
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

			// DEPRECATED FLAGS BELOW

			&cli.StringFlag{
				Name:    "cache-data-path",
				Usage:   "DEPRECATED: Use --cache-storage-local instead.",
				Sources: flagSources("cache.data-path", "CACHE_DATA_PATH"),
			},
			&cli.StringSliceFlag{
				Name:    "upstream-cache",
				Usage:   "DEPRECATED: Use --cache-upstream-url instead.",
				Sources: flagSources("cache.upstream.caches", "UPSTREAM_CACHES"),
			},
			&cli.StringSliceFlag{
				Name:    "upstream-public-key",
				Usage:   "DEPRECATED: Use --cache-upstream-public-key instead.",
				Sources: cli.EnvVars("UPSTREAM_PUBLIC_KEYS"),
			},
			&cli.DurationFlag{
				Name:    "upstream-dialer-timeout",
				Usage:   "DEPRECATED: Use --cache-upstream-dialer-timeout instead.",
				Sources: cli.EnvVars("UPSTREAM_DIALER_TIMEOUT"),
				Value:   3 * time.Second,
			},
			&cli.DurationFlag{
				Name:    "upstream-response-header-timeout",
				Usage:   "DEPRECATED: Use --cache-upstream-response-header-timeout instead.",
				Sources: cli.EnvVars("UPSTREAM_RESPONSE_HEADER_TIMEOUT"),
				Value:   3 * time.Second,
			},
		},
	}
}

func serveAction(registerShutdown registerShutdownFn) cli.ActionFunc {
	return func(ctx context.Context, cmd *cli.Command) error {
		logger := zerolog.Ctx(ctx).With().Str("cmd", "serve").Logger()

		ctx = logger.WithContext(ctx)

		ctx, cancel := context.WithCancel(ctx)

		g, ctx := errgroup.WithContext(ctx)

		defer func() {
			if err := g.Wait(); err != nil {
				logger.Error().Err(err).Msg("error returned from g.Wait()")
			}
		}()

		// NOTE: Reminder that defer statements run last to first so the first
		// thing that happens here is the context is canceled which triggers the
		// errgroup 'g' to start exiting.
		defer cancel()

		g.Go(func() error {
			return maxprocs.AutoMaxProcs(ctx, 30*time.Second, logger)
		})

		db, err := createDatabaseQuerier(cmd)
		if err != nil {
			zerolog.Ctx(ctx).
				Error().
				Err(err).
				Msg("error creating database querier")

			return err
		}

		locker, rwLocker, err := getLockers(ctx, cmd, db)
		if err != nil {
			zerolog.Ctx(ctx).
				Error().
				Err(err).
				Msg("error creating the lockers")

			return err
		}

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

		if cmd.Root().Bool("prometheus-enabled") {
			gatherer, shutdown, err := prometheus.SetupPrometheusMetrics(otelResource)
			if err != nil {
				return fmt.Errorf("error setting up Prometheus metrics: %w", err)
			}

			registerShutdown("prometheus", shutdown)

			server.SetPrometheusGatherer(gatherer)

			logger.
				Info().
				Msg("Prometheus metrics enabled at /metrics")
		}

		analyticsReporter := analytics.Ctx(ctx) // get the noop reporter
		if cmd.Bool("analytics-reporting-enabled") || cmd.Bool("analytics-reporting-samples") {
			analyticsResource, err := analytics.NewResource(
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
					Msg("error creating a new analytics resource")

				return err
			}

			analyticsReporter, err = analytics.New(
				ctx,
				db,
				analyticsResource,
				cmd.Bool("analytics-reporting-samples"),
			)
			if err != nil {
				zerolog.Ctx(ctx).
					Error().
					Err(err).
					Msg("error creating a new analytics reporter")

				return err
			}

			registerShutdown("analytics", analyticsReporter.Shutdown)
		}

		// add the reporter to the context
		ctx = analyticsReporter.WithContext(ctx)

		netrcData, err := parseNetrcFile(cmd.String("netrc-file"))
		if err != nil {
			logger.Warn().Err(err).Msg("failed to parse netrc file, proceeding without netrc authentication")
		}

		ucs, err := getUpstreamCaches(ctx, cmd, netrcData)
		if err != nil {
			return fmt.Errorf("error computing the upstream caches: %w", err)
		}

		cache, err := createCache(ctx, cmd, db, locker, rwLocker, ucs)
		if err != nil {
			return err
		}

		// register the cache metrics
		if err := cache.RegisterUpstreamMetrics(analyticsReporter.GetMeter()); err != nil {
			zerolog.Ctx(ctx).
				Error().
				Err(err).
				Msg("error registering the cache metrics in the analytics reporter")

			return err
		}

		// report boot-up
		record := log.Record{}
		record.SetTimestamp(time.Now())
		record.SetSeverity(log.SeverityInfo)
		record.SetBody(log.StringValue("NCPS Started"))

		analyticsReporter.GetLogger().Emit(ctx, record)

		srv := server.New(cache)
		srv.SetDeletePermitted(cmd.Bool("cache-allow-delete-verb"))
		srv.SetPutPermitted(cmd.Bool("cache-allow-put-verb"))

		server := &http.Server{
			BaseContext:       func(net.Listener) context.Context { return ctx },
			Addr:              cmd.String("server-addr"),
			Handler:           srv,
			ReadHeaderTimeout: 10 * time.Second,
		}

		logger.Info().
			Str("server_addr", cmd.String("server-addr")).
			Msg("Server started")

		if err := server.ListenAndServe(); err != nil {
			return fmt.Errorf("error starting the HTTP listener: %w", err)
		}

		return nil
	}
}

func getUpstreamCaches(ctx context.Context, cmd *cli.Command, netrcData *netrc.Netrc) ([]*upstream.Cache, error) {
	// Handle backward compatibility for upstream flags (deprecated)
	deprecatedUpstreamCache := cmd.StringSlice("upstream-cache")
	upstreamURL := cmd.StringSlice("cache-upstream-url")
	deprecatedPublicKey := cmd.StringSlice("upstream-public-key")
	upstreamPublicKey := cmd.StringSlice("cache-upstream-public-key")
	deprecatedDialerTimeout := cmd.Duration("upstream-dialer-timeout")
	dialerTimeout := cmd.Duration("cache-upstream-dialer-timeout")
	deprecatedResponseHeaderTimeout := cmd.Duration("upstream-response-header-timeout")
	responseHeaderTimeout := cmd.Duration("cache-upstream-response-header-timeout")

	// Show deprecation warning for upstream-cache
	if len(deprecatedUpstreamCache) > 0 {
		zerolog.Ctx(ctx).Warn().
			Msg("--upstream-cache is deprecated, please use --cache-upstream-url instead")

		if len(upstreamURL) > 0 {
			zerolog.Ctx(ctx).Warn().
				Msg("Both --upstream-cache and --cache-upstream-url are set; ignoring the deprecated --upstream-cache")
		} else {
			// Use deprecated value if new one is not set
			upstreamURL = deprecatedUpstreamCache
		}
	}

	// This block is a workaround the fact that --cache-upstream-url cannot be
	// marked as required in order to support the deprecated flag
	// --upstream-cache.
	// TODO: Remove this block and the custom error once the --cache-upstream-url
	// flag is marked as required above.
	{
		// Filter out empty upstream URLs before validation.
		var validUpstreamURLs []string

		for _, u := range upstreamURL {
			if u != "" {
				validUpstreamURLs = append(validUpstreamURLs, u)
			}
		}

		upstreamURL = validUpstreamURLs

		// Validate that at least one upstream cache is configured
		if len(upstreamURL) == 0 {
			return nil, ErrUpstreamCacheRequired
		}
	}

	// Show deprecation warning for upstream-public-key
	if len(deprecatedPublicKey) > 0 {
		zerolog.Ctx(ctx).Warn().
			Msg("--upstream-public-key is deprecated, please use --cache-upstream-public-key instead")

		if len(upstreamPublicKey) > 0 {
			zerolog.Ctx(ctx).Warn().
				Msg("Both --upstream-public-key and --cache-upstream-public-key are set; " +
					"ignoring the deprecated --upstream-public-key")
		} else {
			// Use deprecated value if new one is not set
			upstreamPublicKey = deprecatedPublicKey
		}
	}

	// Show deprecation warning for upstream-dialer-timeout
	// Only warn if the value differs from the default (3s)
	if deprecatedDialerTimeout != 3*time.Second {
		zerolog.Ctx(ctx).Warn().
			Msg("--upstream-dialer-timeout is deprecated, please use --cache-upstream-dialer-timeout instead")

		if dialerTimeout != 3*time.Second {
			zerolog.Ctx(ctx).Warn().
				Msg("Both --upstream-dialer-timeout and --cache-upstream-dialer-timeout are set; " +
					"ignoring the deprecated --upstream-dialer-timeout")
		} else {
			// Use deprecated value if new one is not set
			dialerTimeout = deprecatedDialerTimeout
		}
	}

	// Show deprecation warning for upstream-response-header-timeout
	// Only warn if the value differs from the default (3s)
	if deprecatedResponseHeaderTimeout != 3*time.Second {
		zerolog.Ctx(ctx).Warn().
			Msg("--upstream-response-header-timeout is deprecated, " +
				"please use --cache-upstream-response-header-timeout instead")

		if responseHeaderTimeout != 3*time.Second {
			zerolog.Ctx(ctx).Warn().
				Msg("Both --upstream-response-header-timeout and --cache-upstream-response-header-timeout are set; " +
					"ignoring the deprecated --upstream-response-header-timeout")
		} else {
			// Use deprecated value if new one is not set
			responseHeaderTimeout = deprecatedResponseHeaderTimeout
		}
	}

	ucs := make([]*upstream.Cache, 0, len(upstreamURL))

	for _, us := range upstreamURL {
		u, err := url.Parse(us)
		if err != nil {
			return nil, fmt.Errorf("error parsing --cache-upstream-url=%q: %w", us, err)
		}

		// Build options for this upstream cache
		opts := &upstream.Options{
			DialerTimeout:         dialerTimeout,
			ResponseHeaderTimeout: responseHeaderTimeout,
		}

		// Find public keys for this upstream
		rx := regexp.MustCompile(fmt.Sprintf(`^%s-[0-9]+:[A-Za-z0-9+/=]+$`, regexp.QuoteMeta(u.Host)))
		for _, pubKey := range upstreamPublicKey {
			if rx.MatchString(pubKey) {
				opts.PublicKeys = append(opts.PublicKeys, pubKey)
			}
		}

		// Get credentials for this hostname
		if netrcData != nil {
			if machine := netrcData.FindMachine(u.Hostname()); machine != nil {
				opts.NetrcCredentials = &upstream.NetrcCredentials{
					Username: machine.Login,
					Password: machine.Password,
				}
			}
		}

		uc, err := upstream.New(ctx, u, opts)
		if err != nil {
			return nil, fmt.Errorf("error creating a new upstream cache: %w", err)
		}

		ucs = append(ucs, uc)
	}

	return ucs, nil
}

func getStorageConfig(ctx context.Context, cmd *cli.Command) (string, *s3config.Config, error) {
	deprecatedDataPath := cmd.String("cache-data-path")
	localDataPath := cmd.String("cache-storage-local")
	s3Bucket := cmd.String("cache-storage-s3-bucket")

	// Show deprecation warning if old flag is used
	if deprecatedDataPath != "" {
		zerolog.Ctx(ctx).Warn().
			Msg("--cache-data-path is deprecated, please use --cache-storage-local instead")

		if localDataPath != "" {
			zerolog.Ctx(ctx).Warn().
				Msg("Both --cache-data-path and --cache-storage-local are set; ignoring the deprecated --cache-data-path")
		} else {
			// Use deprecated value if new one is not set
			localDataPath = deprecatedDataPath
		}
	}

	if localDataPath != "" && s3Bucket != "" {
		return "", nil, ErrStorageConflict
	}

	if localDataPath == "" && s3Bucket == "" {
		return "", nil, ErrStorageConfigRequired
	}

	if localDataPath != "" {
		return localDataPath, nil, nil
	}

	s3Cfg := &s3config.Config{
		Bucket:          s3Bucket,
		Region:          cmd.String("cache-storage-s3-region"),
		Endpoint:        cmd.String("cache-storage-s3-endpoint"),
		AccessKeyID:     cmd.String("cache-storage-s3-access-key-id"),
		SecretAccessKey: cmd.String("cache-storage-s3-secret-access-key"),
		ForcePathStyle:  cmd.Bool("cache-storage-s3-force-path-style"),
	}

	if s3Cfg.Endpoint == "" || s3Cfg.AccessKeyID == "" || s3Cfg.SecretAccessKey == "" {
		return "", nil, ErrS3ConfigIncomplete
	}

	if err := s3config.ValidateConfig(*s3Cfg); err != nil {
		return "", nil, err
	}

	return "", s3Cfg, nil
}

//nolint:staticcheck // deprecated: migration support
func getStorageBackend(
	ctx context.Context,
	cmd *cli.Command,
) (storage.ConfigStore, storage.NarInfoStore, storage.NarStore, error) {
	localDataPath, s3Cfg, err := getStorageConfig(ctx, cmd)
	if err != nil {
		return nil, nil, nil, err
	}

	switch {
	case localDataPath != "":
		return createLocalStorage(ctx, localDataPath)

	case s3Cfg != nil:
		return createS3Storage(ctx, *s3Cfg)

	default:
		// This should never happen because getStorageConfig returns an error if neither is set
		return nil, nil, nil, ErrStorageConfigRequired
	}
}

//nolint:staticcheck // deprecated: migration support
func createLocalStorage(
	ctx context.Context,
	dataPath string,
) (storage.ConfigStore, storage.NarInfoStore, storage.NarStore, error) {
	localStore, err := localstorage.New(ctx, dataPath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("error creating a new local store at %q: %w", dataPath, err)
	}

	zerolog.Ctx(ctx).Info().Str("path", dataPath).Msg("using local storage")

	return localStore, localStore, localStore, nil
}

//nolint:staticcheck // deprecated: migration support
func createS3Storage(
	ctx context.Context,
	s3Cfg s3config.Config,
) (storage.ConfigStore, storage.NarInfoStore, storage.NarStore, error) {
	ctx = zerolog.Ctx(ctx).
		With().
		Str("bucket", s3Cfg.Bucket).
		Str("endpoint", s3Cfg.Endpoint).
		Bool("force_path_style", s3Cfg.ForcePathStyle).
		Logger().
		WithContext(ctx)

	zerolog.Ctx(ctx).Debug().Msg("creating S3 storage")

	s3Store, err := storageS3.New(ctx, s3Cfg)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("error creating a new S3 store: %w", err)
	}

	zerolog.Ctx(ctx).Info().Msg("using S3 storage")

	return s3Store, s3Store, s3Store, nil
}

func createDatabaseQuerier(cmd *cli.Command) (database.Querier, error) {
	dbURL := cmd.String("cache-database-url")

	// Build pool configuration from flags
	var poolCfg *database.PoolConfig

	maxOpen := cmd.Int("cache-database-pool-max-open-conns")

	maxIdle := cmd.Int("cache-database-pool-max-idle-conns")
	if maxOpen > 0 || maxIdle > 0 {
		poolCfg = &database.PoolConfig{
			MaxOpenConns: maxOpen,
			MaxIdleConns: maxIdle,
		}
	}

	db, err := database.Open(dbURL, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("error opening the database %q: %w", dbURL, err)
	}

	return db, nil
}

func getChunkStorageBackend(ctx context.Context, cmd *cli.Command, locker lock.Locker) (chunk.Store, error) {
	localDataPath, s3Cfg, err := getStorageConfig(ctx, cmd)
	if err != nil {
		return nil, err
	}

	switch {
	case localDataPath != "":
		// Use {localDataPath}/store as base for chunks to match other stores
		return chunk.NewLocalStore(filepath.Join(localDataPath, "store"))
	case s3Cfg != nil:
		return chunk.NewS3Store(ctx, *s3Cfg, locker)
	default:
		// This should never happen because getStorageConfig returns an error if neither is set
		return nil, ErrStorageConfigRequired
	}
}

func createCache(
	ctx context.Context,
	cmd *cli.Command,
	db database.Querier,
	locker lock.Locker,
	rwLocker lock.RWLocker,
	ucs []*upstream.Cache,
) (*cache.Cache, error) {
	configStore, narInfoStore, narStore, err := getStorageBackend(ctx, cmd)
	if err != nil {
		return nil, err
	}

	c, err := cache.New(
		ctx,
		cmd.String("cache-hostname"),
		db,
		configStore,
		narInfoStore,
		narStore,
		cmd.String("cache-secret-key-path"),
		locker,
		rwLocker,
		cmd.Duration("cache-lock-download-ttl"),
		cmd.Duration("cache-lock-lru-ttl"),
	)
	if err != nil {
		return nil, fmt.Errorf("error creating a new cache: %w", err)
	}

	c.SetTempDir(cmd.String("cache-temp-path"))
	c.SetCacheSignNarinfo(cmd.Bool("cache-sign-narinfo"))

	// Configure CDC
	cdcEnabled := cmd.Bool("cache-cdc-enabled")
	if err := c.SetCDCConfiguration(
		cdcEnabled,
		//nolint:gosec // G115: CLI flags for chunk sizes are positive
		uint32(cmd.Int("cache-cdc-min")),
		//nolint:gosec // G115: CLI flags for chunk sizes are positive
		uint32(cmd.Int("cache-cdc-avg")),
		//nolint:gosec // G115: CLI flags for chunk sizes are positive
		uint32(cmd.Int("cache-cdc-max")),
	); err != nil {
		return nil, fmt.Errorf("error configuring CDC: %w", err)
	}

	// Configure Chunk Store
	if cdcEnabled {
		chunkStore, err := getChunkStorageBackend(ctx, cmd, locker)
		if err != nil {
			return nil, fmt.Errorf("error creating chunk storage backend: %w", err)
		}

		c.SetChunkStore(chunkStore)
	}

	c.AddUpstreamCaches(ctx, ucs...)

	// Trigger the health-checker to speed-up the boot but do not wait for the check to complete.
	c.GetHealthChecker().Trigger()

	if cmd.String("cache-lru-schedule") == "" {
		return c, nil
	}

	maxSizeStr := cmd.String("cache-max-size")
	if maxSizeStr == "" {
		return nil, ErrCacheMaxSizeRequired
	}

	maxSize, err := helper.ParseSize(maxSizeStr)
	if err != nil {
		return nil, fmt.Errorf("error parsing the size: %w", err)
	}

	zerolog.Ctx(ctx).
		Info().
		Uint64("max-size", maxSize).
		Msg("setting up the cache max-size")

	c.SetMaxSize(maxSize)

	var loc *time.Location

	if cronTimezone := cmd.String("cache-lru-schedule-timezone"); cronTimezone != "" {
		loc, err = time.LoadLocation(cronTimezone)
		if err != nil {
			return nil, fmt.Errorf("error parsing the timezone %q: %w", cronTimezone, err)
		}
	}

	zerolog.Ctx(ctx).
		Info().
		Str("time_zone", loc.String()).
		Msg("setting up the cache timezone location")

	c.SetupCron(ctx, loc)

	schedule, err := cron.ParseStandard(cmd.String("cache-lru-schedule"))
	if err != nil {
		return nil, fmt.Errorf("error parsing the cron spec %q: %w", cmd.String("cache-lru-schedule"), err)
	}

	c.AddLRUCronJob(ctx, schedule)

	c.StartCron(ctx)

	return c, nil
}

func detectExtraResourceAttrs(
	ctx context.Context,
	cmd *cli.Command,
	db database.Querier,
	rwLocker lock.RWLocker,
) ([]attribute.KeyValue, error) {
	var attrs []attribute.KeyValue

	// 1. Identify Database Type
	dbURL := cmd.String("cache-database-url")

	dbType, err := database.DetectFromDatabaseURL(dbURL)
	if err != nil {
		return nil, fmt.Errorf("error detecting the database type: %w", err)
	}

	attrs = append(attrs, attribute.String("ncps.db_type", dbType.String()))

	// 2. Identify Lock Type
	// Use shared helper to determine effective backend (including backward compatibility)
	backend, _ := determineEffectiveLockBackend(cmd)

	attrs = append(attrs, attribute.String("ncps.lock_type", backend))

	// 3. Set the cluster UUID
	clusterUUID, err := getOrSetClusterUUID(ctx, db, rwLocker)
	if err != nil {
		return nil, err
	}

	attrs = append(attrs, attribute.String("ncps.cluster_uuid", clusterUUID))

	return attrs, nil
}

func getOrSetClusterUUID(ctx context.Context, db database.Querier, rwLocker lock.RWLocker) (string, error) {
	c := config.New(db, rwLocker)

	cu, err := c.GetClusterUUID(ctx)
	if err != nil {
		if errors.Is(err, config.ErrConfigNotFound) {
			return setClusterUUID(ctx, c)
		}

		return "", err
	}

	return cu, nil
}

func setClusterUUID(ctx context.Context, c *config.Config) (string, error) {
	cu := uuid.New().String()
	if err := c.SetClusterUUID(ctx, cu); err != nil {
		return "",
			fmt.Errorf("error setting the new cluster UUID: %w", err)
	}

	return cu, nil
}

// determineEffectiveLockBackend determines the effective lock backend to use
// based on the configured flags and backward compatibility logic.
// Returns the backend name and the list of valid Redis addresses.
func determineEffectiveLockBackend(cmd *cli.Command) (backend string, validRedisAddrs []string) {
	backend = cmd.String("cache-lock-backend")

	// Check for legacy Redis configuration (backward compatibility)
	redisAddrs := cmd.StringSlice("cache-redis-addrs")

	// Filter out empty addresses
	for _, addr := range redisAddrs {
		if addr != "" {
			validRedisAddrs = append(validRedisAddrs, addr)
		}
	}

	// If Redis addresses are set but backend is not explicitly specified, use Redis
	// This maintains backward compatibility with existing deployments
	if len(validRedisAddrs) > 0 && backend == lockBackendLocal {
		backend = lockBackendRedis
	}

	return backend, validRedisAddrs
}

func getLockers(
	ctx context.Context,
	cmd *cli.Command,
	db database.Querier,
) (
	locker lock.Locker,
	rwLocker lock.RWLocker,
	err error,
) {
	allowDegradedMode := cmd.Bool("cache-lock-allow-degraded-mode")

	// Build retry configuration (common to all distributed backends)
	retryCfg := lock.RetryConfig{
		MaxAttempts:  cmd.Int("cache-lock-retry-max-attempts"),
		InitialDelay: cmd.Duration("cache-lock-retry-initial-delay"),
		MaxDelay:     cmd.Duration("cache-lock-retry-max-delay"),
		Jitter:       cmd.Bool("cache-lock-retry-jitter"),
	}

	// Determine effective lock backend (including backward compatibility)
	backend, validRedisAddrs := determineEffectiveLockBackend(cmd)

	// Log deprecation warning if backward compatibility was triggered
	if len(validRedisAddrs) > 0 && cmd.String("cache-lock-backend") == lockBackendLocal {
		zerolog.Ctx(ctx).Warn().
			Msg("--cache-redis-addrs is set but --cache-lock-backend is 'local'. " +
				"Please explicitly set --cache-lock-backend=redis. " +
				"Defaulting to Redis for backward compatibility.")
	}

	switch backend {
	case lockBackendRedis:
		// Validate that Redis addresses are set
		if len(validRedisAddrs) == 0 {
			return nil, nil, ErrRedisAddrsRequired
		}

		// Redis configured - use distributed locks
		redisCfg := redis.Config{
			Addrs:     validRedisAddrs,
			Username:  cmd.String("cache-redis-username"),
			Password:  cmd.String("cache-redis-password"),
			DB:        cmd.Int("cache-redis-db"),
			UseTLS:    cmd.Bool("cache-redis-use-tls"),
			PoolSize:  cmd.Int("cache-redis-pool-size"),
			KeyPrefix: cmd.String("cache-lock-redis-key-prefix"),
		}

		locker, err = redis.NewLocker(ctx, redisCfg, retryCfg, allowDegradedMode)
		if err != nil {
			return nil, nil, fmt.Errorf("error creating Redis locker: %w", err)
		}

		rwLocker, err = redis.NewRWLocker(ctx, redisCfg, retryCfg, allowDegradedMode)
		if err != nil {
			return nil, nil, fmt.Errorf("error creating Redis RW locker: %w", err)
		}

		zerolog.Ctx(ctx).Info().
			Strs("addrs", redisCfg.Addrs).
			Msg("distributed locking enabled with Redis")

	case lockBackendPostgres:
		// PostgreSQL advisory locks - use database connection
		pgCfg := postgres.Config{
			KeyPrefix: cmd.String("cache-lock-postgres-key-prefix"),
		}

		locker, err = postgres.NewLocker(ctx, db, pgCfg, retryCfg, allowDegradedMode)
		if err != nil {
			return nil, nil, fmt.Errorf("error creating PostgreSQL advisory lock locker: %w", err)
		}

		rwLocker, err = postgres.NewRWLocker(ctx, db, pgCfg, retryCfg, allowDegradedMode)
		if err != nil {
			return nil, nil, fmt.Errorf("error creating PostgreSQL advisory lock RW locker: %w", err)
		}

		zerolog.Ctx(ctx).Info().
			Msg("distributed locking enabled with PostgreSQL advisory locks")

	case lockBackendLocal:
		// No distributed backend - use local locks (single-instance mode)
		locker = local.NewLocker()
		rwLocker = local.NewRWLocker()

		zerolog.Ctx(ctx).
			Info().
			Msg("using local locks (single-instance mode)")

	default:
		return nil, nil, fmt.Errorf("%w: %s (must be 'local', 'redis', or 'postgres')",
			ErrUnknownLockBackend, backend)
	}

	return locker, rwLocker, nil
}

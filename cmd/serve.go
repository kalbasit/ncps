package cmd

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
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog"
	"github.com/sysbot/go-netrc"
	"github.com/urfave/cli/v3"
	"golang.org/x/sync/errgroup"

	localstorage "github.com/kalbasit/ncps/pkg/storage/local"

	"github.com/kalbasit/ncps/pkg/cache"
	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/helper"
	"github.com/kalbasit/ncps/pkg/lock"
	"github.com/kalbasit/ncps/pkg/lock/local"
	"github.com/kalbasit/ncps/pkg/lock/redis"
	"github.com/kalbasit/ncps/pkg/prometheus"
	"github.com/kalbasit/ncps/pkg/server"
	"github.com/kalbasit/ncps/pkg/storage"
	"github.com/kalbasit/ncps/pkg/storage/s3"
)

var (
	// ErrCacheMaxSizeRequired is returned if --cache-lru-schedule was given but not --cache-max-size.
	ErrCacheMaxSizeRequired = errors.New("--cache-max-size is required when --cache-lru-schedule is specified")

	// ErrStorageConfigRequired is returned if neither local nor S3 storage is configured.
	ErrStorageConfigRequired = errors.New("either --cache-storage-local or --cache-storage-s3-bucket is required")

	ErrS3ConfigIncomplete = errors.New(
		"S3 requires --cache-storage-s3-endpoint, --cache-storage-s3-access-key-id, and --cache-storage-s3-secret-access-key",
	)

	// ErrS3EndpointMissingScheme is returned if the S3 endpoint does not include a scheme.
	ErrS3EndpointMissingScheme = errors.New("S3 endpoint must include scheme (http:// or https://)")

	// ErrStorageConflict is returned if both local and S3 storage are configured.
	ErrStorageConflict = errors.New("cannot use both --cache-storage-local and --cache-storage-s3-bucket")

	// ErrUpstreamCacheRequired is returned if no upstream cache is configured.
	ErrUpstreamCacheRequired = errors.New("at least one --cache-upstream-url is required")
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

func serveCommand(userDirs userDirectories, flagSources flagSourcesFn) *cli.Command {
	return &cli.Command{
		Name:    "serve",
		Aliases: []string{"s"},
		Usage:   "serve the nix binary cache over http",
		Action:  serveAction(),
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
				Name:    "cache-secret-key-path",
				Usage:   "The path to the secret key used for signing cached paths",
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
				Name:    "cache-redis-key-prefix",
				Usage:   "Prefix for all Redis lock keys",
				Sources: flagSources("cache.redis.key-prefix", "CACHE_REDIS_KEY_PREFIX"),
				Value:   "ncps:lock:",
			},

			// Lock Configuration
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

func serveAction() cli.ActionFunc {
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
			return autoMaxProcs(ctx, 30*time.Second, logger)
		})

		netrcData, err := parseNetrcFile(cmd.String("netrc-file"))
		if err != nil {
			logger.Warn().Err(err).Msg("failed to parse netrc file, proceeding without netrc authentication")
		}

		ucs, err := getUpstreamCaches(ctx, cmd, netrcData)
		if err != nil {
			return fmt.Errorf("error computing the upstream caches: %w", err)
		}

		cache, err := createCache(ctx, cmd, ucs)
		if err != nil {
			return err
		}

		srv := server.New(cache)
		srv.SetDeletePermitted(cmd.Bool("cache-allow-delete-verb"))
		srv.SetPutPermitted(cmd.Bool("cache-allow-put-verb"))

		// Setup Prometheus metrics if enabled
		var prometheusShutdown func(context.Context) error

		if cmd.Root().Bool("prometheus-enabled") {
			gatherer, shutdown, err := prometheus.SetupPrometheusMetrics(ctx, cmd.Root().Name, Version)
			if err != nil {
				return fmt.Errorf("error setting up Prometheus metrics: %w", err)
			}

			prometheusShutdown = shutdown

			srv.SetPrometheusGatherer(gatherer)

			logger.Info().Msg("Prometheus metrics enabled at /metrics")
		}

		// Cleanup prometheus if needed
		defer func() {
			if prometheusShutdown != nil {
				if err := prometheusShutdown(ctx); err != nil {
					logger.Error().Err(err).Msg("error shutting down Prometheus metrics")
				}
			}
		}()

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

func getStorageBackend(
	ctx context.Context,
	cmd *cli.Command,
) (storage.ConfigStore, storage.NarInfoStore, storage.NarStore, error) {
	// Handle backward compatibility for cache-data-path (deprecated)
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

	switch {
	case localDataPath != "" && s3Bucket != "":
		return nil, nil, nil, ErrStorageConflict

	case localDataPath != "":
		return createLocalStorage(ctx, localDataPath)

	case s3Bucket != "":
		return createS3Storage(ctx, cmd)

	default:
		return nil, nil, nil, ErrStorageConfigRequired
	}
}

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

func createS3Storage(
	ctx context.Context,
	cmd *cli.Command,
) (storage.ConfigStore, storage.NarInfoStore, storage.NarStore, error) {
	s3Bucket := cmd.String("cache-storage-s3-bucket")
	s3Endpoint := cmd.String("cache-storage-s3-endpoint")
	s3AccessKeyID := cmd.String("cache-storage-s3-access-key-id")
	s3SecretAccessKey := cmd.String("cache-storage-s3-secret-access-key")
	s3ForcePathStyle := cmd.Bool("cache-storage-s3-force-path-style")

	if s3Endpoint == "" || s3AccessKeyID == "" || s3SecretAccessKey == "" {
		return nil, nil, nil, ErrS3ConfigIncomplete
	}

	// Ensure endpoint has a scheme
	if !strings.HasPrefix(s3Endpoint, "http://") && !strings.HasPrefix(s3Endpoint, "https://") {
		return nil, nil, nil, fmt.Errorf("%w: %s", ErrS3EndpointMissingScheme, s3Endpoint)
	}

	ctx = zerolog.Ctx(ctx).
		With().
		Str("bucket", s3Bucket).
		Str("endpoint", s3Endpoint).
		Bool("force_path_style", s3ForcePathStyle).
		Logger().
		WithContext(ctx)

	zerolog.Ctx(ctx).Debug().Msg("creating S3 storage")

	s3Cfg := s3.Config{
		Bucket:          s3Bucket,
		Region:          cmd.String("cache-storage-s3-region"),
		Endpoint:        s3Endpoint,
		AccessKeyID:     s3AccessKeyID,
		SecretAccessKey: s3SecretAccessKey,
		ForcePathStyle:  s3ForcePathStyle,
	}

	s3Store, err := s3.New(ctx, s3Cfg)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("error creating a new S3 store: %w", err)
	}

	zerolog.Ctx(ctx).Info().Msg("using S3 storage")

	return s3Store, s3Store, s3Store, nil
}

func createCache(
	ctx context.Context,
	cmd *cli.Command,
	ucs []*upstream.Cache,
) (*cache.Cache, error) {
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

	configStore, narInfoStore, narStore, err := getStorageBackend(ctx, cmd)
	if err != nil {
		return nil, err
	}

	// Initialize distributed or local locks based on Redis configuration
	var (
		downloadLocker lock.Locker
		lruLocker      lock.RWLocker
	)

	redisAddrs := cmd.StringSlice("cache-redis-addrs")
	// Filter out empty addresses
	var validRedisAddrs []string

	for _, addr := range redisAddrs {
		if addr != "" {
			validRedisAddrs = append(validRedisAddrs, addr)
		}
	}

	if len(validRedisAddrs) > 0 {
		// Redis configured - use distributed locks
		redisCfg := redis.Config{
			Addrs:     validRedisAddrs,
			Username:  cmd.String("cache-redis-username"),
			Password:  cmd.String("cache-redis-password"),
			DB:        cmd.Int("cache-redis-db"),
			UseTLS:    cmd.Bool("cache-redis-use-tls"),
			PoolSize:  cmd.Int("cache-redis-pool-size"),
			KeyPrefix: cmd.String("cache-redis-key-prefix"),
		}

		retryCfg := redis.RetryConfig{
			MaxAttempts:  cmd.Int("cache-lock-retry-max-attempts"),
			InitialDelay: cmd.Duration("cache-lock-retry-initial-delay"),
			MaxDelay:     cmd.Duration("cache-lock-retry-max-delay"),
			Jitter:       cmd.Bool("cache-lock-retry-jitter"),
		}

		allowDegradedMode := cmd.Bool("cache-lock-allow-degraded-mode")

		downloadLocker, err = redis.NewLocker(ctx, redisCfg, retryCfg, allowDegradedMode)
		if err != nil {
			return nil, fmt.Errorf("error creating Redis download locker: %w", err)
		}

		lruLocker, err = redis.NewRWLocker(ctx, redisCfg, retryCfg, allowDegradedMode)
		if err != nil {
			return nil, fmt.Errorf("error creating Redis LRU locker: %w", err)
		}

		zerolog.Ctx(ctx).Info().
			Strs("addrs", redisCfg.Addrs).
			Msg("distributed locking enabled with Redis")
	} else {
		// No Redis - use local locks (single-instance mode)
		downloadLocker = local.NewLocker()
		lruLocker = local.NewRWLocker()

		zerolog.Ctx(ctx).Info().Msg("using local locks (single-instance mode)")
	}

	c, err := cache.New(
		ctx,
		cmd.String("cache-hostname"),
		db,
		configStore,
		narInfoStore,
		narStore,
		cmd.String("cache-secret-key-path"),
		downloadLocker,
		lruLocker,
		cmd.Duration("cache-lock-download-ttl"),
		cmd.Duration("cache-lock-lru-ttl"),
	)
	if err != nil {
		return nil, fmt.Errorf("error creating a new cache: %w", err)
	}

	c.SetTempDir(cmd.String("cache-temp-path"))
	c.SetCacheSignNarinfo(cmd.Bool("cache-sign-narinfo"))
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

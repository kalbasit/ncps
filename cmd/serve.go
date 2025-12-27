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

	"github.com/kalbasit/ncps/pkg/cache"
	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/helper"
	"github.com/kalbasit/ncps/pkg/prometheus"
	"github.com/kalbasit/ncps/pkg/server"
	"github.com/kalbasit/ncps/pkg/storage"
	"github.com/kalbasit/ncps/pkg/storage/local"
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

	// ErrStorageConflict is returned if both local and S3 storage are configured.
	ErrStorageConflict = errors.New("cannot use both --cache-storage-local and --cache-storage-s3-bucket")
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
				Name: "cache-data-path",
				//nolint:lll
				Usage:   "DEPRECATED: Use --cache-storage-local instead. The local data path used for configuration and cache storage",
				Sources: flagSources("cache.data-path", "CACHE_DATA_PATH"),
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
				Usage:   "S3-compatible endpoint URL (e.g., s3.amazonaws.com or minio.example.com:9000)",
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
				Name:    "cache-storage-s3-use-ssl",
				Usage:   "Use SSL/TLS for S3 connection (default: true)",
				Sources: flagSources("cache.storage.s3.use-ssl", "CACHE_STORAGE_S3_USE_SSL"),
				Value:   true,
			},
			&cli.StringFlag{
				Name:     "cache-database-url",
				Usage:    "The URL of the database",
				Sources:  flagSources("cache.database-url", "CACHE_DATABASE_URL"),
				Required: true,
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
			&cli.StringSliceFlag{
				Name:     "upstream-cache",
				Usage:    "Set to URL (with scheme) for each upstream cache",
				Sources:  flagSources("cache.upstream.caches", "UPSTREAM_CACHES"),
				Required: true,
			},
			&cli.StringSliceFlag{
				Name:    "upstream-public-key",
				Usage:   "Set to host:public-key for each upstream cache",
				Sources: flagSources("cache.upstream.public-keys", "UPSTREAM_PUBLIC_KEYS"),
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
	ucSlice := cmd.StringSlice("upstream-cache")

	ucs := make([]*upstream.Cache, 0, len(ucSlice))

	for _, us := range ucSlice {
		var pubKeys []string

		u, err := url.Parse(us)
		if err != nil {
			return nil, fmt.Errorf("error parsing --upstream-cache=%q: %w", us, err)
		}

		rx := regexp.MustCompile(fmt.Sprintf(`^%s-[0-9]+:[A-Za-z0-9+/=]+$`, regexp.QuoteMeta(u.Host)))

		for _, pubKey := range cmd.StringSlice("upstream-public-key") {
			if rx.MatchString(pubKey) {
				pubKeys = append(pubKeys, pubKey)
			}
		}

		// Get credentials for this hostname
		var creds *upstream.NetrcCredentials

		if netrcData != nil {
			if machine := netrcData.FindMachine(u.Hostname()); machine != nil {
				creds = &upstream.NetrcCredentials{
					Username: machine.Login,
					Password: machine.Password,
				}
			}
		}

		uc, err := upstream.New(ctx, u, pubKeys, creds)
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
	localStore, err := local.New(ctx, dataPath)
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

	if s3Endpoint == "" || s3AccessKeyID == "" || s3SecretAccessKey == "" {
		return nil, nil, nil, ErrS3ConfigIncomplete
	}

	// Determine SSL usage. The scheme in the endpoint URL (https:// or http://)
	// takes precedence over the --cache-storage-s3-use-ssl flag.
	useSSL := cmd.Bool("cache-storage-s3-use-ssl")
	if s3.IsHTTPS(s3Endpoint) {
		useSSL = true
	} else if strings.HasPrefix(s3Endpoint, "http://") {
		useSSL = false
	}

	endpoint := s3.GetEndpointWithoutScheme(s3Endpoint)

	ctx = zerolog.Ctx(ctx).
		With().
		Str("bucket", s3Bucket).
		Str("endpoint", endpoint).
		Bool("use_ssl", useSSL).
		Logger().
		WithContext(ctx)

	zerolog.Ctx(ctx).Debug().Msg("creating S3 storage")

	s3Cfg := s3.Config{
		Bucket:          s3Bucket,
		Region:          cmd.String("cache-storage-s3-region"),
		Endpoint:        endpoint,
		AccessKeyID:     s3AccessKeyID,
		SecretAccessKey: s3SecretAccessKey,
		UseSSL:          useSSL,
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

	db, err := database.Open(dbURL)
	if err != nil {
		return nil, fmt.Errorf("error opening the database %q: %w", dbURL, err)
	}

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

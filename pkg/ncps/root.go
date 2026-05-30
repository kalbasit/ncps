package ncps

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/urfave/cli-altsrc/v3/json"
	"github.com/urfave/cli-altsrc/v3/toml"
	"github.com/urfave/cli-altsrc/v3/yaml"
	"github.com/urfave/cli/v3"
	"golang.org/x/term"

	altsrc "github.com/urfave/cli-altsrc/v3"

	"github.com/kalbasit/ncps/pkg/otelzerolog"
	"github.com/kalbasit/ncps/pkg/xz"
)

var (
	// ErrXZBinAbsPath is returned when the xz binary path is not an absolute
	// path.
	ErrXZBinAbsPath = errors.New("the path to xz binary must be an absolute path")

	// ErrXZBinEmptyPath is returned when the xz binary path is empty.
	ErrXZBinEmptyPath = errors.New("--xz-binary-path cannot be empty")

	// Version defines the version of the binary, and is meant to be set with ldflags at build time.
	//
	//nolint:gochecknoglobals
	Version = "dev"
)

// Flag name and usage strings shared across multiple sub-commands.
const (
	// Flag names and default values shared across sub-commands.
	flagNameDryRun        = "dry-run"
	flagNameCacheTempPath = "cache-temp-path"
	flagNameConcurrency   = "concurrency"
	flagUsageConcurrency  = "Number of concurrent migration workers"
	flagUsageRedisAddrs   = "Redis server addresses for distributed locking " +
		"(enables coordination with running ncps instances)"
	flagDefaultLockRedisKeyPrefix = "ncps:lock:"
	flagNameStorageLocal          = "cache-storage-local"
	flagNameS3Bucket              = "cache-storage-s3-bucket"
	flagNameS3Endpoint            = "cache-storage-s3-endpoint"
	flagNameS3Region              = "cache-storage-s3-region"
	flagNameS3AccessKeyID         = "cache-storage-s3-access-key-id"
	flagNameS3SecretKey           = "cache-storage-s3-secret-access-key" //nolint:gosec // G101: flag name
	flagNameS3ForcePathStyle      = "cache-storage-s3-force-path-style"
	flagNameDBURL                 = "cache-database-url"
	flagNameDBMaxOpenConns        = "cache-database-pool-max-open-conns"
	flagNameDBMaxIdleConns        = "cache-database-pool-max-idle-conns"
	flagNameRedisAddrs            = "cache-redis-addrs"
	flagNameRedisUsername         = "cache-redis-username"
	flagNameRedisPassword         = "cache-redis-password"
	flagNameRedisDB               = "cache-redis-db"
	flagNameRedisTLS              = "cache-redis-use-tls"
	flagNameRedisPoolSize         = "cache-redis-pool-size"
	flagNameLockBackend           = "cache-lock-backend"
	flagNameLockRedisKeyPrefix    = "cache-lock-redis-key-prefix"
	flagNameLockDownloadTTL       = "cache-lock-download-ttl"
	flagNameLockLRUTTL            = "cache-lock-lru-ttl"
	flagNameLockMaxRetries        = "cache-lock-retry-max-attempts"
	flagNameLockInitialDelay      = "cache-lock-retry-initial-delay"
	flagNameLockMaxDelay          = "cache-lock-retry-max-delay"
	flagNameLockJitter            = "cache-lock-retry-jitter"
	flagNameLockAllowDegraded     = "cache-lock-allow-degraded-mode"

	// Flag usage strings.
	flagUsageStorageLocal       = "The local data path used for configuration and cache storage (use this OR S3 storage)"
	flagUsageS3Bucket           = "S3 bucket name for storage (use this OR --cache-storage-local for local storage)"
	flagUsageS3AccessKeyID      = "S3 access key ID"
	flagUsageS3Endpoint         = "S3-compatible endpoint URL with scheme"
	flagUsageS3Region           = "S3 region (optional)"
	flagUsageS3SecretKey        = "S3 secret access key"
	flagUsageS3ForcePathStyle   = "Force path-style S3 addressing"
	flagUsageDBURL              = "The URL of the database"
	flagUsageDBMaxOpenConns     = "Maximum number of open connections to the database"
	flagUsageDBMaxIdleConns     = "Maximum number of idle connections in the pool"
	flagUsageRedisUsername      = "Redis username"
	flagUsageRedisPassword      = "Redis password"
	flagUsageRedisDB            = "Redis database number"
	flagUsageRedisTLS           = "Use TLS for Redis connections"
	flagUsageLockBackend        = "Lock backend to use: 'local' (single instance) or 'redis' (distributed)"
	flagUsageLockRedisKeyPrefix = "Prefix for all Redis lock keys (only used when Redis is configured)"
	flagUsageLockDownloadTTL    = "TTL for download locks (per-hash locks)"
	flagUsageLockLRUTTL         = "TTL for LRU lock (global exclusive lock)"
	flagUsageLockAllowDegraded  = "Allow falling back to local locks if Redis is unavailable" +
		" (WARNING: breaks HA guarantees)"
	flagUsageLockMaxRetries   = "Maximum number of retry attempts for distributed locks"
	flagUsageLockMaxDelay     = "Maximum retry delay for distributed locks (exponential backoff caps at this)"
	flagUsageLockInitialDelay = "Initial retry delay for distributed locks"
	flagUsageLockJitter       = "Enable jitter in retry delays to prevent thundering herd"
	flagUsageRedisPoolSize    = "Redis connection pool size"
)

type flagSourcesFn func(configFileKey, envVar string) cli.ValueSourceChain

type registerShutdownFn func(name string, sfn shutdownFn)

type userDirectories struct {
	configDir string
	homeDir   string
}

type shutdownFn func(context.Context) error

func New() (*cli.Command, error) {
	var (
		configPath  string
		shutdownFns = make(map[string]shutdownFn)
	)

	flagSources := func(configFileKey, envVar string) cli.ValueSourceChain {
		return cli.NewValueSourceChain(
			toml.TOML(configFileKey, altsrc.NewStringPtrSourcer(&configPath)),
			yaml.YAML(configFileKey, altsrc.NewStringPtrSourcer(&configPath)),
			json.JSON(configFileKey, altsrc.NewStringPtrSourcer(&configPath)),
			cli.EnvVar(envVar),
		)
	}

	registerShutdown := func(name string, sfn shutdownFn) { shutdownFns[name] = sfn }

	userDirs, err := getUserDirs()
	if err != nil {
		return nil, err
	}

	c := &cli.Command{
		Name:    "ncps",
		Usage:   "Nix Binary Cache Proxy Service",
		Version: Version,
		After: func(ctx context.Context, _ *cli.Command) error {
			var wg sync.WaitGroup

			for name, sfn := range shutdownFns {
				if sfn != nil {
					wg.Go(func() {
						if err := sfn(ctx); err != nil {
							zerolog.Ctx(ctx).
								Error().
								Err(err).
								Str("shutdown name", name).
								Msg("error calling the shutting down function")
						}
					})
				}
			}

			wg.Wait()

			return nil
		},
		Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			var err error

			ctx, err = getZeroLogger(ctx, cmd)
			if err != nil {
				return ctx, err
			}

			if cmd.Bool("use-xz-binary") {
				p := cmd.String("xz-binary-path")
				if p == "" {
					// If use-xz-binary is true, and path is empty, it means xz was not found in PATH.
					return ctx, fmt.Errorf("%w: xz binary not found in PATH or --xz-binary-path not set", ErrXZBinEmptyPath)
				}

				zerolog.Ctx(ctx).
					Info().
					Str("xz-binary-path", p).
					Msg("Using xz binary for xz decompression")

				xz.UseXZBinary(p)
			} else {
				zerolog.Ctx(ctx).
					Info().
					Msg("Using internal Go-native library for xz decompression")

				xz.UseInternal()
			}

			return ctx, nil
		},
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "analytics-reporting-enabled",
				Usage:   "Enable reporting anonymous usage statistics (DB type, Lock type, Total Size) to the project maintainers",
				Sources: flagSources("analytics.reporting.enabled", "ANALYTICS_REPORTING_ENABLED"),
				Value:   true,
			},
			&cli.BoolFlag{
				Name: "analytics-reporting-samples",
				//nolint:lll
				Usage: "Enable printing the analytics samples to stdout. This is useful for debugging and verification purposes only.",
				Value: false,
			},
			&cli.StringFlag{
				Name:    "log-level",
				Usage:   "Set the log level",
				Sources: flagSources("log.level", "LOG_LEVEL"),
				Value:   "info",
				Validator: func(lvl string) error {
					_, err := zerolog.ParseLevel(lvl)

					return err
				},
			},
			&cli.BoolFlag{
				Name:  "log-console-writer-enabled",
				Usage: "Enable console writer for zerolog. This is useful when running in terminal.",
				Value: term.IsTerminal(int(os.Stdout.Fd())),
			},
			&cli.StringFlag{
				Name: "log-console-writer-prefix",
				//nolint:lll
				Usage: "Prefix for console writer for zerolog. This is useful when running multiple ncps instances in the same terminal.",
				Value: "",
			},
			&cli.BoolFlag{
				Name:    "otel-enabled",
				Usage:   "Enable Open-Telemetry logs, metrics and tracing.",
				Sources: flagSources("opentelemetry.enabled", "OTEL_ENABLED"),
			},
			&cli.StringFlag{
				Name: "otel-grpc-url",
				Usage: "Configure OpenTelemetry gRPC URL; Missing or https " +
					"scheme enable secure gRPC, insecure otherwize. Omit to emit Telemetry to stdout.",
				Sources: flagSources("opentelemetry.grpc-url", "OTEL_GRPC_URL"),
				Value:   "",
				Validator: func(colURL string) error {
					_, err := url.Parse(colURL)

					return err
				},
			},
			&cli.StringFlag{
				Name:        "config",
				Usage:       "Path to the configuration file (json, toml, yaml)",
				Sources:     cli.EnvVars("NCPS_CONFIG_FILE"),
				Value:       filepath.Join(userDirs.configDir, "ncps", "config.yaml"),
				Destination: &configPath,
			},
			&cli.BoolFlag{
				Name:    "prometheus-enabled",
				Usage:   "Enable Prometheus metrics endpoint at /metrics",
				Sources: flagSources("prometheus.enabled", "PROMETHEUS_ENABLED"),
			},
			&cli.StringFlag{
				Name:    "xz-binary-path",
				Usage:   "Absolute Path to the xz binary",
				Sources: flagSources("xz-binary-path", "XZ_BINARY_PATH"),
				Value: func() string {
					p, err := exec.LookPath("xz")
					if err != nil {
						return ""
					}

					return p
				}(),
				Validator: func(p string) error {
					if p != "" && !filepath.IsAbs(p) {
						return ErrXZBinAbsPath
					}

					return nil
				},
			},
			&cli.BoolFlag{
				Name:    "use-xz-binary",
				Usage:   "Use the xz binary instead of the Go implementation",
				Sources: flagSources("use-xz-binary", "USE_XZ_BINARY"),
				Value:   true,
			},
		},
		Commands: []*cli.Command{
			serveCommand(userDirs, flagSources, registerShutdown),
			migrateCommand(flagSources),
			migrateNarInfoCommand(flagSources, registerShutdown),
			migrateNarToChunksCommand(flagSources, registerShutdown),
			migrateChunksToNarCommand(flagSources, registerShutdown),
			fsckCommand(flagSources, registerShutdown),
		},
	}

	return c, nil
}

func getZeroLogger(ctx context.Context, cmd *cli.Command) (context.Context, error) {
	logLvl := cmd.String("log-level")

	lvl, err := zerolog.ParseLevel(logLvl)
	if err != nil {
		return ctx, fmt.Errorf("error parsing the log-level %q: %w", logLvl, err)
	}

	var output io.Writer = os.Stdout

	if cmd.Bool("log-console-writer-enabled") {
		writer := zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}
		if prefix := cmd.String("log-console-writer-prefix"); prefix != "" {
			writer.FormatTimestamp = func(i any) string {
				return fmt.Sprintf("[%s] %s", prefix, i)
			}
		}

		output = writer
	}

	// Internally this calls global.GetLoggerProvider() which returns the
	// logger once and that logger is updated in place anytime it gets updated
	// (with global.SetLoggerProvider) so no need to re-create this logger if
	// the otel logger was ever updated. In our case, we create the logger
	// early (see Before above) once and it will just work due to this
	// behavior.
	otelWriter, err := otelzerolog.NewOtelWriter(nil)
	if err != nil {
		return ctx, err
	}

	output = zerolog.MultiLevelWriter(output, otelWriter)

	logger := zerolog.New(output).
		Level(lvl).
		With().
		Timestamp().
		Logger()

	logger.
		Info().
		Str("log_level", lvl.String()).
		Msg("logger created")

	return logger.WithContext(ctx), nil
}

func getUserDirs() (userDirectories, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return userDirectories{}, fmt.Errorf("unable to determine user config directory: %w", err)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return userDirectories{}, fmt.Errorf("unable to determine user home directory: %w", err)
	}

	return userDirectories{
		configDir: configDir,
		homeDir:   homeDir,
	}, nil
}

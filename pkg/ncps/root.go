package ncps

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
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
)

// Version defines the version of the binary, and is meant to be set with ldflags at build time.
//
//nolint:gochecknoglobals
var Version = "dev"

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
		},
		Commands: []*cli.Command{
			serveCommand(userDirs, flagSources, registerShutdown),
			migrateNarInfoCommand(flagSources, registerShutdown),
			migrateNarToChunksCommand(flagSources, registerShutdown),
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

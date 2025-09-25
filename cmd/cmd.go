package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/rs/zerolog"
	"github.com/urfave/cli-altsrc/v3/json"
	"github.com/urfave/cli-altsrc/v3/toml"
	"github.com/urfave/cli-altsrc/v3/yaml"
	"github.com/urfave/cli/v3"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutlog"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	"golang.org/x/sync/errgroup"
	"golang.org/x/term"

	altsrc "github.com/urfave/cli-altsrc/v3"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/kalbasit/ncps/pkg/otelzerolog"
	"github.com/kalbasit/ncps/pkg/telemetry"
)

// Version defines the version of the binary, and is meant to be set with ldflags at build time.
//
//nolint:gochecknoglobals
var Version = "dev"

type flagSourcesFn func(configFileKey, envVar string) cli.ValueSourceChain

func New() *cli.Command {
	var otelShutdown func(context.Context) error

	var configPath string

	flagSources := func(configFileKey, envVar string) cli.ValueSourceChain {
		return cli.NewValueSourceChain(
			toml.TOML(configFileKey, altsrc.NewStringPtrSourcer(&configPath)),
			yaml.YAML(configFileKey, altsrc.NewStringPtrSourcer(&configPath)),
			json.JSON(configFileKey, altsrc.NewStringPtrSourcer(&configPath)),
			cli.EnvVar(envVar),
		)
	}

	return &cli.Command{
		Name:    "ncps",
		Usage:   "Nix Binary Cache Proxy Service",
		Version: Version,
		After: func(ctx context.Context, _ *cli.Command) error {
			if otelShutdown != nil {
				return otelShutdown(ctx)
			}

			return nil
		},
		Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			var err error
			otelShutdown, err = setupOTelSDK(ctx, cmd)
			if err != nil {
				return ctx, err
			}

			logLvl := cmd.String("log-level")

			lvl, err := zerolog.ParseLevel(logLvl)
			if err != nil {
				return ctx, fmt.Errorf("error parsing the log-level %q: %w", logLvl, err)
			}

			var output io.Writer = os.Stdout

			colURL := cmd.String("otel-grpc-url")
			if colURL != "" {
				otelWriter, err := otelzerolog.NewOtelWriter(nil)
				if err != nil {
					return ctx, err
				}

				output = zerolog.MultiLevelWriter(os.Stdout, otelWriter)
			}

			if term.IsTerminal(int(os.Stdout.Fd())) {
				output = zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}
			}

			ctx = zerolog.New(output).
				Level(lvl).
				With().
				Timestamp().
				Logger().
				WithContext(ctx)

			(zerolog.Ctx(ctx)).
				Info().
				Str("otel_grpc_url", colURL).
				Str("log_level", lvl.String()).
				Msg("logger created")

			return ctx, nil
		},
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "otel-enabled",
				Usage:   "Enable Open-Telemetry logs, metrics and tracing.",
				Sources: flagSources("opentelemetry.enabled", "OTEL_ENABLED"),
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
				Usage:       "Path to the configuration file (toml, yaml, json)",
				Sources:     cli.EnvVars("NCPS_CONFIG_FILE"),
				Value:       getDefaultConfigPath(),
				Destination: &configPath,
			},
			&cli.BoolFlag{
				Name:    "prometheus-enabled",
				Usage:   "Enable Prometheus metrics endpoint at /metrics",
				Sources: flagSources("prometheus.enabled", "PROMETHEUS_ENABLED"),
			},
		},
		Commands: []*cli.Command{
			serveCommand(flagSources),
		},
	}
}

// getDefaultConfigPath returns the default path to the config file.
func getDefaultConfigPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		panic(fmt.Sprintf("unable to determine user config directory: %v", err))
	}

	return filepath.Join(configDir, "ncps", "config.yaml")
}

// setupOTelSDK bootstraps the OpenTelemetry pipeline.
// If it does not return an error, make sure to call shutdown for proper cleanup.
func setupOTelSDK(ctx context.Context, cmd *cli.Command) (func(context.Context) error, error) {
	var shutdownFuncs []func(context.Context) error

	// shutdown calls cleanup functions registered via shutdownFuncs.
	// The errors from the calls are joined.
	// Each registered cleanup will be invoked once.
	shutdown := func(ctx context.Context) error {
		defer func() {
			shutdownFuncs = nil
		}()

		g, ctx := errgroup.WithContext(ctx)

		for _, fn := range shutdownFuncs {
			g.Go(func() error {
				return fn(ctx)
			})
		}

		return g.Wait()
	}

	// handleErr calls shutdown for cleanup and makes sure that all errors are returned.
	handleErr := func(inErr error) error {
		return errors.Join(inErr, shutdown(ctx))
	}

	// Set up propagator.
	prop := newPropagator()
	otel.SetTextMapPropagator(prop)

	res, err := telemetry.NewResource(ctx, cmd.Root().Name, Version)
	if err != nil {
		return shutdown, handleErr(err)
	}

	colURL := cmd.String("otel-grpc-url")
	enabled := cmd.Bool("otel-enabled")

	// Set up trace provider.
	tracerProvider, err := newTraceProvider(ctx, enabled, colURL, res)
	if err != nil {
		return shutdown, handleErr(err)
	}

	shutdownFuncs = append(shutdownFuncs, tracerProvider.Shutdown)
	otel.SetTracerProvider(tracerProvider)

	// Set up meter provider.
	meterProvider, err := newMeterProvider(ctx, enabled, colURL, res)
	if err != nil {
		return shutdown, handleErr(err)
	}

	shutdownFuncs = append(shutdownFuncs, meterProvider.Shutdown)
	otel.SetMeterProvider(meterProvider)

	// Set up logger provider.
	loggerProvider, err := newLoggerProvider(ctx, enabled, colURL, res)
	if err != nil {
		return shutdown, handleErr(err)
	}

	shutdownFuncs = append(shutdownFuncs, loggerProvider.Shutdown)
	global.SetLoggerProvider(loggerProvider)

	return shutdown, nil
}

func newPropagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
}

func newTraceProvider(
	ctx context.Context,
	enabled bool,
	colURL string,
	res *resource.Resource,
) (*sdktrace.TracerProvider, error) {
	var (
		traceExporter sdktrace.SpanExporter
		err           error
	)

	if enabled && colURL != "" {
		traceExporter, err = otlptracegrpc.New(ctx, otlptracegrpc.WithEndpointURL(colURL))
	} else if enabled {
		traceExporter, err = stdouttrace.New(stdouttrace.WithPrettyPrint())
	} else {
		traceExporter, err = stdouttrace.New(stdouttrace.WithWriter(io.Discard))
	}

	if err != nil {
		return nil, err
	}

	traceProvider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
	)

	return traceProvider, nil
}

func newMeterProvider(
	ctx context.Context,
	enabled bool,
	colURL string,
	res *resource.Resource,
) (*sdkmetric.MeterProvider, error) {
	var (
		metricExporter sdkmetric.Exporter
		err            error
	)

	if enabled && colURL != "" {
		metricExporter, err = otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithEndpointURL(colURL))
	} else if enabled {
		metricExporter, err = stdoutmetric.New()
	} else {
		metricExporter, err = stdoutmetric.New(stdoutmetric.WithWriter(io.Discard))
	}

	if err != nil {
		return nil, err
	}

	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)),
	)

	return meterProvider, nil
}

func newLoggerProvider(
	ctx context.Context,
	enabled bool,
	colURL string,
	res *resource.Resource,
) (*sdklog.LoggerProvider, error) {
	var (
		logExporter sdklog.Exporter
		err         error
	)

	if enabled && colURL != "" {
		logExporter, err = otlploggrpc.New(ctx, otlploggrpc.WithEndpointURL(colURL))
	} else if enabled {
		logExporter, err = stdoutlog.New()
	} else {
		logExporter, err = stdoutlog.New(stdoutlog.WithWriter(io.Discard))
	}

	if err != nil {
		return nil, err
	}

	loggerProvider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)),
		sdklog.WithResource(res),
	)

	return loggerProvider, nil
}

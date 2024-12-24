package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/urfave/cli/v3"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"golang.org/x/sync/errgroup"
	"golang.org/x/term"

	"github.com/kalbasit/ncps/pkg/otelzerolog"
)

// Version defines the version of the binary, and is meant to be set with ldflags at build time.
//
//nolint:gochecknoglobals
var Version = "dev"

func New() *cli.Command {
	var otelShutdown func(context.Context) error

	return &cli.Command{
		Name:    "ncps",
		Usage:   "Nix Binary Cache Proxy Service",
		Version: Version,
		After: func(ctx context.Context, _ *cli.Command) error {
			return otelShutdown(ctx)
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

			colURL := cmd.String("log-otel-grpc-endpoint")
			if colURL != "" {
				otelWriter, err = otelzerolog.NewOtelWriter(ctx, colURL, "ncps")
				if err != nil {
					return ctx, err
				}

				output = zerolog.MultiLevelWriter(os.Stdout, otelWriter)
			}

			if term.IsTerminal(int(os.Stdout.Fd())) {
				output = zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}
			}

			ctx = zerolog.New(output).Level(lvl).WithContext(ctx)

			(zerolog.Ctx(ctx)).
				Info().
				Str("log-otel-grpc-endpoint", colURL).
				Str("log-level", lvl.String()).
				Msg("logger created")

			return ctx, nil
		},
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "log-level",
				Usage:   "Set the log level",
				Sources: cli.EnvVars("LOG_LEVEL"),
				Value:   "info",
				Validator: func(lvl string) error {
					_, err := zerolog.ParseLevel(lvl)

					return err
				},
			},
			&cli.StringFlag{
				Name:    "otel-grpc-endpoint",
				Usage:   "Configure OpenTelemetry gRPC endpoint, omit to disable OpenTelemetry",
				Sources: cli.EnvVars("OTEL_GRPC_ENDPOINT"),
				Value:   "",
				Validator: func(colURL string) error {
					_, err := url.Parse(colURL)

					return err
				},
			},
		},
		Commands: []*cli.Command{
			serveCommand(),
		},
	}
}

// setupOTelSDK bootstraps the OpenTelemetry pipeline.
// If it does not return an error, make sure to call shutdown for proper cleanup.
func setupOTelSDK(ctx context.Context, cmd *cli.Command) (shutdown func(context.Context) error, err error) {
	if colURL := cmd.String("otel-grpc-endpoint"); colURL == "" {
		return func(context.Context) error { return nil }, nil
	}

	var shutdownFuncs []func(context.Context) error

	// shutdown calls cleanup functions registered via shutdownFuncs.
	// The errors from the calls are joined.
	// Each registered cleanup will be invoked once.
	shutdown = func(ctx context.Context) error {
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
	handleErr := func(inErr error) {
		err = errors.Join(inErr, shutdown(ctx))
	}

	// Set up propagator.
	prop := newPropagator()
	otel.SetTextMapPropagator(prop)

	// Set up trace provider.
	tracerProvider, err := newTraceProvider(ctx, cmd)
	if err != nil {
		handleErr(err)
		return
	}
	shutdownFuncs = append(shutdownFuncs, tracerProvider.Shutdown)
	otel.SetTracerProvider(tracerProvider)

	// Set up meter provider.
	meterProvider, err := newMeterProvider(ctx, cmd)
	if err != nil {
		handleErr(err)
		return
	}
	shutdownFuncs = append(shutdownFuncs, meterProvider.Shutdown)
	otel.SetMeterProvider(meterProvider)

	// Set up logger provider.
	loggerProvider, err := newLoggerProvider(ctx, cmd)
	if err != nil {
		handleErr(err)
		return
	}
	shutdownFuncs = append(shutdownFuncs, loggerProvider.Shutdown)
	global.SetLoggerProvider(loggerProvider)

	return
}

func newPropagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
}

func newTraceProvider(ctx context.Context, cmd *cli.Command) (*sdktrace.TracerProvider, error) {
	traceExporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpointURL(cmd.String("otel-grpc-endpoint")))
	if err != nil {
		return nil, err
	}

	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(cmd.Root().Name),
	)

	traceProvider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
	)

	return traceProvider, nil
}

func newMeterProvider(ctx context.Context, cmd *cli.Command) (*sdkmetric.MeterProvider, error) {
	metricExporter, err := otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithEndpointURL(cmd.String("otel-grpc-endpoint")))
	if err != nil {
		return nil, err
	}

	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(cmd.Root().Name),
	)

	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)),
	)
	return meterProvider, nil
}

func newLoggerProvider(ctx context.Context, cmd *cli.Command) (*sdklog.LoggerProvider, error) {
	logExporter, err := otlploggrpc.New(ctx, otlploggrpc.WithEndpointURL(cmd.String("otel-grpc-endpoint")))
	if err != nil {
		return nil, err
	}

	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(cmd.Root().Name),
	)

	loggerProvider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)),
		sdklog.WithResource(res),
	)
	return loggerProvider, nil
}

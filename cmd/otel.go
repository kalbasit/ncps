package cmd

import (
	"context"
	"errors"
	"io"

	"github.com/rs/zerolog"
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

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/kalbasit/ncps/pkg/telemetry"
)

func newResource(ctx context.Context, cmd *cli.Command) (*resource.Resource, error) {
	return telemetry.NewResource(ctx, cmd.Root().Name, Version)
}

// setupOTelSDK bootstraps the OpenTelemetry pipeline.
// If it does not return an error, make sure to call shutdown for proper cleanup.
func setupOTelSDK(
	ctx context.Context,
	cmd *cli.Command,
	otelResource *resource.Resource,
) (func(context.Context) error, error) {
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

	colURL := cmd.String("otel-grpc-url")
	enabled := cmd.Bool("otel-enabled")

	ctx = zerolog.Ctx(ctx).
		With().
		Bool("otel-enabled", enabled).
		Str("otel-grpc-url", colURL).
		Logger().
		WithContext(ctx)

	// Set up trace provider.
	tracerProvider, err := newTraceProvider(ctx, enabled, colURL, otelResource)
	if err != nil {
		zerolog.Ctx(ctx).
			Error().
			Err(err).
			Msg("error creating a new tracer provider")

		return shutdown, handleErr(err)
	}

	shutdownFuncs = append(shutdownFuncs, tracerProvider.Shutdown)
	otel.SetTracerProvider(tracerProvider)

	// Set up meter provider.
	meterProvider, err := newMeterProvider(ctx, enabled, colURL, otelResource)
	if err != nil {
		zerolog.Ctx(ctx).
			Error().
			Err(err).
			Msg("error creating a new meter provider")

		return shutdown, handleErr(err)
	}

	shutdownFuncs = append(shutdownFuncs, meterProvider.Shutdown)
	otel.SetMeterProvider(meterProvider)

	// Set up logger provider.
	loggerProvider, err := newLoggerProvider(ctx, enabled, colURL, otelResource)
	if err != nil {
		zerolog.Ctx(ctx).
			Error().
			Err(err).
			Msg("error creating a new logger provider")

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
		zerolog.Ctx(ctx).
			Info().
			Msg("setting up tracer provider with gRPC endpoint")

		traceExporter, err = otlptracegrpc.New(ctx, otlptracegrpc.WithEndpointURL(colURL))
	} else if enabled {
		zerolog.Ctx(ctx).
			Info().
			Msg("setting up tracer provider with pretty printing")

		traceExporter, err = stdouttrace.New(stdouttrace.WithPrettyPrint())
	} else {
		zerolog.Ctx(ctx).
			Info().
			Msg("setting up tracer provider to discard traces")

		traceExporter, err = stdouttrace.New(stdouttrace.WithWriter(io.Discard))
	}

	if err != nil {
		zerolog.Ctx(ctx).
			Error().
			Err(err).
			Msg("error setting up the tracer provider")

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
		zerolog.Ctx(ctx).
			Info().
			Msg("setting up meter provider with gRPC endpoint")

		metricExporter, err = otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithEndpointURL(colURL))
	} else if enabled {
		zerolog.Ctx(ctx).
			Info().
			Msg("setting up meter provider with pretty printing")

		metricExporter, err = stdoutmetric.New()
	} else {
		zerolog.Ctx(ctx).
			Info().
			Msg("setting up meter provider to discard metrics")

		metricExporter, err = stdoutmetric.New(stdoutmetric.WithWriter(io.Discard))
	}

	if err != nil {
		zerolog.Ctx(ctx).
			Error().
			Err(err).
			Msg("error setting up the meter provider")

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
		zerolog.Ctx(ctx).
			Info().
			Msg("setting up tracer logger with gRPC endpoint")

		logExporter, err = otlploggrpc.New(ctx, otlploggrpc.WithEndpointURL(colURL))
	} else if enabled {
		zerolog.Ctx(ctx).
			Info().
			Msg("setting up logger provider with pretty printing")

		logExporter, err = stdoutlog.New()
	} else {
		zerolog.Ctx(ctx).
			Info().
			Msg("setting up logger provider to discard logs")

		logExporter, err = stdoutlog.New(stdoutlog.WithWriter(io.Discard))
	}

	if err != nil {
		zerolog.Ctx(ctx).
			Error().
			Err(err).
			Msg("error setting up the logger provider")

		return nil, err
	}

	loggerProvider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)),
		sdklog.WithResource(res),
	)

	return loggerProvider, nil
}

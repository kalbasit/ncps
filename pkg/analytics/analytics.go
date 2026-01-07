package analytics

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"golang.org/x/sync/errgroup"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/kalbasit/ncps/pkg/database"
)

const (
	// DefaultEndpoint is the default address for the analytics collector.
	DefaultEndpoint = "otlp.ncps.dev:443"

	// How often to report anonymous metrics.
	metricInterval = 1 * time.Hour

	instrumentationName = "github.com/kalbasit/ncps/pkg/analytics"
)

type shutdownFn func(context.Context) error

type Reporter struct {
	db  database.Querier
	res *resource.Resource

	logger log.Logger
	meter  metric.Meter

	shutdownFns map[string]shutdownFn
}

// Start initializes the analytics reporting pipeline.
// It returns a shutdown function that should be called when the application exits.
func Start(
	ctx context.Context,
	db database.Querier,
	res *resource.Resource,
) (*Reporter, error) {
	r := &Reporter{
		db:          db,
		res:         res,
		shutdownFns: make(map[string]shutdownFn),
	}

	if err := r.newLogger(ctx); err != nil {
		return nil, err
	}

	if err := r.newMeter(ctx); err != nil {
		return nil, err
	}

	zerolog.Ctx(ctx).
		Info().
		Str("endpoint", DefaultEndpoint).
		Msg("Reporting anonymous metrics to the project maintainers")

	return r, nil
}

func (r *Reporter) GetLogger() log.Logger { return r.logger }

func (r *Reporter) GetMeter() metric.Meter { return r.meter }

func (r *Reporter) Shutdown(ctx context.Context) error {
	g, gCtx := errgroup.WithContext(ctx)

	for name, sfn := range r.shutdownFns {
		g.Go(func() error {
			if err := sfn(gCtx); err != nil {
				zerolog.Ctx(gCtx).
					Error().
					Err(err).
					Str("shutdown name", name).
					Msg("error calling the shutting down function")

				return err
			}

			return nil
		})
	}

	return g.Wait()
}

func (r *Reporter) newLogger(ctx context.Context) error {
	exporter, err := otlploghttp.New(ctx,
		otlploghttp.WithEndpoint(DefaultEndpoint),
		otlploghttp.WithCompression(otlploghttp.GzipCompression),
	)
	if err != nil {
		return fmt.Errorf("failed to create analytics log exporter: %w", err)
	}

	logProvider := sdklog.NewLoggerProvider(
		sdklog.WithResource(r.res),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)),
	)

	r.shutdownFns["logger"] = logProvider.Shutdown

	r.logger = logProvider.Logger(instrumentationName)

	return nil
}

func (r *Reporter) newMeter(ctx context.Context) error {
	exporter, err := otlpmetrichttp.New(ctx,
		otlpmetrichttp.WithEndpoint(DefaultEndpoint),
		otlpmetrichttp.WithCompression(otlpmetrichttp.GzipCompression),
	)
	if err != nil {
		return fmt.Errorf("failed to create analytics metric exporter: %w", err)
	}

	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(r.res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithInterval(metricInterval))),
	)

	r.shutdownFns["meter"] = meterProvider.Shutdown

	// Register the Metrics
	meter := meterProvider.Meter(instrumentationName)
	if err := r.registerMeterCallbacks(meter); err != nil {
		return err
	}

	r.meter = meter

	return nil
}

func (r *Reporter) registerMeterCallbacks(meter metric.Meter) error {
	return r.registerMeterTotalSizeGaugeCallback(meter)
}

func (r *Reporter) registerMeterTotalSizeGaugeCallback(meter metric.Meter) error {
	// Metric: Total NAR Size
	// The resource attributes created above will automatically be attached to this metric.
	totalSizeGauge, err := meter.Int64ObservableGauge(
		"ncps_store_nar_files_total_size_bytes",
		metric.WithDescription("Total size of all NAR files stored"),
		metric.WithUnit("By"),
	)
	if err != nil {
		return fmt.Errorf("failed to create analytics gauge: %w", err)
	}

	_, err = meter.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		// This uses the existing GetNarTotalSize query from your database package
		size, err := r.db.GetNarTotalSize(ctx)
		if err != nil {
			zerolog.Ctx(ctx).
				Error().
				Err(err).
				Str("endpoint", DefaultEndpoint).
				Msg("error gathering the total size of NAR files in the store")

			// In case of error, we just skip observing this time rather than crashing
			return nil
		}

		o.ObserveInt64(totalSizeGauge, size)

		return nil
	}, totalSizeGauge)
	if err != nil {
		return fmt.Errorf("failed to register analytics callback: %w", err)
	}

	return nil
}

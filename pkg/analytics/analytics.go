package analytics

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/resource"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/kalbasit/ncps/pkg/database"
)

const (
	// DefaultEndpointURL is the default address for the analytics collector.
	DefaultEndpointURL = "https://otlp.ncps.dev"

	// How often to report anonymous metricsz/.
	interval = 1 * time.Hour

	instrumentationName = "github.com/kalbasit/ncps/pkg/analytics"
)

type reporter struct {
	db database.Querier
}

// Start initializes the analytics reporting pipeline.
// It returns a shutdown function that should be called when the application exits.
func Start(
	ctx context.Context,
	db database.Querier,
	res *resource.Resource,
) (func(context.Context) error, error) {
	r := &reporter{db}

	// Create a dedicated OTLP Exporter
	// Uncomment the line below to see the metrics on stdout.
	//exporter, err := stdoutmetric.New()
	exporter, err := otlpmetrichttp.New(ctx,
		otlpmetrichttp.WithEndpointURL(DefaultEndpointURL),
		otlpmetrichttp.WithCompression(otlpmetrichttp.GzipCompression),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create analytics exporter: %w", err)
	}

	// Create a dedicated MeterProvider
	// PeriodicReader defaults to 60s, which is good for low-volume reporting.
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithInterval(interval))),
	)

	// Register the Metrics
	meter := meterProvider.Meter(instrumentationName)
	if err := r.registerCallbacks(meter); err != nil {
		return nil, err
	}

	zerolog.Ctx(ctx).
		Info().
		Str("endpoint-url", DefaultEndpointURL).
		Msg("Reporting anonymous metrics to the project maintainers")

	return meterProvider.Shutdown, nil
}

func (r *reporter) registerCallbacks(meter metric.Meter) error {
	return r.registerTotalSizeGaugeCallback(meter)
}

func (r *reporter) registerTotalSizeGaugeCallback(meter metric.Meter) error {
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
				Str("endpoint-url", DefaultEndpointURL).
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

package prometheus

import (
	"context"

	"go.opentelemetry.io/otel"

	promclient "github.com/prometheus/client_golang/prometheus"
	prometheus "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/kalbasit/ncps/pkg/telemetry"
)

// SetupPrometheusMetrics configures OpenTelemetry to export metrics in Prometheus format only
// without any console output or other telemetry.
func SetupPrometheusMetrics(
	ctx context.Context,
	serviceName, serviceVersion string,
) (promclient.Gatherer, func(context.Context) error, error) {
	// Create resource with service information using shared telemetry function
	res, err := telemetry.NewResource(ctx, serviceName, serviceVersion)
	if err != nil {
		return nil, nil, err
	}

	// Create a custom Prometheus registry
	registry := promclient.NewRegistry()

	// Create Prometheus exporter with the custom registry
	prometheusExporter, err := prometheus.New(
		prometheus.WithRegisterer(registry),
	)
	if err != nil {
		return nil, nil, err
	}

	// Create meter provider with Prometheus exporter
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(prometheusExporter),
	)

	// Set the meter provider globally for OpenTelemetry instrumentation
	otel.SetMeterProvider(meterProvider)

	// Return the Prometheus registry (which implements Gatherer) and shutdown function
	return registry, meterProvider.Shutdown, nil
}

package prometheus

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/resource"

	promclient "github.com/prometheus/client_golang/prometheus"
	prometheus "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// SetupPrometheusMetrics configures OpenTelemetry to export metrics in Prometheus format only
// without any console output or other telemetry.
func SetupPrometheusMetrics(res *resource.Resource) (promclient.Gatherer, func(context.Context) error, error) {
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

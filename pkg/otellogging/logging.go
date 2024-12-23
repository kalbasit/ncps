package otellogging

import (
	"context"
	"encoding/json"

	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/resource"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
)

// OtelWriter implements zerolog.LevelWriter interface.
type OtelWriter struct {
	exporter      *otlpmetricgrpc.Exporter
	serviceName   string
	meterProvider *sdkmetric.MeterProvider
	meter         metric.Meter
}

// NewOtelWriter creates a new OpenTelemetry writer for zerolog.
func NewOtelWriter(ctx context.Context, endpoint, serviceName string) (*OtelWriter, error) {
	// Create OTLP exporter
	exporter, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithEndpoint(endpoint),
		otlpmetricgrpc.WithInsecure(),
	)
	if err != nil {
		return nil, err
	}

	// Create resource attributes
	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(serviceName),
	)

	// Create meter provider with the resource
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter)),
	)

	// Set the global meter provider
	otel.SetMeterProvider(meterProvider)

	// Create meter
	meter := meterProvider.Meter(
		"zerolog-metrics",
		metric.WithInstrumentationVersion("1.0.0"),
	)

	return &OtelWriter{
		exporter:      exporter,
		serviceName:   serviceName,
		meterProvider: meterProvider,
		meter:         meter,
	}, nil
}

// Write implements io.Writer.
func (w *OtelWriter) Write(p []byte) (n int, err error) {
	// Parse the JSON log entry
	var logEntry map[string]interface{}
	if err := json.Unmarshal(p, &logEntry); err != nil {
		return 0, err
	}

	// Create a counter for log entries using the configured meter
	counter, _ := w.meter.Int64Counter("log_entries_total")
	counter.Add(context.Background(), 1)

	return len(p), nil
}

// WriteLevel implements zerolog.LevelWriter.
func (w *OtelWriter) WriteLevel(_ zerolog.Level, p []byte) (n int, err error) {
	return w.Write(p)
}

// Close closes the OpenTelemetry exporter and meter provider.
func (w *OtelWriter) Close(ctx context.Context) error {
	if err := w.meterProvider.Shutdown(ctx); err != nil {
		return err
	}

	return w.exporter.Shutdown(ctx)
}

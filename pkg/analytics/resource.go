package analytics

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"

	semconv "go.opentelemetry.io/otel/semconv/v1.39.0"
)

// NewResource creates a new OpenTelemetry resource with standard attributes
// but WITHOUT hostname and process owner to preserve anonymity.
func NewResource(
	ctx context.Context,
	serviceName,
	serviceVersion,
	schemaURL string,
	extraAttrs ...attribute.KeyValue,
) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{
		semconv.ServiceName(serviceName),
		semconv.ServiceVersionKey.String(serviceVersion),
	}
	attrs = append(attrs, extraAttrs...)

	return resource.New(
		ctx,

		// Set the Schema URL.
		resource.WithSchemaURL(schemaURL),

		// Add Custom attributes.
		resource.WithAttributes(attrs...),

		// Discover and provide attributes from OTEL_RESOURCE_ATTRIBUTES and
		// OTEL_SERVICE_NAME environment variables.
		resource.WithFromEnv(),

		// Discover and provide information about the OpenTelemetry SDK used.
		resource.WithTelemetrySDK(),

		// Discover and provide process information.
		// NOTE: resource.WithProcessOwner() is deliberately excluded to avoid PII
		resource.WithProcessPID(),
		resource.WithProcessExecutableName(),
		resource.WithProcessExecutablePath(),
		resource.WithProcessRuntimeName(),
		resource.WithProcessRuntimeVersion(),
		resource.WithProcessRuntimeDescription(),

		// Discover and provide OS information.
		resource.WithOS(),

		// Discover and provide container information.
		resource.WithContainer(),

		// Discover and provide host information.
		// NOTE: resource.WithHost() is deliberately excluded to avoid PII
	)
}

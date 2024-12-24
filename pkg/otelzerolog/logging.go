package otelzerolog

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/sdk/resource"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
)

// OtelWriter implements zerolog.LevelWriter interface.
type OtelWriter struct {
	logger      log.Logger
	serviceName string
	logExporter *otlploggrpc.Exporter
}

// NewOtelWriter creates a new OpenTelemetry writer for zerolog.
func NewOtelWriter(ctx context.Context, endpoint, serviceName string) (*OtelWriter, error) {
	// Create OTLP logs exporter
	logExporter, err := otlploggrpc.New(ctx,
		otlploggrpc.WithEndpoint(endpoint),
		otlploggrpc.WithInsecure(),
	)
	if err != nil {
		return nil, err
	}

	// Create resource with service name
	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(serviceName),
	)

	// Create logger provider
	loggerProvider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)),
		sdklog.WithResource(res),
	)

	logger := loggerProvider.Logger("zerolog-otel")

	return &OtelWriter{
		logger:      logger,
		serviceName: serviceName,
		logExporter: logExporter,
	}, nil
}

// Write implements io.Writer.
func (w *OtelWriter) Write(p []byte) (n int, err error) {
	var logEntry map[string]interface{}
	if err := json.Unmarshal(p, &logEntry); err != nil {
		return 0, err
	}

	var rec log.Record

	if levelStr, ok := logEntry["level"].(string); ok {
		level := zerolog.InfoLevel
		if l, err := zerolog.ParseLevel(levelStr); err == nil {
			level = l
		}

		rec.SetSeverity(convertLevel(level))
		rec.SetSeverityText(level.String())

		delete(logEntry, "level")
	}

	if msg, ok := logEntry["message"].(string); ok {
		rec.SetBody(log.StringValue(msg))

		delete(logEntry, "message")
	}

	rec.AddAttributes(getKeyValueForMap(logEntry)...)

	// Send log record
	w.logger.Emit(context.Background(), rec)

	return len(p), nil
}

// WriteLevel implements zerolog.LevelWriter.
func (w *OtelWriter) WriteLevel(_ zerolog.Level, p []byte) (n int, err error) {
	return w.Write(p)
}

// Close shuts down the OpenTelemetry exporter.
func (w *OtelWriter) Close(ctx context.Context) error {
	return w.logExporter.Shutdown(ctx)
}

// convertLevel converts zerolog level to OpenTelemetry severity.
func convertLevel(level zerolog.Level) log.Severity {
	switch level {
	case zerolog.DebugLevel:
		return log.SeverityDebug
	case zerolog.InfoLevel:
		return log.SeverityInfo
	case zerolog.WarnLevel:
		return log.SeverityWarn
	case zerolog.ErrorLevel:
		return log.SeverityError
	case zerolog.FatalLevel:
		return log.SeverityFatal
	case zerolog.PanicLevel:
		return log.SeverityFatal
	case zerolog.NoLevel:
		return log.SeverityInfo
	case zerolog.Disabled:
		return log.SeverityInfo
	case zerolog.TraceLevel:
		return log.SeverityTrace
	default:
		return log.SeverityInfo
	}
}

func getKeyValueForMap(m map[string]interface{}) []log.KeyValue {
	kvs := make([]log.KeyValue, 0, len(m))

	for k, v := range m {
		switch val := v.(type) {
		case bool:
			kvs = append(kvs, log.Bool(k, val))
		case float64:
			if ival := int64(val); float64(ival) == val {
				kvs = append(kvs, log.Int64(k, ival))
			} else {
				kvs = append(kvs, log.Float64(k, val))
			}
		case string:
			kvs = append(kvs, log.String(k, val))
		case []interface{}:
			kvs = append(kvs, log.Slice(k, getValuesForSlice(val)...))
		case map[string]interface{}:
			kvs = append(kvs, log.Map(k, getKeyValueForMap(val)...))
		default:
			panic(fmt.Sprintf("Typeof(%q) => %T: not known", k, v))
		}
	}

	return kvs
}

func getValuesForSlice(vals []interface{}) []log.Value {
	var vs []log.Value

	for _, v := range vals {
		switch val := v.(type) {
		case bool:
			vs = append(vs, log.BoolValue(val))
		case float64:
			if ival := int64(val); float64(ival) == val {
				vs = append(vs, log.Int64Value(ival))
			} else {
				vs = append(vs, log.Float64Value(val))
			}
		case string:
			vs = append(vs, log.StringValue(val))
		case map[string]interface{}:
			vs = append(vs, log.MapValue(getKeyValueForMap(val)...))
		case []interface{}:
			vs = append(vs, log.SliceValue(getValuesForSlice(val)...))
		default:
			panic(fmt.Sprintf("Typeof(%#v) => %T: not known", v, v))
		}
	}

	return vs
}

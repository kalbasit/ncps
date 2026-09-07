package otelzerolog

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/global"
)

// OtelWriter implements zerolog.LevelWriter interface.
type OtelWriter struct {
	logger log.Logger
}

// NewOtelWriter creates a new OpenTelemetry writer for zerolog.
func NewOtelWriter(loggerProvider log.LoggerProvider) (*OtelWriter, error) {
	if loggerProvider == nil {
		loggerProvider = global.GetLoggerProvider()
	}

	logger := loggerProvider.Logger("otel-zerolog")

	return &OtelWriter{
		logger: logger,
	}, nil
}

// Write implements io.Writer.
func (w *OtelWriter) Write(p []byte) (n int, err error) {
	var logEntry map[string]any
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
		rec.SetBody(attribute.StringValue(msg))

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

func getKeyValueForMap(m map[string]any) []attribute.KeyValue {
	kvs := make([]attribute.KeyValue, 0, len(m))

	for k, v := range m {
		switch val := v.(type) {
		case bool:
			kvs = append(kvs, attribute.Bool(k, val))
		case float64:
			if ival := int64(val); float64(ival) == val {
				kvs = append(kvs, attribute.Int64(k, ival))
			} else {
				kvs = append(kvs, attribute.Float64(k, val))
			}
		case string:
			kvs = append(kvs, attribute.String(k, val))
		case []any:
			kvs = append(kvs, attribute.Slice(k, getValuesForSlice(val)...))
		case map[string]any:
			kvs = append(kvs, attribute.Map(k, getKeyValueForMap(val)...))
		default:
			panic(fmt.Sprintf("Typeof(%q) => %T: not known", k, v))
		}
	}

	return kvs
}

func getValuesForSlice(vals []any) []attribute.Value {
	var vs []attribute.Value

	for _, v := range vals {
		switch val := v.(type) {
		case bool:
			vs = append(vs, attribute.BoolValue(val))
		case float64:
			if ival := int64(val); float64(ival) == val {
				vs = append(vs, attribute.Int64Value(ival))
			} else {
				vs = append(vs, attribute.Float64Value(val))
			}
		case string:
			vs = append(vs, attribute.StringValue(val))
		case map[string]any:
			vs = append(vs, attribute.MapValue(getKeyValueForMap(val)...))
		case []any:
			vs = append(vs, attribute.SliceValue(getValuesForSlice(val)...))
		default:
			panic(fmt.Sprintf("Typeof(%#v) => %T: not known", v, v))
		}
	}

	return vs
}

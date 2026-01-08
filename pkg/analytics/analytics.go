package analytics

import (
	"context"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"golang.org/x/sync/errgroup"

	nooplog "go.opentelemetry.io/otel/log/noop"
	noopmetric "go.opentelemetry.io/otel/metric/noop"
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

//nolint:gochecknoglobals
var ctxKey = &struct{}{}

type shutdownFn func(context.Context) error

type Reporter interface {
	GetLogger() log.Logger
	GetMeter() metric.Meter
	LogPanic(context.Context, any, []byte)
	Shutdown(context.Context) error
	WithContext(context.Context) context.Context
}

type nopReporter struct{}

func (nr nopReporter) GetLogger() log.Logger {
	return nooplog.NewLoggerProvider().Logger("noop")
}

func (nr nopReporter) GetMeter() metric.Meter {
	return noopmetric.NewMeterProvider().Meter("noop")
}
func (nr nopReporter) LogPanic(context.Context, any, []byte)           {}
func (nr nopReporter) Shutdown(context.Context) error                  { return nil }
func (nr nopReporter) WithContext(ctx context.Context) context.Context { return ctx }

type reporter struct {
	db  database.Querier
	res *resource.Resource

	logger log.Logger
	meter  metric.Meter

	shutdownFns map[string]shutdownFn
}

// New initializes the analytics reporting pipeline.
// It returns a shutdown function that should be called when the application exits.
func New(
	ctx context.Context,
	db database.Querier,
	res *resource.Resource,
) (Reporter, error) {
	r := &reporter{
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

func Ctx(ctx context.Context) Reporter {
	r, ok := ctx.Value(ctxKey).(*reporter)
	if !ok || r == nil {
		return nopReporter{}
	}

	return r
}

func SafeGo(ctx context.Context, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				Ctx(ctx).LogPanic(ctx, r, debug.Stack())
			}
		}()

		fn()
	}()
}

func (r *reporter) GetLogger() log.Logger { return r.logger }

func (r *reporter) GetMeter() metric.Meter { return r.meter }

func (r *reporter) LogPanic(ctx context.Context, rvr any, stack []byte) {
	record := log.Record{}
	record.SetTimestamp(time.Now())
	record.SetSeverity(log.SeverityFatal)
	record.SetSeverityText("FATAL")
	record.SetBody(log.StringValue("Application panic recovered"))
	record.AddAttributes(
		log.String("panic.value", fmt.Sprintf("%v", rvr)),
		log.String("panic.stack", string(stack)),
	)

	r.logger.Emit(ctx, record)
}

func (r *reporter) Shutdown(ctx context.Context) error {
	var g errgroup.Group

	for name, sfn := range r.shutdownFns {
		name, sfn := name, sfn

		g.Go(func() error {
			if err := sfn(ctx); err != nil {
				zerolog.Ctx(ctx).
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

func (r *reporter) WithContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxKey, r)
}

func (r *reporter) newLogger(ctx context.Context) error {
	// Uncomment the line below to see the logs on stdout.
	// exporter, err := stdoutlog.New()
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

func (r *reporter) newMeter(ctx context.Context) error {
	// Uncomment the line below to see the metrics on stdout.
	// exporter, err := stdoutmetric.New()
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

func (r *reporter) registerMeterCallbacks(meter metric.Meter) error {
	return r.registerMeterTotalSizeGaugeCallback(meter)
}

func (r *reporter) registerMeterTotalSizeGaugeCallback(meter metric.Meter) error {
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

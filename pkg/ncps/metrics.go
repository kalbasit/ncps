package ncps

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	otelPackageNameMetrics = "github.com/kalbasit/ncps/pkg/ncps"

	// Migration result constants for metrics.
	MigrationResultSuccess = "success"
	MigrationResultFailure = "failure"
	MigrationResultSkipped = "skipped"

	// Migration operation constants for metrics.
	MigrationOperationMigrate = "migrate"
	MigrationOperationDelete  = "delete"
)

var (
	//nolint:gochecknoglobals
	meterMigration metric.Meter

	// migrationNarInfosTotal tracks total narinfos processed during migration.
	//nolint:gochecknoglobals
	migrationNarInfosTotal metric.Int64Counter

	// migrationDuration tracks the duration of migration operations.
	//nolint:gochecknoglobals
	migrationDuration metric.Float64Histogram

	// migrationBatchSize tracks the number of narinfos in each migration batch.
	//nolint:gochecknoglobals
	migrationBatchSize metric.Int64Histogram
)

//nolint:gochecknoinits
func init() {
	meterMigration = otel.Meter(otelPackageNameMetrics)

	var err error

	migrationNarInfosTotal, err = meterMigration.Int64Counter(
		"ncps_migration_narinfos_total",
		metric.WithDescription("Total number of narinfos processed during migration"),
		metric.WithUnit("{narinfo}"),
	)
	if err != nil {
		panic(err)
	}

	migrationDuration, err = meterMigration.Float64Histogram(
		"ncps_migration_duration_seconds",
		metric.WithDescription("Duration of narinfo migration operations"),
		metric.WithUnit("s"),
	)
	if err != nil {
		panic(err)
	}

	migrationBatchSize, err = meterMigration.Int64Histogram(
		"ncps_migration_batch_size",
		metric.WithDescription("Number of narinfos in each migration batch"),
		metric.WithUnit("{narinfo}"),
	)
	if err != nil {
		panic(err)
	}
}

// RecordMigrationNarInfo records a narinfo migration operation.
// operation should be one of MigrationOperation* constants.
// result should be one of MigrationResult* constants.
func RecordMigrationNarInfo(ctx context.Context, operation, result string) {
	if migrationNarInfosTotal == nil {
		return
	}

	migrationNarInfosTotal.Add(ctx, 1,
		metric.WithAttributes(
			attribute.String("operation", operation),
			attribute.String("result", result),
		),
	)
}

// RecordMigrationDuration records the duration of a migration operation.
// operation should be one of MigrationOperation* constants.
// duration should be in seconds.
func RecordMigrationDuration(ctx context.Context, operation string, duration float64) {
	if migrationDuration == nil {
		return
	}

	migrationDuration.Record(ctx, duration,
		metric.WithAttributes(
			attribute.String("operation", operation),
		),
	)
}

// RecordMigrationBatchSize records the size of a migration batch.
func RecordMigrationBatchSize(ctx context.Context, size int64) {
	if migrationBatchSize == nil {
		return
	}

	migrationBatchSize.Record(ctx, size)
}

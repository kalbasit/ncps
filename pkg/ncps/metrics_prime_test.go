package ncps_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/resource"

	"github.com/kalbasit/ncps/pkg/cache"
	"github.com/kalbasit/ncps/pkg/lock"
	"github.com/kalbasit/ncps/pkg/ncps"
	"github.com/kalbasit/ncps/pkg/prometheus"
)

// TestPrimedCountersExposedAtZero verifies the fix for issue #1337: counter
// metrics must appear at GET /metrics from startup (value 0), before any NAR or
// narinfo has been served.
//
// This test is intentionally NOT parallel: it installs a global OTel meter
// provider via SetupPrometheusMetrics. Running it during the sequential phase
// (before t.Parallel() tests start) isolates it from other tests that record
// into the same global instruments, which would otherwise make the zero-value
// assertions flaky.
//
//nolint:paralleltest // sets global OTel meter provider; needs isolation for deterministic zero assertions.
func TestPrimedCountersExposedAtZero(t *testing.T) {
	ctx := context.Background()

	gatherer, shutdown, err := prometheus.SetupPrometheusMetrics(resource.Default())
	require.NoError(t, err)

	t.Cleanup(func() {
		assert.NoError(t, shutdown(ctx))
	})

	// Prime every package's counters after the meter provider is installed.
	cache.PrimeMetrics(ctx)
	lock.PrimeMetrics(ctx)
	ncps.PrimeMetrics(ctx)

	families, err := gatherer.Gather()
	require.NoError(t, err)

	// Map each gathered family to the sum of its counter values so the
	// assertions are robust to the empty-attribute series the prime emits.
	values := make(map[string]float64, len(families))

	for _, fam := range families {
		var total float64

		for _, m := range fam.GetMetric() {
			total += m.GetCounter().GetValue()
		}

		values[fam.GetName()] = total
	}

	// The headline metrics from issue #1337 plus the rest of the counters that
	// share the "not exported until first increment" problem.
	wantZero := []string{
		"ncps_nar_served_total",
		"ncps_narinfo_served_total",
		"ncps_lru_cleanup_runs_total",
		"ncps_lru_narinfos_evicted_total",
		"ncps_lru_nar_files_evicted_total",
		"ncps_lru_chunks_evicted_total",
		"ncps_lru_bytes_freed_total",
		"ncps_background_migration_objects_total",
		"ncps_download_coordination_fallback_total",
		"ncps_lock_acquisitions_total",
		"ncps_lock_failures_total",
		"ncps_lock_retry_attempts_total",
		"ncps_migration_objects_total",
	}

	for _, name := range wantZero {
		got, ok := values[name]
		assert.Truef(t, ok, "metric %q missing from /metrics at startup; got %v", name, names(values))
		assert.Zerof(t, got, "primed metric %q must be exposed at value 0 (prime must not inflate the count)", name)
	}

	// Priming must not inflate the count: after one real event the total is 1,
	// not 2. Drive a real increment through a public recording API and re-scrape.
	lock.RecordLockRetryAttempt(ctx, lock.LockTypeRead)

	families, err = gatherer.Gather()
	require.NoError(t, err)

	var retryTotal float64

	for _, fam := range families {
		if fam.GetName() != "ncps_lock_retry_attempts_total" {
			continue
		}

		for _, m := range fam.GetMetric() {
			retryTotal += m.GetCounter().GetValue()
		}
	}

	assert.InDelta(t, float64(1), retryTotal, 0, "priming (0) plus one real event must total 1, not 2")
}

func names(m map[string]float64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	return out
}

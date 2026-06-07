package lock_test

import (
	"context"
	"testing"

	"github.com/kalbasit/ncps/pkg/lock"
)

// TestPrimeMetricsNoProviderIsNoOp verifies that priming the counters is safe
// when no OTel meter provider has been installed (metrics disabled). The
// measurements are simply dropped; the call must not error or panic.
func TestPrimeMetricsNoProviderIsNoOp(t *testing.T) {
	t.Parallel()

	// Must not panic. No provider is configured in this test binary, so the
	// zero-valued measurements are dropped.
	lock.PrimeMetrics(context.Background())
}

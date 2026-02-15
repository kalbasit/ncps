package circuitbreaker_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/kalbasit/ncps/pkg/circuitbreaker"
)

//nolint:paralleltest // modifying global timeNow
func TestCircuitBreaker_State(t *testing.T) {
	// Not parallel because we mock timeNow
	currentTime := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)

	cleanup := circuitbreaker.SetTimeNow(func() time.Time {
		return currentTime
	})
	t.Cleanup(cleanup)

	cb := circuitbreaker.New(3, 1*time.Minute)

	// Initially closed
	assert.Equal(t, circuitbreaker.StateClosed, cb.State())
	assert.False(t, cb.IsOpen())

	// Force open
	cb.ForceOpen()
	assert.Equal(t, circuitbreaker.StateOpen, cb.State())
	assert.True(t, cb.IsOpen())

	// Advance time past timeout
	currentTime = currentTime.Add(61 * time.Second)

	assert.Equal(t, circuitbreaker.StateHalfOpen, cb.State())
	assert.True(t, cb.IsOpen())

	// If a request is allowed, it sets the timer to now, keeping it open/half-open regarding State() logic
	// but allowing this specific request.
	allowed := cb.AllowRequest()
	assert.True(t, allowed)

	assert.Equal(t, circuitbreaker.StateOpen, cb.State())
	assert.True(t, cb.IsOpen())

	// If success is recorded
	cb.RecordSuccess()
	assert.Equal(t, circuitbreaker.StateClosed, cb.State())
	assert.False(t, cb.IsOpen())
}

func TestState_String(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		state    circuitbreaker.State
		expected string
	}{
		{circuitbreaker.StateClosed, "closed"},
		{circuitbreaker.StateOpen, "open"},
		{circuitbreaker.StateHalfOpen, "half-open"},
		{circuitbreaker.State(999), "unknown"},
	}

	for _, tc := range testCases {
		t.Run(tc.expected, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.expected, tc.state.String())
		})
	}
}

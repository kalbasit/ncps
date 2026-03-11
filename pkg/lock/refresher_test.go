package lock_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/kalbasit/ncps/pkg/lock"
)

var errLockExpired = errors.New("lock expired")

// mockLocker is a test double for lock.Locker that counts Extend calls.
type mockLocker struct {
	extendCalls atomic.Int64
	extendErr   error
}

func (m *mockLocker) Lock(_ context.Context, _ string, _ time.Duration) error { return nil }
func (m *mockLocker) Unlock(_ context.Context, _ string) error                { return nil }
func (m *mockLocker) TryLock(_ context.Context, _ string, _ time.Duration) (bool, error) {
	return true, nil
}

func (m *mockLocker) Extend(_ context.Context, _ string) error {
	m.extendCalls.Add(1)

	return m.extendErr
}

func TestStartRefresher_ExtendsLock(t *testing.T) {
	t.Parallel()

	ml := &mockLocker{}
	ttl := 30 * time.Millisecond // short TTL so refresh fires quickly (interval = 20ms)

	stop := lock.StartRefresher(context.Background(), ml, "key", ttl)
	defer stop()

	// Wait long enough for at least 2 refreshes
	time.Sleep(60 * time.Millisecond)
	stop()

	assert.GreaterOrEqual(t, ml.extendCalls.Load(), int64(2))
}

func TestStartRefresher_StopPreventsExtend(t *testing.T) {
	t.Parallel()

	ml := &mockLocker{}
	ttl := 30 * time.Millisecond
	stop := lock.StartRefresher(context.Background(), ml, "key", ttl)

	// Stop immediately before any tick fires
	stop()

	// Small sleep to allow goroutine to exit
	time.Sleep(5 * time.Millisecond)

	calls := ml.extendCalls.Load()
	// Allow at most 1 call in case the ticker fired right before stop
	assert.LessOrEqual(t, calls, int64(1))
}

func TestStartRefresher_ContextCancellation(t *testing.T) {
	t.Parallel()

	ml := &mockLocker{}
	ttl := 30 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())

	stop := lock.StartRefresher(ctx, ml, "key", ttl)
	defer stop()

	// Cancel context — goroutine should exit
	cancel()

	// Wait a bit, then confirm no more calls happen after cancellation
	time.Sleep(5 * time.Millisecond)

	callsBefore := ml.extendCalls.Load()

	time.Sleep(60 * time.Millisecond)

	callsAfter := ml.extendCalls.Load()

	assert.Equal(t, callsBefore, callsAfter, "no Extend calls expected after context cancellation")
}

func TestStartRefresher_ExtendFailureKeepsRetrying(t *testing.T) {
	t.Parallel()

	ml := &mockLocker{extendErr: errLockExpired}
	ttl := 30 * time.Millisecond

	stop := lock.StartRefresher(context.Background(), ml, "key", ttl)
	defer stop()

	// Even with errors, the refresher keeps calling Extend
	time.Sleep(70 * time.Millisecond)
	stop()

	assert.GreaterOrEqual(t, ml.extendCalls.Load(), int64(2))
}

func TestStartRefresher_StopIdempotent(t *testing.T) {
	t.Parallel()

	ml := &mockLocker{}
	stop := lock.StartRefresher(context.Background(), ml, "key", 30*time.Millisecond)

	// Calling stop multiple times should not panic
	assert.NotPanics(t, func() {
		stop()
		stop()
		stop()
	})
}

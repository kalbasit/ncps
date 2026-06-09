package cache

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/nar"
)

// TestStageInflightNar_ActivatesDuringChunkingWindow verifies that the holder's
// staging producer remains alive through the eager-CDC chunking window — bounded
// by ds.done, not ds.stored — so a cross-pod staging request recorded AFTER the
// bytes are stored (chunk start) but before chunking completes is still observed
// and staged from the still-present temp file.
//
// This is the producer-liveness guarantee Option A relies on (design D2): the
// contending reader records its staging request mid-chunking, and the holder must
// still be polling to act on it. It passes without any producer code change; this
// test locks that behavior in.
func TestStageInflightNar_ActivatesDuringChunkingWindow(t *testing.T) {
	t.Parallel()

	c, _, store, dir, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	c.SetInflightStaging(true, 5*time.Minute, 4, true)

	ctx := context.Background()

	const (
		hash    = "abcdef0123456789abcdef0123456789"
		content = "abcdefghijklmno" // 15 bytes -> parts of 4, 4, 4, 3
	)

	// The download bytes are fully written to the temp file (chunking runs after
	// the bytes land), so this models the holder at the start of the chunking
	// window: ds.stored has closed but ds.done has not. The temp file's native
	// compression is xz here to exercise a non-default staged compression label.
	ds := newCompletedStagingDownloadState(t, dir, content, nar.CompressionTypeXz)
	ds.storedOnce.Do(func() { close(ds.stored) })

	// No staging request exists yet — the producer must keep polling past ds.stored.
	producerDone := make(chan struct{})

	go func() {
		defer close(producerDone)

		c.stageInflightNar(ctx, hash, ds)
	}()

	// Record the cross-pod waiter's request mid-chunking-window, after ds.stored.
	// A producer (incorrectly) bound to ds.stored would already have exited and
	// missed this; one bound to ds.done is still polling and picks it up.
	require.NoError(t, c.markStagingRequested(ctx, hash))

	select {
	case <-producerDone:
	case <-time.After(10 * time.Second):
		t.Fatal("staging producer did not stage a request recorded during the chunking window")
	}

	st, err := c.getStagingState(ctx, hash)
	require.NoError(t, err)
	require.NotNil(t, st)
	assert.Equal(t, int64(4), st.PartsAvailable, "15 bytes at part size 4 = 4 parts")
	assert.Equal(t, stagingStatusComplete, st.Status)
	assert.Equal(t, nar.CompressionTypeXz.String(), st.Compression,
		"staged compression must match the temp file's native compression")
	assert.Equal(t, content, readStagingParts(t, store, hash, st.PartsAvailable))
}

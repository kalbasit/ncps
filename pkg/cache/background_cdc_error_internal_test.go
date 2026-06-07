package cache

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A peer holding the per-hash migration lock surfaces as ErrMigrationInProgress in the
// background CDC goroutine. That is a benign "someone else owns it" outcome in an HA
// fleet: it MUST NOT fail the in-flight download nor log at error level.
func TestReportBackgroundCDCErrorMigrationInProgressIsBenign(t *testing.T) {
	t.Parallel()

	var logBuf bytes.Buffer

	ctx := zerolog.New(&logBuf).WithContext(context.Background())

	ds := newDownloadState()

	c := &Cache{}
	c.reportBackgroundCDCError(
		ctx,
		ds,
		fmt.Errorf("background chunking: %w", ErrMigrationInProgress),
		"nar/abc123.nar",
		"CDC chunking failed in background after pullNarIntoStore",
	)

	require.NoError(t, ds.getError(), "a peer-held migration lock must not fail the download")
	assert.NotContains(t, logBuf.String(), `"level":"error"`,
		"a benign migration-lock contention must not log at error level")
}

// Any other background CDC error is a genuine failure: it MUST fail the download
// (ds.setError) and log at error level so it is visible and alertable.
func TestReportBackgroundCDCErrorRealErrorFailsDownload(t *testing.T) {
	t.Parallel()

	var logBuf bytes.Buffer

	ctx := zerolog.New(&logBuf).WithContext(context.Background())

	ds := newDownloadState()

	realErr := io.ErrUnexpectedEOF // a genuine truncation error, not ErrMigrationInProgress

	c := &Cache{}
	c.reportBackgroundCDCError(
		ctx,
		ds,
		realErr,
		"nar/abc123.nar",
		"CDC chunking failed in background after pullNarIntoStore",
	)

	require.ErrorIs(t, ds.getError(), realErr, "a genuine CDC error must fail the download")
	assert.Contains(t, logBuf.String(), `"level":"error"`,
		"a genuine CDC error must be logged at error level")
}

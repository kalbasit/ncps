package cache

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/nix-community/go-nix/pkg/narinfo/signature"
	"github.com/nix-community/go-nix/pkg/nixhash"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/chunker"
	"github.com/kalbasit/ncps/pkg/zstd"
)

// AcquireMigrationLockForTest acquires the per-hash CDC migration lock so external
// (cache_test) tests can simulate an in-flight background chunker holding it — production
// holds it for the chunk's whole duration via withNarMigrationLock. It returns a release
// function and whether the lock was acquired.
func (c *Cache) AcquireMigrationLockForTest(ctx context.Context, hash string) (func(), bool) {
	lockKey := migrationLockKey(hash)

	acquired, err := c.downloadLocker.TryLock(ctx, lockKey, c.downloadLockTTL)
	if err != nil || !acquired {
		return func() {}, false
	}

	return func() { _ = c.downloadLocker.Unlock(context.WithoutCancel(ctx), lockKey) }, true
}

// BuildTraceFingerprint is a test-only export of buildTraceFingerprint.
func BuildTraceFingerprint(e BuildTraceEntryJSON) (string, error) {
	return buildTraceFingerprint(e)
}

// BuildTraceEntryJSON is a test-only re-export of the unexported type.
type BuildTraceEntryJSON = buildTraceEntryJSON

// BuildTraceKey is a test-only re-export.
type BuildTraceKey = buildTraceKey

// BuildTraceValue is a test-only re-export.
type BuildTraceValue = buildTraceValue

// BuildTraceSig is a test-only re-export.
type BuildTraceSig = buildTraceSig

// SetChunker is a test-only export to inject a custom chunker implementation.
func (c *Cache) SetChunker(ch chunker.Chunker) {
	c.cdcMu.Lock()
	defer c.cdcMu.Unlock()

	c.chunker = ch
}

// SetupNarInfo is a test-only export.
func SetupNarInfo(t *testing.T, hash, urlVal, compression string) *narinfo.NarInfo {
	return setupNarInfo(t, hash, urlVal, compression)
}

// CompressZstd is a test-only export.
func CompressZstd(t *testing.T, data string) string {
	return compressZstd(t, data)
}

func compressZstd(t *testing.T, data string) string {
	t.Helper()

	var buf strings.Builder

	pw := zstd.NewPooledWriter(&buf)

	_, err := io.WriteString(pw, data)
	require.NoError(t, err)

	err = pw.Close()
	assert.NoError(t, err) //nolint:testifylint

	return buf.String()
}

func setupNarInfo(t *testing.T, hash, urlVal, compression string) *narinfo.NarInfo {
	t.Helper()

	h, err := nixhash.ParseAny("sha256:"+hash, nil)
	require.NoError(t, err)

	return &narinfo.NarInfo{
		StorePath:   "/nix/store/" + hash + "-test",
		URL:         urlVal,
		Compression: compression,
		FileHash:    h,
		FileSize:    1234,
		NarHash:     h,
		NarSize:     1234,
		References:  []string{},
		Deriver:     "test.drv",
		System:      "x86_64-linux",
		Signatures:  []signature.Signature{},
		CA:          "",
	}
}

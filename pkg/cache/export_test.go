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

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/zstd"
)

// CheckAndFixNarInfo is a test-only export of the unexported checkAndFixNarInfo method.
func (c *Cache) CheckAndFixNarInfo(ctx context.Context, hash string) error {
	return c.checkAndFixNarInfo(ctx, hash)
}

// HasNarInStore is a test-only export of the unexported hasNarInStore method.
// It returns true if the NAR exists as a whole file in narStore (not chunks).
func (c *Cache) HasNarInStore(ctx context.Context, narURL nar.URL) bool {
	return c.hasNarInStore(ctx, narURL)
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

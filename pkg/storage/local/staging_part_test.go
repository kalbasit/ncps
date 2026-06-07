package local_test

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/storage"
	"github.com/kalbasit/ncps/pkg/storage/local"
)

func TestStagingParts_WriteReadDelete(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	s, err := local.New(newContext(), dir)
	require.NoError(t, err)

	ctx := newContext()

	const hash = "abcdef0123456789abcdef0123456789"

	// A missing part reads as a confirmed not-found.
	_, err = s.GetStagingPart(ctx, hash, 0)
	require.ErrorIs(t, err, storage.ErrNotFound,
		"a part that was never written must read as ErrNotFound")

	// Write three ordered parts.
	parts := []string{"part-zero-", "part-one--", "part-two--"}
	for i, p := range parts {
		n, putErr := s.PutStagingPart(ctx, hash, int64(i), strings.NewReader(p), int64(len(p)))
		require.NoError(t, putErr)
		assert.Equal(t, int64(len(p)), n)
	}

	// Read them back and confirm contiguous reassembly in index order.
	var got strings.Builder

	for i := range parts {
		rc, getErr := s.GetStagingPart(ctx, hash, int64(i))
		require.NoError(t, getErr)

		b, readErr := io.ReadAll(rc)
		require.NoError(t, readErr)
		require.NoError(t, rc.Close())

		got.Write(b)
	}

	assert.Equal(t, strings.Join(parts, ""), got.String(),
		"reassembling parts 0..N in order must yield the original byte stream")

	// Delete reclaims every part for the hash.
	require.NoError(t, s.DeleteStagingParts(ctx, hash))

	_, err = s.GetStagingPart(ctx, hash, 0)
	require.ErrorIs(t, err, storage.ErrNotFound,
		"after DeleteStagingParts, parts must be gone")

	// Delete is a no-op when nothing exists.
	require.NoError(t, s.DeleteStagingParts(ctx, hash))
}

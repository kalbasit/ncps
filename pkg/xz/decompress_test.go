package xz_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	xzpkg "github.com/ulikunitz/xz"

	"github.com/kalbasit/ncps/pkg/xz"
)

// generateValidXZ creates a valid XZ compressed payload for "hello world".
func generateValidXZ(t *testing.T) []byte {
	t.Helper()

	var buf bytes.Buffer

	xw, err := xzpkg.NewWriter(&buf)
	require.NoError(t, err)

	_, err = io.WriteString(xw, "hello world")
	require.NoError(t, err)

	require.NoError(t, xw.Close())

	return buf.Bytes()
}

func testDecompressorBehavior(t *testing.T, name string, fn xz.DecompressorFn) {
	t.Run(name, func(t *testing.T) {
		t.Parallel()

		t.Run("Valid stream", func(t *testing.T) {
			t.Parallel()

			input := generateValidXZ(t)
			rc, err := fn(context.Background(), bytes.NewReader(input))
			require.NoError(t, err, "should not error on valid xz stream")

			defer rc.Close()

			output, err := io.ReadAll(rc)
			require.NoError(t, err, "should read till EOF without error")
			assert.Equal(t, "hello world", string(output))
		})

		t.Run("Invalid stream (plaintext)", func(t *testing.T) {
			t.Parallel()

			input := []byte("this is not an xz stream")
			_, err := fn(context.Background(), bytes.NewReader(input))
			require.Error(t, err, "should return an error immediately when parsing the header")
		})

		t.Run("Empty stream", func(t *testing.T) {
			t.Parallel()

			input := []byte{}
			_, err := fn(context.Background(), bytes.NewReader(input))
			require.Error(t, err, "should return an error immediately for empty stream")
		})
	})
}

func TestDecompressors(t *testing.T) {
	t.Parallel()

	testDecompressorBehavior(t, "decompressCommand", xz.DecompressCommand)
	testDecompressorBehavior(t, "decompressInternal", xz.DecompressInternal)
}

package nar_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/pierrec/lz4/v4"
	"github.com/sorairolake/lzip-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ulikunitz/xz"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/zstd"
)

func TestDecompressReader(t *testing.T) {
	t.Parallel()

	content := []byte("hello world")

	// Pre-calculated bzip2 blob for "hello world"
	// Created using: echo -n "hello world" | bzip2 | base64
	bzip2Content, err := base64.StdEncoding.DecodeString(
		"QlpoOTFBWSZTWUT3E3gAAAGRgEAABkSQgCAAIgM0hDAhtoFUJ4u5IpwoSCJ7ibwA",
	)
	require.NoError(t, err)

	tests := []struct {
		name        string
		comp        nar.CompressionType
		getInput    func(t *testing.T) io.Reader
		expectError bool
		errorMsg    string
	}{
		{
			name: "None",
			comp: nar.CompressionTypeNone,
			getInput: func(_ *testing.T) io.Reader {
				return bytes.NewReader(content)
			},
		},
		{
			name: "None with ReadCloser",
			comp: nar.CompressionTypeNone,
			getInput: func(_ *testing.T) io.Reader {
				return io.NopCloser(bytes.NewReader(content))
			},
		},
		{
			name: "Empty (defaults to None)",
			comp: nar.CompressionType(""),
			getInput: func(_ *testing.T) io.Reader {
				return bytes.NewReader(content)
			},
		},
		{
			name: "Bzip2",
			comp: nar.CompressionTypeBzip2,
			getInput: func(_ *testing.T) io.Reader {
				return bytes.NewReader(bzip2Content)
			},
		},
		{
			name: "Zstd",
			comp: nar.CompressionTypeZstd,
			getInput: func(t *testing.T) io.Reader {
				var buf bytes.Buffer

				pw := zstd.NewPooledWriter(&buf)
				_, err := pw.Write(content)
				require.NoError(t, err)
				require.NoError(t, pw.Close())

				return &buf
			},
		},
		{
			name: "Lz4",
			comp: nar.CompressionTypeLz4,
			getInput: func(t *testing.T) io.Reader {
				var buf bytes.Buffer

				zw := lz4.NewWriter(&buf)
				_, err := zw.Write(content)
				require.NoError(t, err)
				require.NoError(t, zw.Close())

				return &buf
			},
		},
		{
			name: "Br",
			comp: nar.CompressionTypeBr,
			getInput: func(t *testing.T) io.Reader {
				var buf bytes.Buffer

				zw := brotli.NewWriter(&buf)
				_, err := zw.Write(content)
				require.NoError(t, err)
				require.NoError(t, zw.Close())

				return &buf
			},
		},
		{
			name: "Lzip",
			comp: nar.CompressionTypeLzip,
			getInput: func(t *testing.T) io.Reader {
				var buf bytes.Buffer

				zw := lzip.NewWriter(&buf)
				_, err := zw.Write(content)
				require.NoError(t, err)
				require.NoError(t, zw.Close())

				return &buf
			},
		},
		{
			name: "Xz",
			comp: nar.CompressionTypeXz,
			getInput: func(t *testing.T) io.Reader {
				var buf bytes.Buffer

				zw, err := xz.NewWriter(&buf)
				require.NoError(t, err)
				_, err = zw.Write(content)
				require.NoError(t, err)
				require.NoError(t, zw.Close())

				return &buf
			},
		},
		{
			name: "Unsupported",
			comp: nar.CompressionType("unknown"),
			getInput: func(_ *testing.T) io.Reader {
				return bytes.NewReader(content)
			},
			expectError: true,
			errorMsg:    "unsupported compression type: unknown",
		},
		{
			name: "Lzip invalid data",
			comp: nar.CompressionTypeLzip,
			getInput: func(_ *testing.T) io.Reader {
				return bytes.NewReader([]byte("invalid lzip data"))
			},
			expectError: true,
			errorMsg:    "failed to create lzip reader",
		},
		{
			name: "Xz invalid data",
			comp: nar.CompressionTypeXz,
			getInput: func(_ *testing.T) io.Reader {
				return bytes.NewReader([]byte("invalid xz data"))
			},
			expectError: true,
			errorMsg:    "invalid header magic bytes",
		},
	}

	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r, err := nar.DecompressReader(ctx, tt.getInput(t), tt.comp)

			var got []byte
			// If creation succeeded, the error might happen during Read or Close
			if err == nil {
				// We must attempt to read the data to trigger the asynchronous xz failure
				var readErr error

				got, readErr = io.ReadAll(r)

				// Close the reader to wait for the process to exit and grab its exit code/stderr
				closeErr := r.Close()

				// Aggregate the error (prefer the read error, fallback to the close error)
				if readErr != nil {
					err = readErr
				} else if closeErr != nil {
					err = closeErr
				}
			}

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				require.NoError(t, err)
				assert.Equal(t, content, got)
			}
		})
	}
}

func TestCloserWrapper(t *testing.T) {
	t.Parallel()

	content := []byte("hello")
	cw := io.NopCloser(bytes.NewReader(content))

	got, err := io.ReadAll(cw)
	require.NoError(t, err)
	assert.Equal(t, content, got)

	err = cw.Close()
	assert.NoError(t, err)
}

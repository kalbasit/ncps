package zstd_test

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/zstd"
)

func TestGetAndPutWriter(t *testing.T) {
	t.Parallel()

	// Get a writer from the pool
	writer1 := zstd.GetWriter()
	require.NotNil(t, writer1)

	// Reset and put it back
	zstd.PutWriter(writer1)

	// Get another writer - should be the same instance if pool reuses
	writer2 := zstd.GetWriter()
	require.NotNil(t, writer2)

	zstd.PutWriter(writer2)
}

func TestGetAndPutReader(t *testing.T) {
	t.Parallel()

	// Get a reader from the pool
	reader1 := zstd.GetReader()
	require.NotNil(t, reader1)

	// Reset and put it back
	zstd.PutReader(reader1)

	// Get another reader - should be the same instance if pool reuses
	reader2 := zstd.GetReader()
	require.NotNil(t, reader2)

	zstd.PutReader(reader2)
}

func TestPutWriterWithNil(t *testing.T) {
	t.Parallel()

	// Should not panic when putting nil
	zstd.PutWriter(nil)
}

func TestPutReaderWithNil(t *testing.T) {
	t.Parallel()

	// Should not panic when putting nil
	zstd.PutReader(nil)
}

func TestPooledWriter(t *testing.T) {
	t.Parallel()

	data := []byte("Hello, World!")

	var buf bytes.Buffer

	writer := zstd.NewPooledWriter(&buf)
	require.NotNil(t, writer)

	// Write data
	n, err := writer.Write(data)
	require.NoError(t, err)
	assert.Equal(t, len(data), n)

	// Close the writer
	err = writer.Close()
	require.NoError(t, err)

	// Verify data was compressed
	assert.NotEmpty(t, buf.Bytes())
}

func TestPooledWriterCloseMultiple(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	writer := zstd.NewPooledWriter(&buf)
	require.NotNil(t, writer)

	// Close should be idempotent
	err := writer.Close()
	require.NoError(t, err)

	// Second close should not panic
	err = writer.Close()
	require.NoError(t, err)
}

func TestPooledReader(t *testing.T) {
	t.Parallel()

	// First, create and zstd some data
	originalData := []byte("Hello,  Reader!")

	var compressed bytes.Buffer

	writer := zstd.NewPooledWriter(&compressed)
	require.NotNil(t, writer)

	_, err := writer.Write(originalData)
	require.NoError(t, err)

	err = writer.Close()
	require.NoError(t, err)

	// Now dezstd using pooled reader
	reader, err := zstd.NewPooledReader(bytes.NewReader(compressed.Bytes()))
	require.NoError(t, err)
	require.NotNil(t, reader)

	// Read all data
	decompressed, err := io.ReadAll(reader)
	require.NoError(t, err)

	assert.Equal(t, originalData, decompressed)

	// Close the reader
	err = reader.Close()
	require.NoError(t, err)
}

func TestPooledReaderCloseMultiple(t *testing.T) {
	t.Parallel()

	// Compress some data first
	originalData := []byte("test data")

	var compressed bytes.Buffer

	writer := zstd.NewPooledWriter(&compressed)
	_, err := writer.Write(originalData)
	require.NoError(t, err)
	err = writer.Close()
	require.NoError(t, err)

	// Create pooled reader
	reader, err := zstd.NewPooledReader(bytes.NewReader(compressed.Bytes()))
	require.NoError(t, err)

	// Read the data before closing
	_, err = io.ReadAll(reader)
	require.NoError(t, err)

	// Close multiple times should not panic
	err = reader.Close()
	require.NoError(t, err)

	err = reader.Close()
	require.NoError(t, err)
}

func TestPooledReaderInvalidData(t *testing.T) {
	t.Parallel()

	// Try to read from invalid zstd data
	invalidData := []byte("not compressed data")
	reader, err := zstd.NewPooledReader(bytes.NewReader(invalidData))
	// This should not error on creation, but on read
	if err != nil {
		// If error occurs during Reset, that's also acceptable
		return
	}

	require.NotNil(t, reader)

	// Reading should fail with invalid data
	_, err = io.ReadAll(reader)
	require.Error(t, err)

	reader.Close()
}

func TestPooledReaderWithNilDecoder(t *testing.T) {
	t.Parallel()

	// Create a pooled reader and close it without using it
	originalData := []byte("test")

	var compressed bytes.Buffer

	writer := zstd.NewPooledWriter(&compressed)
	_, err := writer.Write(originalData)
	require.NoError(t, err)
	err = writer.Close()
	require.NoError(t, err)

	reader, err := zstd.NewPooledReader(bytes.NewReader(compressed.Bytes()))
	require.NoError(t, err)

	// Manually set to nil to test Close with nil decoder
	reader.Decoder = nil
	err = reader.Close()
	require.NoError(t, err)
}

func TestWriterPoolReuse(t *testing.T) {
	t.Parallel()

	// Test that pool actually reuses instances
	writer1 := zstd.GetWriter()
	ptr1 := writer1

	zstd.PutWriter(writer1)

	writer2 := zstd.GetWriter()
	ptr2 := writer2

	zstd.PutWriter(writer2)

	// In most cases they should be the same pointer (pool reuse)
	// But this is not guaranteed, so we just verify we can use them
	assert.NotNil(t, ptr1)
	assert.NotNil(t, ptr2)
}

func TestReaderPoolReuse(t *testing.T) {
	t.Parallel()

	// Test that pool actually reuses instances
	reader1 := zstd.GetReader()
	ptr1 := reader1

	zstd.PutReader(reader1)

	reader2 := zstd.GetReader()
	ptr2 := reader2

	zstd.PutReader(reader2)

	// In most cases they should be the same pointer (pool reuse)
	// But this is not guaranteed, so we just verify we can use them
	assert.NotNil(t, ptr1)
	assert.NotNil(t, ptr2)
}

func TestPooledWriterAndReaderRoundTrip(t *testing.T) {
	t.Parallel()

	testCases := []string{
		"Hello, World!",
		"",
		"a",
		"The quick brown fox jumps over the lazy dog",
		"Multiple\nlines\nof\ntext",
	}

	for _, testData := range testCases {
		t.Run(testData, func(t *testing.T) {
			t.Parallel()

			// Compress
			var compressed bytes.Buffer

			writer := zstd.NewPooledWriter(&compressed)
			require.NotNil(t, writer)

			n, err := writer.Write([]byte(testData))
			require.NoError(t, err)
			assert.Equal(t, len(testData), n)

			err = writer.Close()
			require.NoError(t, err)

			// DecompressReader
			reader, err := zstd.NewPooledReader(bytes.NewReader(compressed.Bytes()))
			require.NoError(t, err)
			require.NotNil(t, reader)

			decompressed, err := io.ReadAll(reader)
			require.NoError(t, err)

			assert.Equal(t, testData, string(decompressed))

			err = reader.Close()
			require.NoError(t, err)
		})
	}
}

func TestPooledWriterEncodeAllPattern(t *testing.T) {
	t.Parallel()

	testData := []byte("test data for encode all pattern")

	// Test the EncodeAll pattern used in chunk storage
	writer := zstd.GetWriter()
	compressed := writer.EncodeAll(testData, nil)
	zstd.PutWriter(writer)

	// Verify the compressed data can be decompressed
	reader, err := zstd.NewPooledReader(bytes.NewReader(compressed))
	require.NoError(t, err)

	decompressed, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())

	assert.Equal(t, testData, decompressed)
}

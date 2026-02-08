package chunker_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/chunker"
)

var errRead = errors.New("read error")

type errorReader struct {
	data []byte
	err  error
}

func (r *errorReader) Read(p []byte) (n int, err error) {
	if len(r.data) > 0 {
		n = copy(p, r.data)
		r.data = r.data[n:]

		return n, nil
	}

	return 0, r.err
}

func TestCDCChunker_Chunk_ErrorRace(t *testing.T) {
	t.Parallel()

	// We want to ensure that if an error occurs, it's ALWAYS returned,
	// even if the chunks channel is also closed.

	ctx := context.Background()
	chr, err := chunker.NewCDCChunker(1024, 2048, 4096)
	require.NoError(t, err)

	// Run many times to increase chance of hitting the race if it exists
	for i := 0; i < 10000; i++ {
		reader := &errorReader{
			data: make([]byte, 1024), // At least one chunk
			err:  errRead,
		}

		chunks, err := collectChunks(ctx, t, chr, reader)
		for _, c := range chunks {
			c.Free()
		}

		if !errors.Is(err, errRead) {
			t.Fatalf("at iteration %d: expected error %v, got %v", i, errRead, err)
		}
	}
}

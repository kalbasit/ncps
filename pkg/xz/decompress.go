package xz

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
)

var (
	// ErrDecompressorNotInitialized is returned when the decompressor is not initialized.
	ErrDecompressorNotInitialized = errors.New("decompressor not initialized")

	//nolint:gochecknoglobals // Used by other packages to decompress data.
	decompress atomic.Pointer[DecompressorFn]
)

//nolint:gochecknoinits // Initialize the decompressor with the internal decompressor.
func init() { store(decompressInternal) }

type DecompressorFn func(context.Context, io.Reader) (io.ReadCloser, error)

func Decompress(ctx context.Context, r io.Reader) (io.ReadCloser, error) {
	fn, err := load()
	if err != nil {
		return nil, err
	}

	return fn(ctx, r)
}

func load() (DecompressorFn, error) {
	fnPtr := decompress.Load()
	if fnPtr == nil || *fnPtr == nil {
		return nil, ErrDecompressorNotInitialized
	}

	return *fnPtr, nil
}

func store(fn DecompressorFn) {
	decompress.Store(&fn)
}

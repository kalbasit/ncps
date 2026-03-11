package nar

import (
	"compress/bzip2"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/andybalholm/brotli"
	"github.com/pierrec/lz4/v4"
	"github.com/sorairolake/lzip-go"

	"github.com/kalbasit/ncps/pkg/helper"
	"github.com/kalbasit/ncps/pkg/xz"
	"github.com/kalbasit/ncps/pkg/zstd"
)

// ErrUnsupportedCompressionType is returned when an unsupported compression type is encountered.
var ErrUnsupportedCompressionType = errors.New("unsupported compression type")

// DecompressReader returns a Reader that decompresses the data from r using the given compression type.
// The caller is responsible for closing the returned ReadCloser.
func DecompressReader(ctx context.Context, r io.Reader, comp CompressionType) (io.ReadCloser, error) {
	var dr io.ReadCloser

	switch comp {
	case CompressionTypeNone, CompressionType(""):
		if rc, ok := r.(io.ReadCloser); ok {
			return rc, nil
		}

		return io.NopCloser(r), nil

	case CompressionTypeBzip2:
		dr = io.NopCloser(bzip2.NewReader(r))

	case CompressionTypeZstd:
		pr, err := zstd.NewPooledReader(r)
		if err != nil {
			return nil, fmt.Errorf("failed to create zstd reader: %w", err)
		}

		dr = pr

	case CompressionTypeLz4:
		dr = io.NopCloser(lz4.NewReader(r))

	case CompressionTypeBr:
		dr = io.NopCloser(brotli.NewReader(r))

	case CompressionTypeLzip:
		lr, err := lzip.NewReader(r)
		if err != nil {
			return nil, fmt.Errorf("failed to create lzip reader: %w", err)
		}

		dr = io.NopCloser(lr)

	case CompressionTypeXz:
		var err error

		dr, err = xz.Decompress(ctx, r)
		if err != nil {
			return nil, err
		}

	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedCompressionType, comp)
	}

	// If the input reader is an io.ReadCloser, ensure it's closed when the
	// decompression reader is closed.
	if rc, ok := r.(io.ReadCloser); ok && dr != nil {
		return helper.NewMultiReadCloser(dr, dr, rc), nil
	}

	return dr, nil
}

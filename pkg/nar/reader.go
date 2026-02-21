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

	"github.com/kalbasit/ncps/pkg/xz"
	"github.com/kalbasit/ncps/pkg/zstd"
)

// ErrUnsupportedCompressionType is returned when an unsupported compression type is encountered.
var ErrUnsupportedCompressionType = errors.New("unsupported compression type")

// DecompressReader returns a Reader that decompresses the data from r using the given compression type.
// The caller is responsible for closing the returned ReadCloser.
func DecompressReader(ctx context.Context, r io.Reader, comp CompressionType) (io.ReadCloser, error) {
	switch comp {
	case CompressionTypeNone, CompressionType(""):
		if rc, ok := r.(io.ReadCloser); ok {
			return rc, nil
		}

		return io.NopCloser(r), nil

	case CompressionTypeBzip2:
		return io.NopCloser(bzip2.NewReader(r)), nil

	case CompressionTypeZstd:
		pr, err := zstd.NewPooledReader(r)
		if err != nil {
			return nil, fmt.Errorf("failed to create zstd reader: %w", err)
		}

		return pr, nil

	case CompressionTypeLz4:
		return io.NopCloser(lz4.NewReader(r)), nil

	case CompressionTypeBr:
		return io.NopCloser(brotli.NewReader(r)), nil

	case CompressionTypeLzip:
		lr, err := lzip.NewReader(r)
		if err != nil {
			return nil, fmt.Errorf("failed to create lzip reader: %w", err)
		}

		return io.NopCloser(lr), nil

	case CompressionTypeXz:
		return xz.Decompress(ctx, r)

	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedCompressionType, comp)
	}
}

package nar

import (
	"compress/bzip2"
	"errors"
	"fmt"
	"io"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
	"github.com/sorairolake/lzip-go"
	"github.com/ulikunitz/xz"
)

// ErrUnsupportedCompressionType is returned when an unsupported compression type is encountered.
var ErrUnsupportedCompressionType = errors.New("unsupported compression type")

// DecompressReader returns a Reader that decompresses the data from r using the given compression type.
// The caller is responsible for closing the returned ReadCloser.
func DecompressReader(r io.Reader, comp CompressionType) (io.ReadCloser, error) {
	switch comp {
	case CompressionTypeNone, CompressionType(""):
		if rc, ok := r.(io.ReadCloser); ok {
			return rc, nil
		}

		return io.NopCloser(r), nil

	case CompressionTypeBzip2:
		return io.NopCloser(bzip2.NewReader(r)), nil

	case CompressionTypeZstd:
		zr, err := zstd.NewReader(r)
		if err != nil {
			return nil, fmt.Errorf("failed to create zstd reader: %w", err)
		}

		return zr.IOReadCloser(), nil

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
		xr, err := xz.NewReader(r)
		if err != nil {
			return nil, fmt.Errorf("failed to create xz reader: %w", err)
		}

		return io.NopCloser(xr), nil

	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedCompressionType, comp)
	}
}

package xz

import (
	"context"
	"io"

	"github.com/ulikunitz/xz"
)

func UseInternal() {
	store(decompressInternal)
}

func decompressInternal(_ context.Context, r io.Reader) (io.ReadCloser, error) {
	xr, err := xz.NewReader(r)
	if err != nil {
		return nil, err
	}

	return io.NopCloser(xr), nil
}

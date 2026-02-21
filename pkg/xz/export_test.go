package xz

import (
	"context"
	"io"
)

func DecompressCommand(ctx context.Context, r io.Reader) (io.ReadCloser, error) {
	p, err := getXZBin()
	if err != nil {
		return nil, err
	}

	return decompressCommand(p)(ctx, r)
}

func DecompressInternal(ctx context.Context, r io.Reader) (io.ReadCloser, error) {
	return decompressInternal(ctx, r)
}

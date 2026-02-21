package xz

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// ErrXZBinAbsPath is returned when XZ_BINARY_PATH is not an absolute path.
var ErrXZBinAbsPath = errors.New("XZ_BINARY_PATH must be an absolute path")

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

func getXZBin() (string, error) {
	if p := os.Getenv("XZ_BINARY_PATH"); p != "" {
		if !filepath.IsAbs(p) {
			return "", ErrXZBinAbsPath
		}

		return p, nil
	}

	return exec.LookPath("xz")
}

package xz

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ulikunitz/xz"
)

// ErrXZBinAbsPath is returned when XZ_BIN is not an absolute path.
var ErrXZBinAbsPath = errors.New("XZ_BIN must be an absolute path")

//nolint:gochecknoinits // Initialize Decompress variable once.
func init() {
	Decompress = computeDecompressFn()
}

func computeDecompressFn() DecompressorFn {
	if os.Getenv("FORCE_USE_INTERNAL_XZ") != "" {
		return decompressInternal
	}

	p, err := getXZBin()
	if err == nil {
		return decompressCommand(p)
	}

	return decompressInternal
}

func getXZBin() (string, error) {
	if p := os.Getenv("XZ_BIN"); p != "" {
		if !filepath.IsAbs(p) {
			return "", ErrXZBinAbsPath
		}

		return p, nil
	}

	return exec.LookPath("xz")
}

// Decompress is a function that decompresses data using the system's xz binary,
// if found, if not it uses the ulikunitz/xz library.
//
//nolint:gochecknoglobals // Used by other packages to decompress data.
var Decompress DecompressorFn

type DecompressorFn func(context.Context, io.Reader) (io.ReadCloser, error)

type xzReadCloser struct {
	reader io.Reader
	stdout io.ReadCloser
	cmd    *exec.Cmd
	stderr *bytes.Buffer

	waitOnce sync.Once
	waitErr  error
}

func (x *xzReadCloser) Read(p []byte) (n int, err error) {
	n, err = x.reader.Read(p)
	if err == io.EOF {
		x.waitOnce.Do(func() {
			x.waitErr = x.cmd.Wait()
		})

		if x.waitErr != nil {
			return n, fmt.Errorf("xz decompression failed: %w, stderr: %s", x.waitErr, x.stderr.String())
		}
	}

	return n, err
}

func (x *xzReadCloser) Close() error {
	// Close stdout first to signal the reader is done
	closeErr := x.stdout.Close()
	if closeErr != nil && (errors.Is(closeErr, os.ErrClosed) ||
		errors.Is(closeErr, os.ErrInvalid) ||
		strings.Contains(closeErr.Error(), "file already closed")) {
		closeErr = nil
	}

	// Wait for the command to finish and get the exit status
	x.waitOnce.Do(func() {
		x.waitErr = x.cmd.Wait()
	})

	if x.waitErr != nil {
		// Return the captured stderr to explain WHY it failed
		return fmt.Errorf("xz decompression failed: %w, stderr: %s", x.waitErr, x.stderr.String())
	}

	return closeErr
}

// decompressCommand streams the decompression using the system's xz binary.
func decompressCommand(path string) DecompressorFn {
	return func(ctx context.Context, r io.Reader) (io.ReadCloser, error) {
		cmd := exec.CommandContext(ctx, path, "-d", "-c")
		cmd.Stdin = r

		var stderr bytes.Buffer

		cmd.Stderr = &stderr

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
		}

		if err := cmd.Start(); err != nil {
			return nil, fmt.Errorf("failed to start xz process: %w", err)
		}

		// we peek 1 byte from stdout. If xz fails immediately (e.g. invalid stream),
		// Peek will return an error or EOF, and we can check Wait() synchronously.
		br := bufio.NewReader(stdout)
		_, peekErr := br.Peek(1)

		xrc := &xzReadCloser{
			reader: br,
			stdout: stdout,
			cmd:    cmd,
			stderr: &stderr,
		}

		if peekErr != nil {
			// If we got an error (like EOF), the command might have exited.
			xrc.waitOnce.Do(func() {
				xrc.waitErr = cmd.Wait()
			})

			if xrc.waitErr != nil {
				return nil, fmt.Errorf("xz decompression failed: %w, stderr: %s", xrc.waitErr, stderr.String())
			}
			// If waitErr is nil, it was just a valid empty stream.
		}

		return xrc, nil
	}
}

func decompressInternal(_ context.Context, r io.Reader) (io.ReadCloser, error) {
	xr, err := xz.NewReader(r)
	if err != nil {
		return nil, err
	}

	return io.NopCloser(xr), nil
}

package chunk

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/kalbasit/ncps/pkg/helper"
	"github.com/kalbasit/ncps/pkg/zstd"
)

// localReadCloser wraps a pooled zstd reader and file to properly close both on Close().
type localReadCloser struct {
	*zstd.PooledReader
	file *os.File
}

func (r *localReadCloser) Close() error {
	_ = r.PooledReader.Close()

	return r.file.Close()
}

// localStore implements Store for local filesystem.
type localStore struct {
	baseDir string
}

// NewLocalStore returns a new local chunk store.
func NewLocalStore(baseDir string) (Store, error) {
	s := &localStore{
		baseDir: baseDir,
	}
	// Ensure base directory exists
	if err := os.MkdirAll(s.storeDir(), 0o755); err != nil {
		return nil, fmt.Errorf("failed to create chunk store directory: %w", err)
	}

	return s, nil
}

func (s *localStore) storeDir() string {
	return filepath.Join(s.baseDir, "chunk")
}

func (s *localStore) chunkPath(hash string) (string, error) {
	fp, err := helper.FilePathWithSharding(hash)
	if err != nil {
		return "", fmt.Errorf("chunkPath hash=%q: %w", hash, err)
	}

	return filepath.Join(s.storeDir(), fp), nil
}

func (s *localStore) HasChunk(_ context.Context, hash string) (bool, error) {
	path, err := s.chunkPath(hash)
	if err != nil {
		return false, err
	}

	_, err = os.Stat(path)
	if err == nil {
		return true, nil
	}

	if os.IsNotExist(err) {
		return false, nil
	}

	return false, err
}

func (s *localStore) GetChunk(_ context.Context, hash string) (io.ReadCloser, error) {
	path, err := s.chunkPath(hash)
	if err != nil {
		return nil, err
	}

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}

		return nil, err
	}

	// Use pooled reader instead of creating new instance
	pr, err := zstd.NewPooledReader(f)
	if err != nil {
		f.Close()

		return nil, fmt.Errorf("failed to create zstd reader: %w", err)
	}

	return &localReadCloser{pr, f}, nil
}

func (s *localStore) PutChunk(_ context.Context, hash string, data []byte) (bool, int64, error) {
	path, err := s.chunkPath(hash)
	if err != nil {
		return false, 0, err
	}

	// Create parent directory
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, 0, err
	}

	// Use pooled encoder
	enc := zstd.GetWriter()
	defer zstd.PutWriter(enc)

	// Compress data with zstd
	compressed := enc.EncodeAll(data, nil)

	// Write to temporary file first to ensure atomicity
	tmpFile, err := os.CreateTemp(dir, "chunk-*")
	if err != nil {
		return false, 0, err
	}
	defer os.Remove(tmpFile.Name()) // Ensure temp file is cleaned up

	if _, err = tmpFile.Write(compressed); err == nil {
		err = tmpFile.Sync()
	}

	if closeErr := tmpFile.Close(); err == nil {
		err = closeErr
	}

	if err != nil {
		return false, 0, err
	}

	if err := os.Link(tmpFile.Name(), path); err != nil {
		if os.IsExist(err) {
			// Chunk already exists, which is fine. We didn't create it.
			return false, int64(len(compressed)), nil
		}

		return false, 0, err // Some other error
	}

	return true, int64(len(compressed)), nil
}

func (s *localStore) DeleteChunk(_ context.Context, hash string) error {
	path, err := s.chunkPath(hash)
	if err != nil {
		return err
	}

	err = os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	// Attempt to remove parent directories bottom-up.
	// These will fail silently if a directory is not empty, which is the desired behavior.
	dir := filepath.Dir(path)
	for dir != s.storeDir() {
		if os.Remove(dir) != nil {
			// Stop if we can't remove a directory (e.g., it's not empty).
			break
		}

		dir = filepath.Dir(dir)
	}

	//nolint:nilerr // we explicitly ignore errors attempting to remove parent directories
	return nil
}

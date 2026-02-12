package chunk

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/klauspost/compress/zstd"
)

// localReadCloser wraps a zstd decoder and file to properly close both on Close().
type localReadCloser struct {
	*zstd.Decoder
	file *os.File
}

func (r *localReadCloser) Close() error {
	r.Decoder.Close()

	return r.file.Close()
}

// localStore implements Store for local filesystem.
type localStore struct {
	baseDir string
	encoder *zstd.Encoder
	decoder *zstd.Decoder
}

// NewLocalStore returns a new local chunk store.
func NewLocalStore(baseDir string) (Store, error) {
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create zstd encoder: %w", err)
	}

	decoder, err := zstd.NewReader(nil)
	if err != nil {
		encoder.Close()

		return nil, fmt.Errorf("failed to create zstd decoder: %w", err)
	}

	s := &localStore{
		baseDir: baseDir,
		encoder: encoder,
		decoder: decoder,
	}
	// Ensure base directory exists
	if err := os.MkdirAll(s.storeDir(), 0o755); err != nil {
		encoder.Close()
		decoder.Close()

		return nil, fmt.Errorf("failed to create chunk store directory: %w", err)
	}

	return s, nil
}

func (s *localStore) storeDir() string {
	return filepath.Join(s.baseDir, "chunks")
}

func (s *localStore) chunkPath(hash string) string {
	if len(hash) < 2 {
		return filepath.Join(s.storeDir(), hash)
	}
	// Content-addressable storage with 2-level nesting: chunks/ab/abcdef...
	return filepath.Join(s.storeDir(), hash[:2], hash)
}

func (s *localStore) HasChunk(_ context.Context, hash string) (bool, error) {
	_, err := os.Stat(s.chunkPath(hash))
	if err == nil {
		return true, nil
	}

	if os.IsNotExist(err) {
		return false, nil
	}

	return false, err
}

func (s *localStore) GetChunk(_ context.Context, hash string) (io.ReadCloser, error) {
	f, err := os.Open(s.chunkPath(hash))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}

		return nil, err
	}

	// Create a new decoder for this specific file
	decoder, err := zstd.NewReader(f)
	if err != nil {
		f.Close()

		return nil, fmt.Errorf("failed to create zstd decoder: %w", err)
	}

	return &localReadCloser{decoder, f}, nil
}

func (s *localStore) PutChunk(_ context.Context, hash string, data []byte) (bool, int64, error) {
	path := s.chunkPath(hash)

	// Create parent directory
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, 0, err
	}

	// Compress data with zstd
	compressed := s.encoder.EncodeAll(data, nil)

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
	path := s.chunkPath(hash)

	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	// Attempt to remove parent directory. This will fail if it's not empty,
	// which is the desired behavior. We can ignore the error.
	parentDir := filepath.Dir(path)
	if parentDir != s.storeDir() {
		_ = os.Remove(parentDir)
	}

	return nil
}

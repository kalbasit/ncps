package chunk

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// localStore implements Store for local filesystem.
type localStore struct {
	baseDir string
}

// NewLocalStore returns a new local chunk store.
func NewLocalStore(baseDir string) (Store, error) {
	s := &localStore{baseDir: baseDir}
	// Ensure base directory exists
	if err := os.MkdirAll(s.storeDir(), 0o755); err != nil {
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

	return f, nil
}

func (s *localStore) PutChunk(ctx context.Context, hash string, data []byte) (bool, error) {
	path := s.chunkPath(hash)

	// Check if exists
	if exists, err := s.HasChunk(ctx, hash); err != nil {
		return false, err
	} else if exists {
		return false, nil
	}

	// Create parent directory
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}

	// Write to temporary file first to ensure atomicity
	tmpFile, err := os.CreateTemp(filepath.Dir(path), "chunk-*")
	if err != nil {
		return false, err
	}

	// Defer removal of the temp file on failure.
	var successful bool

	defer func() {
		if !successful {
			os.Remove(tmpFile.Name())
		}
	}()

	if _, err = tmpFile.Write(data); err == nil {
		err = tmpFile.Sync()
	}

	if closeErr := tmpFile.Close(); err == nil {
		err = closeErr
	}

	if err != nil {
		return false, err
	}

	// Rename to final path
	if err := os.Rename(tmpFile.Name(), path); err != nil {
		return false, err
	}

	successful = true

	return true, nil
}

func (s *localStore) DeleteChunk(_ context.Context, hash string) error {
	err := os.Remove(s.chunkPath(hash))
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}

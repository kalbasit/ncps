package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/nix-community/go-nix/pkg/narinfo/signature"
	"github.com/rs/zerolog"

	"github.com/kalbasit/ncps/pkg/helper"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage"
)

var (
	// ErrPathMustBeAbsolute is returned if the given path to New was not absolute.
	ErrPathMustBeAbsolute = errors.New("path must be absolute")

	// ErrPathMustExist is returned if the given path to New did not exist.
	ErrPathMustExist = errors.New("path must exist")

	// ErrPathMustBeADirectory is returned if the given path to New is not a directory.
	ErrPathMustBeADirectory = errors.New("path must be a directory")

	// ErrPathMustBeWritable is returned if the given path to New is not writable.
	ErrPathMustBeWritable = errors.New("path must be writable")
)

const (
	fileMode = 0o400
	dirMode  = 0o700
)

// Store represents a local store and implements storage.Store.
type Store struct{ path string }

func New(ctx context.Context, path string) (*Store, error) {
	if err := validatePath(ctx, path); err != nil {
		return nil, err
	}

	s := &Store{path: path}

	if err := s.setupDirs(); err != nil {
		return nil, fmt.Errorf("error setting up the store directory: %w", err)
	}

	return s, nil
}

// GetSecretKey returns secret key from the store.
func (s *Store) GetSecretKey(_ context.Context) (signature.SecretKey, error) {
	skPath := s.secretKeyPath()

	if _, err := os.Stat(skPath); os.IsNotExist(err) {
		return signature.SecretKey{}, storage.ErrNotFound
	}

	skc, err := os.ReadFile(skPath)
	if err != nil {
		return signature.SecretKey{}, fmt.Errorf("error reading the secret: %w", err)
	}

	return signature.LoadSecretKey(string(skc))
}

// PutSecretKey stores the secret key in the store.
func (s *Store) PutSecretKey(_ context.Context, sk signature.SecretKey) error {
	skPath := s.secretKeyPath()

	if _, err := os.Stat(skPath); err == nil {
		return storage.ErrAlreadyExists
	}

	return os.WriteFile(skPath, []byte(sk.String()), fileMode)
}

// DeleteSecretKey deletes the secret key in the store.
func (s *Store) DeleteSecretKey(_ context.Context) error {
	skPath := s.secretKeyPath()

	if _, err := os.Stat(skPath); os.IsNotExist(err) {
		return storage.ErrNotFound
	}

	return os.Remove(skPath)
}

// HasNarInfo returns true if the store has the narinfo.
func (s *Store) HasNarInfo(_ context.Context, hash string) bool {
	narInfoPath := filepath.Join(s.storeNarInfoPath(), helper.NarInfoFilePath(hash))

	_, err := os.Stat(narInfoPath)

	return err == nil
}

// GetNarInfo returns narinfo from the store.
func (s *Store) GetNarInfo(_ context.Context, hash string) (*narinfo.NarInfo, error) {
	narInfoPath := filepath.Join(s.storeNarInfoPath(), helper.NarInfoFilePath(hash))

	nif, err := os.Open(narInfoPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, storage.ErrNotFound
		}

		return nil, fmt.Errorf("error opening the narinfo file %q: %w", narInfoPath, err)
	}

	defer nif.Close()

	return narinfo.Parse(nif)
}

// PutNarInfo puts the narinfo in the store.
func (s *Store) PutNarInfo(_ context.Context, hash string, narInfo *narinfo.NarInfo) error {
	narInfoPath := filepath.Join(s.storeNarInfoPath(), helper.NarInfoFilePath(hash))

	if err := os.MkdirAll(filepath.Dir(narInfoPath), dirMode); err != nil {
		return fmt.Errorf("error creating the directories for %q: %w", narInfoPath, err)
	}

	nif, err := os.OpenFile(narInfoPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fileMode)
	if err != nil {
		if os.IsExist(err) {
			return storage.ErrAlreadyExists
		}

		return fmt.Errorf("error opening the narinfo file for writing %q: %w", narInfoPath, err)
	}

	defer nif.Close()

	if _, err := nif.WriteString(narInfo.String()); err != nil {
		return fmt.Errorf("error writing the narinfo to %q: %w", narInfoPath, err)
	}

	return nil
}

// DeleteNarInfo deletes the narinfo from the store.
func (s *Store) DeleteNarInfo(_ context.Context, hash string) error {
	narInfoPath := filepath.Join(s.storeNarInfoPath(), helper.NarInfoFilePath(hash))

	if err := os.Remove(narInfoPath); err != nil {
		if os.IsNotExist(err) {
			return storage.ErrNotFound
		}

		return fmt.Errorf("error deleting narinfo %q from store: %w", narInfoPath, err)
	}

	return nil
}

// HasNar returns true if the store has the nar.
func (s *Store) HasNar(_ context.Context, narURL nar.URL) bool {
	narPath := filepath.Join(s.storeNarPath(), narURL.ToFilePath())

	_, err := os.Stat(narPath)

	return err == nil
}

// GetNar returns nar from the store.
// NOTE: The caller must close the returned io.ReadCloser!
func (s *Store) GetNar(_ context.Context, narURL nar.URL) (int64, io.ReadCloser, error) {
	narPath := filepath.Join(s.storeNarPath(), narURL.ToFilePath())

	info, err := os.Stat(narPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil, storage.ErrNotFound
		}

		return 0, nil, fmt.Errorf("error stat'ing the nar file %q: %w", narPath, err)
	}

	nf, err := os.Open(narPath)
	if err != nil {
		return 0, nil, fmt.Errorf("error opening the nar file %q: %w", narPath, err)
	}

	return info.Size(), nf, nil
}

// PutNar puts the nar in the store.
func (s *Store) PutNar(_ context.Context, narURL nar.URL, body io.Reader) (int64, error) {
	narPath := filepath.Join(s.storeNarPath(), narURL.ToFilePath())

	if _, err := os.Stat(narPath); err == nil {
		return 0, storage.ErrAlreadyExists
	}

	pattern := narURL.Hash + "-*.nar"
	if cext := narURL.Compression.String(); cext != "" {
		pattern += "." + cext
	}

	f, err := os.CreateTemp(s.storeTMPPath(), pattern)
	if err != nil {
		return 0, fmt.Errorf("error creating the temporary directory: %w", err)
	}

	written, err := io.Copy(f, body)
	if err != nil {
		f.Close()
		os.Remove(f.Name())

		return 0, fmt.Errorf("error writing the nar to the temporary file: %w", err)
	}

	if err := f.Close(); err != nil {
		return 0, fmt.Errorf("error closing the temporary file: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(narPath), dirMode); err != nil {
		return 0, fmt.Errorf("error creating the directories for %q: %w", narPath, err)
	}

	if err := os.Rename(f.Name(), narPath); err != nil {
		return 0, fmt.Errorf("error creating the nar file %q: %w", narPath, err)
	}

	return written, os.Chmod(narPath, fileMode)
}

// DeleteNar deletes the nar from the store.
func (s *Store) DeleteNar(_ context.Context, narURL nar.URL) error {
	narPath := filepath.Join(s.storeNarPath(), narURL.ToFilePath())

	if err := os.Remove(narPath); err != nil {
		if os.IsNotExist(err) {
			return storage.ErrNotFound
		}

		return fmt.Errorf("error deleting nar %q from store: %w", narPath, err)
	}

	return nil
}

func (s *Store) configPath() string       { return filepath.Join(s.path, "config") }
func (s *Store) secretKeyPath() string    { return filepath.Join(s.configPath(), "cache.key") }
func (s *Store) storePath() string        { return filepath.Join(s.path, "store") }
func (s *Store) storeNarInfoPath() string { return filepath.Join(s.storePath(), "narinfo") }
func (s *Store) storeNarPath() string     { return filepath.Join(s.storePath(), "nar") }
func (s *Store) storeTMPPath() string     { return filepath.Join(s.storePath(), "tmp") }

func (s *Store) setupDirs() error {
	if err := os.RemoveAll(s.storeTMPPath()); err != nil {
		return fmt.Errorf("error removing the temporary download directory: %w", err)
	}

	allPaths := []string{
		s.configPath(),
		s.storePath(),
		s.storeNarInfoPath(),
		s.storeNarPath(),
		s.storeTMPPath(),
	}

	for _, p := range allPaths {
		if err := os.MkdirAll(p, dirMode); err != nil {
			return fmt.Errorf("error creating the directory %q: %w", p, err)
		}
	}

	return nil
}

func validatePath(ctx context.Context, path string) error {
	log := zerolog.Ctx(ctx)

	if !filepath.IsAbs(path) {
		log.Error().Str("path", path).Msg("path is not absolute")

		return ErrPathMustBeAbsolute
	}

	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		log.Error().Str("path", path).Msg("path does not exist")

		return ErrPathMustExist
	}

	if !info.IsDir() {
		log.Error().Str("path", path).Msg("path is not a directory")

		return ErrPathMustBeADirectory
	}

	if !isWritable(ctx, path) {
		return ErrPathMustBeWritable
	}

	return nil
}

func isWritable(ctx context.Context, path string) bool {
	log := zerolog.Ctx(ctx)

	tmpFile, err := os.CreateTemp(path, "write_test")
	if err != nil {
		log.Error().
			Err(err).
			Str("path", path).
			Msg("error writing a temp file in the path")

		return false
	}

	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	return true
}

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

	// ErrNoSecretKey is returned if no secret key is present.
	ErrNoSecretKey = errors.New("no secret key was found")
)

const (
	secretKeyFileMode = 0o400
	dirsFileMode      = 0o700
)

// Store represents a local store and implements storage.Store.
type Store struct {
	path string
}

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
func (s *Store) GetSecretKey(ctx context.Context) (signature.SecretKey, error) {
	skPath := s.secretKeyPath()

	if _, err := os.Stat(skPath); os.IsNotExist(err) {
		return signature.SecretKey{}, ErrNoSecretKey
	}

	skc, err := os.ReadFile(skPath)
	if err != nil {
		return signature.SecretKey{}, fmt.Errorf("error reading the secret: %w", err)
	}

	return signature.LoadSecretKey(string(skc))
}

// PutSecretKey stores the secret key in the store.
func (s *Store) PutSecretKey(ctx context.Context, sk signature.SecretKey) error {
	skPath := s.secretKeyPath()

	if _, err := os.Stat(skPath); err == nil {
		return storage.ErrAlreadyExists
	}

	return os.WriteFile(skPath, []byte(sk.String()), secretKeyFileMode)
}

// DeleteSecretKey deletes the secret key in the store.
func (s *Store) DeleteSecretKey(ctx context.Context) error {
	skPath := s.secretKeyPath()

	if _, err := os.Stat(skPath); os.IsNotExist(err) {
		return ErrNoSecretKey
	}

	return os.Remove(skPath)
}

// GetNarInfo returns narinfo from the store.
func (s *Store) GetNarInfo(ctx context.Context, hash string) (*narinfo.NarInfo, error) {
	nif, err := os.Open(filepath.Join(s.storeNarInfoPath(), helper.NarInfoFilePath(hash)))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, storage.ErrNotFound
		}

		return nil, fmt.Errorf("error opening the narinfo: %w", err)
	}

	return narinfo.Parse(nif)
}

// PutNarInfo puts the narinfo in the store.
func (s *Store) PutNarInfo(ctx context.Context, hash string, narInfo *narinfo.NarInfo) error {
	return errors.New("not implemented")
}

// DeleteNarInfo deletes the narinfo from the store.
func (s *Store) DeleteNarInfo(ctx context.Context, hash string) error {
	return errors.New("not implemented")
}

// GetNar returns nar from the store.
func (s *Store) GetNar(ctx context.Context, narURL nar.URL) (int64, io.ReadCloser, error) {
	return 0, nil, errors.New("not implemented")
}

// PutNar puts the nar in the store.
func (s *Store) PutNar(ctx context.Context, narURL nar.URL, body io.ReadCloser) error {
	return errors.New("not implemented")
}

// DeleteNar deletes the nar from the store.
func (s *Store) DeleteNar(ctx context.Context, narURL nar.URL) error {
	return errors.New("not implemented")
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
		if err := os.MkdirAll(p, dirsFileMode); err != nil {
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

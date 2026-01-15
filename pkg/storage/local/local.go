package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/nix-community/go-nix/pkg/narinfo/signature"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/kalbasit/ncps/pkg/helper"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage"
)

const (
	fileMode        = 0o400
	dirMode         = 0o700
	otelPackageName = "github.com/kalbasit/ncps/pkg/storage/local"
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

	//nolint:gochecknoglobals
	tracer trace.Tracer
)

//nolint:gochecknoinits
func init() {
	tracer = otel.Tracer(otelPackageName)
}

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

	_, span := tracer.Start(
		ctx,
		"local.GetSecretKey",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("secret_key_path", skPath),
		),
	)
	defer span.End()

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
func (s *Store) PutSecretKey(ctx context.Context, sk signature.SecretKey) error {
	skPath := s.secretKeyPath()

	_, span := tracer.Start(
		ctx,
		"local.PutSecretKey",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("secret_key_path", skPath),
		),
	)
	defer span.End()

	if _, err := os.Stat(skPath); err == nil {
		return storage.ErrAlreadyExists
	}

	return os.WriteFile(skPath, []byte(sk.String()), fileMode)
}

// DeleteSecretKey deletes the secret key in the store.
func (s *Store) DeleteSecretKey(ctx context.Context) error {
	skPath := s.secretKeyPath()

	_, span := tracer.Start(
		ctx,
		"local.DeleteSecretKey",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("secret_key_path", skPath),
		),
	)
	defer span.End()

	if _, err := os.Stat(skPath); os.IsNotExist(err) {
		return storage.ErrNotFound
	}

	return os.Remove(skPath)
}

// HasNarInfo returns true if the store has the narinfo.
func (s *Store) HasNarInfo(ctx context.Context, hash string) bool {
	narInfoPath := filepath.Join(s.storeNarInfoPath(), helper.NarInfoFilePath(hash))

	_, span := tracer.Start(
		ctx,
		"local.HasNarInfo",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("narinfo_hash", hash),
			attribute.String("narinfo_path", narInfoPath),
		),
	)
	defer span.End()

	_, err := os.Stat(narInfoPath)

	return err == nil
}

// GetNarInfo returns narinfo from the store.
func (s *Store) GetNarInfo(ctx context.Context, hash string) (*narinfo.NarInfo, error) {
	narInfoPath := filepath.Join(s.storeNarInfoPath(), helper.NarInfoFilePath(hash))

	_, span := tracer.Start(
		ctx,
		"local.GetNarInfo",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("narinfo_hash", hash),
			attribute.String("narinfo_path", narInfoPath),
		),
	)
	defer span.End()

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
func (s *Store) PutNarInfo(ctx context.Context, hash string, narInfo *narinfo.NarInfo) error {
	narInfoPath := filepath.Join(s.storeNarInfoPath(), helper.NarInfoFilePath(hash))

	_, span := tracer.Start(
		ctx,
		"local.PutNarInfo",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("narinfo_hash", hash),
			attribute.String("narinfo_path", narInfoPath),
		),
	)
	defer span.End()

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
func (s *Store) DeleteNarInfo(ctx context.Context, hash string) error {
	narInfoPath := filepath.Join(s.storeNarInfoPath(), helper.NarInfoFilePath(hash))

	_, span := tracer.Start(
		ctx,
		"local.DeleteNarInfo",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("narinfo_hash", hash),
			attribute.String("narinfo_path", narInfoPath),
		),
	)
	defer span.End()

	if err := os.Remove(narInfoPath); err != nil {
		if os.IsNotExist(err) {
			return storage.ErrNotFound
		}

		return fmt.Errorf("error deleting narinfo %q from store: %w", narInfoPath, err)
	}

	return nil
}

// HasNar returns true if the store has the nar.
func (s *Store) HasNar(ctx context.Context, narURL nar.URL) bool {
	narPath := filepath.Join(s.storeNarPath(), narURL.ToFilePath())

	_, span := tracer.Start(
		ctx,
		"local.HasNar",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("nar_url", narURL.String()),
			attribute.String("nar_path", narPath),
		),
	)
	defer span.End()

	_, err := os.Stat(narPath)

	return err == nil
}

// GetNar returns nar from the store.
// NOTE: The caller must close the returned io.ReadCloser!
func (s *Store) GetNar(ctx context.Context, narURL nar.URL) (int64, io.ReadCloser, error) {
	narPath := filepath.Join(s.storeNarPath(), narURL.ToFilePath())

	_, span := tracer.Start(
		ctx,
		"local.GetNar",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("nar_url", narURL.String()),
			attribute.String("nar_path", narPath),
		),
	)
	defer span.End()

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
func (s *Store) PutNar(ctx context.Context, narURL nar.URL, body io.Reader) (int64, error) {
	narPath := filepath.Join(s.storeNarPath(), narURL.ToFilePath())

	_, span := tracer.Start(
		ctx,
		"local.PutNar",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("nar_url", narURL.String()),
			attribute.String("nar_path", narPath),
		),
	)
	defer span.End()

	if _, err := os.Stat(narPath); err == nil {
		return 0, storage.ErrAlreadyExists
	}

	if err := os.MkdirAll(filepath.Dir(narPath), dirMode); err != nil {
		return 0, fmt.Errorf("error creating the directories for %q: %w", narPath, err)
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

	if err := os.Rename(f.Name(), narPath); err != nil {
		return 0, fmt.Errorf("error creating the nar file %q: %w", narPath, err)
	}

	return written, os.Chmod(narPath, fileMode)
}

// DeleteNar deletes the nar from the store.
func (s *Store) DeleteNar(ctx context.Context, narURL nar.URL) error {
	narPath := filepath.Join(s.storeNarPath(), narURL.ToFilePath())

	_, span := tracer.Start(
		ctx,
		"local.DeleteNar",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("nar_url", narURL.String()),
			attribute.String("nar_path", narPath),
		),
	)
	defer span.End()

	if err := os.Remove(narPath); err != nil {
		if os.IsNotExist(err) {
			return storage.ErrNotFound
		}

		return fmt.Errorf("error deleting nar %q from store: %w", narPath, err)
	}

	return nil
}

// HasFile returns true if the store has the file at the given path.
func (s *Store) HasFile(ctx context.Context, path string) bool {
	filePath, err := s.sanitizePath(path)
	if err != nil {
		return false
	}

	_, span := tracer.Start(
		ctx,
		"local.HasFile",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("path", path),
			attribute.String("file_path", filePath),
		),
	)
	defer span.End()

	_, err = os.Stat(filePath)

	return err == nil
}

// GetFile returns the file from the store at the given path.
// NOTE: The caller must close the returned io.ReadCloser!
func (s *Store) GetFile(ctx context.Context, path string) (int64, io.ReadCloser, error) {
	filePath, err := s.sanitizePath(path)
	if err != nil {
		return 0, nil, err
	}

	_, span := tracer.Start(
		ctx,
		"local.GetFile",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("path", path),
			attribute.String("file_path", filePath),
		),
	)
	defer span.End()

	info, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil, storage.ErrNotFound
		}

		return 0, nil, fmt.Errorf("error stating the file %q: %w", filePath, err)
	}

	f, err := os.Open(filePath)
	if err != nil {
		return 0, nil, fmt.Errorf("error opening the file %q: %w", filePath, err)
	}

	return info.Size(), f, nil
}

// PutFile puts the file in the store at the given path.
func (s *Store) PutFile(ctx context.Context, path string, body io.Reader) (int64, error) {
	filePath, err := s.sanitizePath(path)
	if err != nil {
		return 0, err
	}

	_, span := tracer.Start(
		ctx,
		"local.PutFile",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("path", path),
			attribute.String("file_path", filePath),
		),
	)
	defer span.End()

	if err := os.MkdirAll(filepath.Dir(filePath), dirMode); err != nil {
		return 0, fmt.Errorf("error creating the directories for %q: %w", filePath, err)
	}

	f, err := os.CreateTemp(s.storeTMPPath(), filepath.Base(path)+"-*")
	if err != nil {
		return 0, fmt.Errorf("error creating the temporary file: %w", err)
	}

	written, err := io.Copy(f, body)
	if err != nil {
		f.Close()
		os.Remove(f.Name())

		return 0, fmt.Errorf("error writing to the temporary file: %w", err)
	}

	if err := f.Close(); err != nil {
		return 0, fmt.Errorf("error closing the temporary file: %w", err)
	}

	if err := os.Rename(f.Name(), filePath); err != nil {
		return 0, fmt.Errorf("error moving the file to %q: %w", filePath, err)
	}

	if err := os.Chmod(filePath, fileMode); err != nil {
		return 0, fmt.Errorf("error changing mode of %q: %w", filePath, err)
	}

	return written, nil
}

// DeleteFile deletes the file from the store at the given path.
func (s *Store) DeleteFile(ctx context.Context, path string) error {
	filePath, err := s.sanitizePath(path)
	if err != nil {
		return err
	}

	_, span := tracer.Start(
		ctx,
		"local.DeleteFile",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("path", path),
			attribute.String("file_path", filePath),
		),
	)
	defer span.End()

	if err := os.Remove(filePath); err != nil {
		if os.IsNotExist(err) {
			return storage.ErrNotFound
		}

		return fmt.Errorf("error deleting file %q: %w", filePath, err)
	}

	return nil
}

func (s *Store) configPath() string    { return filepath.Join(s.path, "config") }
func (s *Store) secretKeyPath() string { return filepath.Join(s.configPath(), "cache.key") }
func (s *Store) storePath() string     { return filepath.Join(s.path, "store") }

func (s *Store) sanitizePath(path string) (string, error) {
	// Sanitize path to prevent traversal.
	relativePath := strings.TrimPrefix(path, "/")
	filePath := filepath.Join(s.storePath(), relativePath)

	// Final check to ensure the path is within the store directory.
	if !strings.HasPrefix(filePath, s.storePath()) {
		return "", storage.ErrNotFound
	}

	return filePath, nil
}

func (s *Store) storeNarInfoPath() string { return filepath.Join(s.storePath(), "narinfo") }
func (s *Store) storeNarPath() string     { return filepath.Join(s.storePath(), "nar") }
func (s *Store) storeTMPPath() string     { return filepath.Join(s.storePath(), "tmp") }

func (s *Store) setupDirs() error {
	// RemoveAll is safe to call on non-existent directories
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

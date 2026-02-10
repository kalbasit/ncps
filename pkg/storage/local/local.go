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

	"github.com/nix-community/go-nix/pkg/narinfo/signature"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	narinfopkg "github.com/nix-community/go-nix/pkg/narinfo"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/narinfo"
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
	if err := os.MkdirAll(s.configPath(), dirMode); err != nil {
		return fmt.Errorf("error creating the directory %q: %w", s.configPath(), err)
	}

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

	if err := os.Remove(skPath); err != nil {
		return err
	}

	// Best-effort cleanup of empty parent directories
	removeEmptyParentDirs(ctx, skPath, s.configPath())

	return nil
}

// HasNarInfo returns true if the store has the narinfo.
func (s *Store) HasNarInfo(ctx context.Context, hash string) bool {
	nifP, err := narinfo.FilePath(hash)
	if err != nil {
		return false
	}

	narInfoPath := filepath.Join(s.storeNarInfoPath(), nifP)

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

	_, err = os.Stat(narInfoPath)

	return err == nil
}

// WalkNarInfos walks all narinfos in the store and calls fn for each one.
func (s *Store) WalkNarInfos(ctx context.Context, fn func(hash string) error) error {
	root := s.storeNarInfoPath()

	_, span := tracer.Start(
		ctx,
		"local.WalkNarInfos",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("root", root),
		),
	)
	defer span.End()

	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil
	}

	return filepath.Walk(root, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		if filepath.Ext(path) != ".narinfo" {
			return nil
		}

		// path is something like .../store/narinfo/h/ha/hash.narinfo
		// we want hash
		fileName := filepath.Base(path)
		hash := strings.TrimSuffix(fileName, ".narinfo")

		return fn(hash)
	})
}

// GetNarInfo returns narinfo from the store.
func (s *Store) GetNarInfo(ctx context.Context, hash string) (*narinfopkg.NarInfo, error) {
	nifP, err := narinfo.FilePath(hash)
	if err != nil {
		return nil, err
	}

	narInfoPath := filepath.Join(s.storeNarInfoPath(), nifP)

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

	return narinfopkg.Parse(nif)
}

// PutNarInfo puts the narinfo in the store.
func (s *Store) PutNarInfo(ctx context.Context, hash string, narInfo *narinfopkg.NarInfo) error {
	nifP, err := narinfo.FilePath(hash)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(s.storeNarInfoPath(), dirMode); err != nil {
		return fmt.Errorf("error creating the directories for %q: %w", s.storeNarInfoPath(), err)
	}

	narInfoPath := filepath.Join(s.storeNarInfoPath(), nifP)

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
	nifP, err := narinfo.FilePath(hash)
	if err != nil {
		return err
	}

	narInfoPath := filepath.Join(s.storeNarInfoPath(), nifP)

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

	// Best-effort cleanup of empty parent directories
	removeEmptyParentDirs(ctx, narInfoPath, s.storeNarInfoPath())

	return nil
}

// HasNar returns true if the store has the nar.
func (s *Store) HasNar(ctx context.Context, narURL nar.URL) bool {
	tfp, err := narURL.ToFilePath()
	if err != nil {
		return false
	}

	narPath := filepath.Join(s.storeNarPath(), tfp)

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

	_, err = os.Stat(narPath)

	return err == nil
}

// GetNar returns nar from the store.
// NOTE: The caller must close the returned io.ReadCloser!
func (s *Store) GetNar(ctx context.Context, narURL nar.URL) (int64, io.ReadCloser, error) {
	tfp, err := narURL.ToFilePath()
	if err != nil {
		return 0, nil, err
	}

	narPath := filepath.Join(s.storeNarPath(), tfp)

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
	tfp, err := narURL.ToFilePath()
	if err != nil {
		return 0, err
	}

	narPath := filepath.Join(s.storeNarPath(), tfp)

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
	tfp, err := narURL.ToFilePath()
	if err != nil {
		return err
	}

	narPath := filepath.Join(s.storeNarPath(), tfp)

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

	// Best-effort cleanup of empty parent directories
	removeEmptyParentDirs(ctx, narPath, s.storeNarPath())

	return nil
}

// removeEmptyParentDirs removes empty parent directories up to and including categoryDir.
// It starts from the parent of filePath and walks up the directory tree,
// removing directories only if they are empty.
// It stops when it reaches categoryDir (which is also removed if empty) or when
// a directory is not empty.
func removeEmptyParentDirs(ctx context.Context, filePath, categoryDir string) {
	// Start from the parent directory of the file
	currentDir := filepath.Dir(filePath)

	for {
		// Check if we've reached above the category directory
		// If currentDir is not within categoryDir, we're done
		rel, err := filepath.Rel(categoryDir, currentDir)
		if err != nil || strings.HasPrefix(rel, "..") {
			// We've gone above categoryDir, stop
			break
		}

		// Try to remove the current directory (only succeeds if empty)
		if err := os.Remove(currentDir); err != nil {
			// Directory is not empty or we don't have permissions, stop here
			// This is expected behavior, not an error
			// Log the error for debugging purposes.
			zerolog.Ctx(ctx).
				Debug().
				Err(err).
				Str("dir", currentDir).
				Msg("failed to remove parent directory, stopping cleanup")

			break
		}

		// If we successfully removed the directory, move up to its parent
		// But stop if we just removed the category directory itself
		if currentDir == categoryDir {
			break
		}

		currentDir = filepath.Dir(currentDir)
	}
}

func (s *Store) configPath() string       { return filepath.Join(s.path, "config") }
func (s *Store) secretKeyPath() string    { return filepath.Join(s.configPath(), "cache.key") }
func (s *Store) storePath() string        { return filepath.Join(s.path, "store") }
func (s *Store) storeNarInfoPath() string { return filepath.Join(s.storePath(), "narinfo") }
func (s *Store) storeNarPath() string     { return filepath.Join(s.storePath(), "nar") }
func (s *Store) storeTMPPath() string     { return filepath.Join(s.storePath(), "tmp") }

func (s *Store) setupDirs() error {
	// RemoveAll is safe to call on non-existent directories
	if err := os.RemoveAll(s.storeTMPPath()); err != nil {
		return fmt.Errorf("error removing the temporary download directory: %w", err)
	}

	allPaths := []string{
		s.storePath(),
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

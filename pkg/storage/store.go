package storage

import (
	"context"
	"errors"
	"io"

	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/nix-community/go-nix/pkg/narinfo/signature"

	"github.com/kalbasit/ncps/pkg/nar"
)

var (
	// ErrNotFound is returned if the nar or narinfo were not found.
	ErrNotFound = errors.New("not found")

	// ErrAlreadyExists is returned the store already has a file with the
	// same name.
	ErrAlreadyExists = errors.New("file already exists")
)

// ConfigStore represents a store for the ncps to use for storing
// configurations.
//
// Deprecated: The configuration is now stored in the database. This interface
// will be removed in a future release.
type ConfigStore interface {
	// GetSecretKey returns secret key from the store.
	//
	// Deprecated: Use config.GetSecretKey instead.
	GetSecretKey(ctx context.Context) (signature.SecretKey, error)

	// PutSecretKey stores the secret key in the store.
	//
	// Deprecated: Use config.SetSecretKey instead.
	PutSecretKey(ctx context.Context, sk signature.SecretKey) error

	// DeleteSecretKey deletes the secret key in the store.
	//
	// Deprecated: The secret key is stored in the database.
	DeleteSecretKey(ctx context.Context) error
}

// NarInfoStore represents a store capable of storing narinfos.
type NarInfoStore interface {
	// HasNarInfo returns true if the store has the narinfo.
	HasNarInfo(ctx context.Context, hash string) bool

	// GetNarInfo returns narinfo from the store.
	GetNarInfo(ctx context.Context, hash string) (*narinfo.NarInfo, error)

	// PutNarInfo puts the narinfo in the store.
	PutNarInfo(ctx context.Context, hash string, narInfo *narinfo.NarInfo) error

	// DeleteNarInfo deletes the narinfo from the store.
	DeleteNarInfo(ctx context.Context, hash string) error

	// WalkNarInfos walks all narinfos in the store and calls fn for each one.
	WalkNarInfos(ctx context.Context, fn func(hash string) error) error
}

// NarStore represents a store capable of storing nars.
type NarStore interface {
	// HasNar returns true if the store has the nar.
	//
	// HasNar collapses every failure mode into false: a confirmed absence and an
	// undeterminable result (e.g. a timed-out or stale stat on a network
	// filesystem) are indistinguishable. Callers that must not treat "could not
	// determine" as "absent" — e.g. before a destructive purge — MUST use StatNar.
	HasNar(ctx context.Context, narURL nar.URL) bool

	// StatNar reports whether the store has the nar, distinguishing a confirmed
	// absence from an undeterminable result:
	//   - (true, nil):  the nar is present.
	//   - (false, nil): the nar is confirmed absent (e.g. ENOENT / NoSuchKey).
	//   - (false, err): presence could not be determined (transient/ambiguous
	//                   error such as an I/O timeout or a stale NFS handle).
	// Callers MUST NOT treat the (false, err) case as a confirmed absence.
	StatNar(ctx context.Context, narURL nar.URL) (bool, error)

	// GetNar returns nar from the store.
	// NOTE: The caller must close the returned io.ReadCloser!
	GetNar(ctx context.Context, narURL nar.URL) (int64, io.ReadCloser, error)

	// PutNar puts the nar in the store.
	// If size > 0, it's the known size of the nar (for efficient streaming).
	// If size <= 0, the size is unknown (e.g., when re-compressing on-the-fly).
	PutNar(ctx context.Context, narURL nar.URL, body io.Reader, size int64) (int64, error)

	// DeleteNar deletes the nar from the store.
	DeleteNar(ctx context.Context, narURL nar.URL) error

	// WalkNars walks all NAR files in the store and calls fn for each one.
	WalkNars(ctx context.Context, fn func(narURL nar.URL) error) error

	// PutStagingPart writes one in-flight staging part-object for a NAR hash at
	// the given zero-based index. Parts are immutable once written. If size > 0
	// it is the known byte length of body. It returns the number of bytes written.
	// See change serve-whole-nar-in-flight.
	PutStagingPart(ctx context.Context, hash string, index int64, body io.Reader, size int64) (int64, error)

	// GetStagingPart opens the staging part-object for hash at index for reading.
	// The caller must close the returned io.ReadCloser. A missing part returns
	// storage.ErrNotFound.
	GetStagingPart(ctx context.Context, hash string, index int64) (io.ReadCloser, error)

	// DeleteStagingParts removes all staging part-objects for hash. It is a no-op
	// when none exist.
	DeleteStagingParts(ctx context.Context, hash string) error
}

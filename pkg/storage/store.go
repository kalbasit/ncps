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
)

// ConfigStore represents a store for the ncps to use for storing
// configurations.
type ConfigStore interface {
	// GetSecretKey returns secret key from the store.
	GetSecretKey(ctx context.Context) (signature.SecretKey, error)

	// PutSecretKey stores the secret key in the store.
	PutSecretKey(ctx context.Context, sk signature.SecretKey) error

	// DeleteSecretKey deletes the secret key in the store.
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
}

// NarStore represents a store capable of storing nars.
type NarStore interface {
	// HasNar returns true if the store has the nar.
	HasNar(ctx context.Context, narURL nar.URL) bool

	// GetNar returns nar from the store.
	// NOTE: The caller must close the returned io.ReadCloser!
	GetNar(ctx context.Context, narURL nar.URL) (int64, io.ReadCloser, error)

	// PutNar puts the nar in the store.
	PutNar(ctx context.Context, narURL nar.URL, body io.Reader) (int64, error)

	// DeleteNar deletes the nar from the store.
	DeleteNar(ctx context.Context, narURL nar.URL) error
}

// Store represents a store capable of storing narinfos and nars.
type Store struct {
	ConfigStore
	NarInfoStore
	NarStore
}

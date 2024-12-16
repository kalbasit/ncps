package local

import (
	"context"
	"errors"
	"io"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/nix-community/go-nix/pkg/narinfo/signature"
)

// Store represents a local store and implements storage.Store.
type Store struct {
	path string
}

func New(path string) (*Store, error) {
	return nil, errors.New("not implemented")
}

// GetSecretKey returns secret key from the store.
func (s *Store) GetSecretKey(ctx context.Context) (signature.SecretKey, error) {
	return signature.SecretKey{}, errors.New("not implemented")
}

// PutSecretKey stores the secret key in the store.
func (s *Store) PutSecretKey(ctx context.Context, sk signature.SecretKey) error {
	return errors.New("not implemented")
}

// DeleteSecretKey deletes the secret key in the store.
func (s *Store) DeleteSecretKey(ctx context.Context) error {
	return errors.New("not implemented")
}

// GetNarInfo returns narinfo from the store.
func (s *Store) GetNarInfo(ctx context.Context, hash string) (*narinfo.NarInfo, error) {
	return nil, errors.New("not implemented")
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

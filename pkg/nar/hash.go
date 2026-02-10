package nar

import (
	"errors"
	"regexp"
)

var (
	// ErrInvalidHash is returned if the hash is not valid.
	ErrInvalidHash = errors.New("invalid nar hash")

	// narHashPattern defines the valid characters for a nar hash.
	//nolint:gochecknoglobals // This is used in other regexes to ensure they validate the same thing.
	narHashPattern = `[a-z0-9]+`

	narHashRegexp = regexp.MustCompile(`^(` + narHashPattern + `)$`)
)

func ValidateHash(hash string) error {
	if !narHashRegexp.MatchString(hash) {
		return ErrInvalidHash
	}

	return nil
}

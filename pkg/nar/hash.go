package nar

import (
	"errors"
	"regexp"

	"github.com/kalbasit/ncps/pkg/narinfo"
)

// NormalizedHashPattern defines the valid characters for a Nix32 encoded hash.
// Nix32 uses a 32-character alphabet excluding 'e', 'o', 'u', and 't'.
// Valid characters: 0-9, a-d, f-n, p-s, v-z
// Hashes must be exactly 52 characters long.
const NormalizedHashPattern = `[0-9a-df-np-sv-z]{52}`

const HashPattern = `(` + narinfo.HashPattern + `-)?` + NormalizedHashPattern

var (
	// ErrInvalidHash is returned if the hash is not valid.
	ErrInvalidHash = errors.New("invalid nar hash")

	narHashRegexp = regexp.MustCompile(`^(` + HashPattern + `)$`)
)

func ValidateHash(hash string) error {
	if !narHashRegexp.MatchString(hash) {
		return ErrInvalidHash
	}

	return nil
}

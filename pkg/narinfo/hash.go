package narinfo

import (
	"errors"
	"regexp"
)

// narInfoHashPattern defines the valid characters for a Nix32 encoded hash.
// Nix32 uses a 32-character alphabet excluding 'e', 'o', 'u', and 't'.
// Valid characters: 0-9, a-d, f-n, p-s, v-z
// Hashes must be exactly 32 characters long.
const HashPattern = `[0-9a-df-np-sv-z]{32}`

var (
	// ErrInvalidHash is returned if the hash is invalid.
	ErrInvalidHash = errors.New("invalid narinfo hash")

	// hashRegexp is used to validate hashes.
	hashRegexp = regexp.MustCompile(`^` + HashPattern + `$`)
)

// ValidateHash validates the given hash according to Nix32 encoding requirements.
// A valid hash must:
// - Be exactly 32 characters long
// - Contain only characters from the Nix32 alphabet ('0'-'9', 'a'-'z' excluding 'e', 'o', 'u', 't').
func ValidateHash(hash string) error {
	if !hashRegexp.MatchString(hash) {
		return ErrInvalidHash
	}

	return nil
}

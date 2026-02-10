package narinfo

import (
	"errors"
	"regexp"
)

var (
	// ErrInvalidHash is returned if the hash is invalid.
	ErrInvalidHash = errors.New("invalid narinfo hash")

	// narInfoHashPattern defines the valid characters for a narinfo hash.
	//nolint:gochecknoglobals // This is used in other regexes to ensure they validate the same thing.
	narInfoHashPattern = `[a-z0-9]+`

	// hashRegexp is used to validate hashes.
	hashRegexp = regexp.MustCompile(`^` + narInfoHashPattern + `$`)
)

// ValidateHash validates the given hash.
func ValidateHash(hash string) error {
	if !hashRegexp.MatchString(hash) {
		return ErrInvalidHash
	}

	return nil
}

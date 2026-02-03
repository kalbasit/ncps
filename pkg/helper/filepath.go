package helper

import (
	"errors"
	"path/filepath"
	"regexp"
)

var (
	// ErrInvalidHash is returned if the hash is invalid.
	ErrInvalidHash = errors.New("invalid hash")

	// hashRegexp is used to validate hashes.
	hashRegexp = regexp.MustCompile(`^[a-z0-9]+$`)
)

// ValidateHash validates the given hash.
func ValidateHash(hash string) error {
	if !hashRegexp.MatchString(hash) {
		return ErrInvalidHash
	}

	return nil
}

// NarInfoFilePath returns the path of the narinfo file given a hash.
func NarInfoFilePath(hash string) (string, error) {
	if len(hash) < 3 {
		panic("hash=\"" + hash + "\" is less than 3 characters long")
	}

	if err := ValidateHash(hash); err != nil {
		return "", err
	}

	return FilePathWithSharding(hash + ".narinfo")
}

// NarFilePath returns the path of the nar file given a hash and an optional compression.
func NarFilePath(hash, compression string) (string, error) {
	if len(hash) < 3 {
		panic("hash=\"" + hash + "\" is less than 3 characters long")
	}

	if err := ValidateHash(hash); err != nil {
		return "", err
	}

	fn := hash + ".nar"
	if compression != "" {
		fn += "." + compression
	}

	return FilePathWithSharding(fn)
}

// FilePathWithSharding returns the path to a file with sharding.
func FilePathWithSharding(fn string) (string, error) {
	if len(fn) < 3 {
		panic("fn=\"" + fn + "\" is less than 3 characters long")
	}

	lvl1 := fn[:1]
	lvl2 := fn[:2]

	return filepath.Join(lvl1, lvl2, fn), nil
}

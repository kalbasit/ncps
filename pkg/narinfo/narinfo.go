package narinfo

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/kalbasit/ncps/pkg/helper"
)

var (
	// ErrInvalidHash is returned if the hash is invalid.
	ErrInvalidHash = errors.New("invalid narinfo hash")

	// hashRegexp is used to validate hashes.
hashRegexp = regexp.MustCompile(`^([a-z0-9]{32}|[a-z0-9]{52})$`)
)

// ValidateHash validates the given hash.
func ValidateHash(hash string) error {
	if !hashRegexp.MatchString(hash) {
		return ErrInvalidHash
	}

	return nil
}

// FilePath returns the path of the narinfo file given a hash.
func FilePath(hash string) (string, error) {
	if len(hash) < 3 {
		return "", fmt.Errorf("hash=%q: %w", hash, helper.ErrInputTooShort)
	}

	if err := ValidateHash(hash); err != nil {
		return "", err
	}

	return helper.FilePathWithSharding(hash + ".narinfo")
}

package nar

import (
	"fmt"

	"github.com/kalbasit/ncps/pkg/helper"
)

// FilePath returns the path of the nar file given a hash and an optional compression.
func FilePath(hash, compression string) (string, error) {
	if len(hash) < 3 {
		return "", fmt.Errorf("hash=%q: %w", hash, helper.ErrInputTooShort)
	}

	if err := ValidateHash(hash); err != nil {
		return "", err
	}

	fn := hash + ".nar"
	if compression != "" {
		fn += "." + compression
	}

	return helper.FilePathWithSharding(fn)
}

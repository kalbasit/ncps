package narinfo

import (
	"fmt"

	"github.com/kalbasit/ncps/pkg/helper"
)

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

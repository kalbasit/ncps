package helper

import (
	"errors"
	"fmt"
	"path/filepath"
)

// ErrInputTooShort is returned if the input is less than 3 characters long.
var ErrInputTooShort = errors.New("input is less than 3 characters long")

// FilePathWithSharding returns the path to a file with sharding.
func FilePathWithSharding(fn string) (string, error) {
	if len(fn) < 3 {
		return "", fmt.Errorf("fn=%q: %w", fn, ErrInputTooShort)
	}

	lvl1 := fn[:1]
	lvl2 := fn[:2]

	return filepath.Join(lvl1, lvl2, fn), nil
}

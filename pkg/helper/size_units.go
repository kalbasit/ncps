package helper

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrInvalidSizeSuffix is returned if the suffix is not valid.
var ErrInvalidSizeSuffix = errors.New("invalid size suffix")

// ParseSize parses size with units and returns the same size in bytes.
func ParseSize(str string) (uint64, error) {
	if len(str) < 2 {
		return 0, fmt.Errorf("error parsing the unit for %q: %w", str, ErrInvalidSizeSuffix)
	}

	num, err := strconv.ParseUint(str[:len(str)-1], 10, 64)
	if err != nil {
		return 0, err
	}

	suffix := strings.ToUpper(str[len(str)-1:])
	switch suffix {
	case "B":
		return num, nil
	case "K":
		return num * 1024, nil
	case "M":
		return num * 1024 * 1024, nil
	case "G":
		return num * 1024 * 1024 * 1024, nil
	case "T":
		return num * 1024 * 1024 * 1024 * 1024, nil
	default:
		return 0, fmt.Errorf("error parsing the unit for %q: %w", str, ErrInvalidSizeSuffix)
	}
}

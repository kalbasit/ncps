package nixcacheinfo

import (
	"errors"
	"fmt"
	"strings"
)

// NOTE: Copied from:
// https://github.com/nix-community/go-nix/blob/0327d78224c2de28edd957d2ef4240711217d7fe/pkg/narinfo/parser.go#L109-L120

var (
	// ErrSplitOnceFoundMultipleSeparators is returned if splitOnce encountered multiple separators in the string.
	ErrSplitOnceFoundMultipleSeparators = errors.New("found multiple separators in the string")

	// ErrSplitOnceFoundNoSeparators is returned if splitOnce encountered multiple separators in the string.
	ErrSplitOnceFoundNoSeparators = errors.New("found no separators in the string")
)

// splitOnce - Split a string and make sure it's only splittable once.
func splitOnce(s string, sep string) (string, string, error) {
	idx := strings.Index(s, sep)
	if idx == -1 {
		return "", "", fmt.Errorf("%w: separator=%q string=%q", ErrSplitOnceFoundNoSeparators, sep, s)
	}

	if strings.Contains(s[idx+1:], sep) {
		return "", "", fmt.Errorf("%w: separator=%q string=%q", ErrSplitOnceFoundMultipleSeparators, sep, s)
	}

	return s[0:idx], s[idx+len(sep):], nil
}

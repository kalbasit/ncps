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

// HashPattern is the strict validation pattern for complete nar hashes.
// It matches an optional prefix (narinfo hash + separator) followed by exactly
// a 52-character normalized hash. Used with anchors (^...$) to validate the full input.
// For extraction and lenient parsing, use HashPatternLenient instead.
const HashPattern = `(?:(` + narinfo.HashPattern + `[-_]))?` + NormalizedHashPattern

// HashPatternLenient is used for parsing/extraction. It matches optional prefix
// followed by anything, allowing us to extract and validate parts separately.
const HashPatternLenient = `(?:(` + narinfo.HashPattern + `[-_]))?(.+)`

var (
	// ErrInvalidHash is returned if the hash is not valid.
	ErrInvalidHash = errors.New("invalid nar hash")

	narHashRegexp           = regexp.MustCompile(`^` + HashPattern + `$`)
	narNormalizedHashRegexp = regexp.MustCompile(`^` + NormalizedHashPattern + `$`)
	narHashLenientRegexp    = regexp.MustCompile(`^` + HashPatternLenient + `$`)
)

func ValidateHash(hash string) error {
	if !narHashRegexp.MatchString(hash) {
		return ErrInvalidHash
	}

	return nil
}

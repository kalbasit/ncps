package nar

import (
	"errors"
	"regexp"

	"github.com/kalbasit/ncps/pkg/narinfo"
)

// NormalizedHashPattern defines the valid patterns for Nix store hashes.
// It supports two primary formats:
//  1. Nix32 (Base32): A custom 32-character alphabet (0-9, a-z excluding 'e', 'o', 'u', 't').
//     Used for truncated SHA-256 (52 chars).
//  2. Hexadecimal (Base16): Standard 0-9, a-f.
//     Used for full SHA-256 digests (64 chars).
const NormalizedHashPattern = `(?:[0-9a-df-np-sv-z]{52}|[0-9a-f]{64})`

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

// ValidateHash validates a Nix archive (nar) hash string. It returns
// ErrInvalidHash if the hash does not match the expected pattern. The
// function accepts both the optional narinfo hash prefix and the 52‑ or
// 64‑character normalized hash value, following the definitions in
// NormalizedHashPattern and HashPattern.
func ValidateHash(hash string) error {
	if !narHashRegexp.MatchString(hash) {
		return ErrInvalidHash
	}

	return nil
}

package helper

import "regexp"

// Matches either:
// - Standard NAR hash: 52 lowercase alphanumeric chars
// - Standard narinfo hash: 32 lowercase alphanumeric chars
// - NAR hash with narinfo prefix: 32 chars + dash/underscore + 52 chars (used by nix-serve).
var isValidHashRegexp = regexp.MustCompile(`^([a-z0-9]{32}|[a-z0-9]{52}|[a-z0-9]{32}[-_][a-z0-9]{52})$`)

// NarInfoURLPath returns the path of the narinfo file given a hash.
func NarInfoURLPath(hash string) string { return "/" + hash + ".narinfo" }

// IsValidHash returns true if the hash is valid (32 or 52 lowercase alphanumeric characters).
func IsValidHash(hash string) bool {
	return isValidHashRegexp.MatchString(hash)
}

package helper

import "regexp"

var isValidHashRegexp = regexp.MustCompile(`^([a-z0-9]{32,64})$`)

// NarInfoURLPath returns the path of the narinfo file given a hash.
func NarInfoURLPath(hash string) string { return "/" + hash + ".narinfo" }

// IsValidHash returns true if the hash is valid (32 or 52 lowercase alphanumeric characters).
func IsValidHash(hash string) bool {
	return isValidHashRegexp.MatchString(hash)
}

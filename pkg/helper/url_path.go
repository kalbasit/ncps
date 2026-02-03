package helper

// NarInfoURLPath returns the path of the narinfo file given a hash.
func NarInfoURLPath(hash string) string { return "/" + hash + ".narinfo" }

// IsValidHash returns true if the hash is valid (32 or 52 lowercase alphanumeric characters).
func IsValidHash(hash string) bool {
	if len(hash) != 32 && len(hash) != 52 {
		return false
	}

	for _, r := range hash {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}

	return true
}

package helper

// NarInfoURLPath returns the path of the narinfo file given a hash.
func NarInfoURLPath(hash string) string { return "/" + hash + ".narinfo" }

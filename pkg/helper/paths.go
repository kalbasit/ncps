package helper

// NarInfoPath returns the path of the narinfo file given a hash.
func NarInfoPath(hash string) string { return "/" + hash + ".narinfo" }

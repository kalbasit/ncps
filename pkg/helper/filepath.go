package helper

// NarInfoFilePath returns the path of the narinfo file given a hash.
func NarInfoFilePath(hash string) string {
	return hash + ".narinfo"
}

// NarFilePath returns the path of the nar file given a hash and an optional compression.
func NarFilePath(hash, compression string) string {
	fn := hash + ".nar"
	if compression != "" {
		fn += "." + compression
	}

	return fn
}

package helper

// NarInfoPath returns the path of the narinfo file given a hash.
func NarInfoPath(hash string) string { return "/" + hash + ".narinfo" }

// NarPath returns the path of the nar file given a hash and an optional compression.
func NarPath(hash, compression string) string {
	p := "/nar/" + hash + ".nar"
	if compression != "" {
		p += "." + compression
	}

	return p
}

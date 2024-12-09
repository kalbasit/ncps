package helper

// NarInfoURLPath returns the path of the narinfo file given a hash.
func NarInfoURLPath(hash string) string { return "/" + hash + ".narinfo" }

// NarURLPath returns the path of the nar file given a hash and an optional compression.
func NarURLPath(hash, compression string) string {
	p := "/nar/" + hash + ".nar"
	if compression != "" {
		p += "." + compression
	}

	return p
}

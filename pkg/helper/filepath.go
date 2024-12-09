package helper

import "path/filepath"

// NarInfoFilePath returns the path of the narinfo file given a hash.
func NarInfoFilePath(hash string) string {
	if len(hash) < 3 {
		panic("hash=\"" + hash + "\" is less than 3 characters long")
	}

	return FilePathWithSharding(hash + ".narinfo")
}

// NarFilePath returns the path of the nar file given a hash and an optional compression.
func NarFilePath(hash, compression string) string {
	if len(hash) < 3 {
		panic("hash=\"" + hash + "\" is less than 3 characters long")
	}

	fn := hash + ".nar"
	if compression != "" {
		fn += "." + compression
	}

	return FilePathWithSharding(fn)
}

// FilePathWithSharding returns the path to a file with sharding.
func FilePathWithSharding(fn string) string {
	if len(fn) < 3 {
		panic("fn=\"" + fn + "\" is less than 3 characters long")
	}

	lvl1 := fn[:1]
	lvl2 := fn[:2]

	return filepath.Join(lvl1, lvl2, fn)
}

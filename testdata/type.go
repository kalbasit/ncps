package testdata

import "github.com/kalbasit/ncps/pkg/nar"

type Entry struct {
	NarInfoHash string
	NarInfoPath string
	NarInfoText string

	NarHash        string
	NarCompression nar.CompressionType
	NarPath        string
	NarText        string

	// NoZstdEncoding, when true, causes the test server to ignore
	// Accept-Encoding: zstd and serve raw bytes without Content-Encoding.
	// This simulates nix-serve behavior.
	NoZstdEncoding bool
}

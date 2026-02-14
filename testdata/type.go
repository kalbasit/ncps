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

	// NarInfoNarHash is the NAR hash as it appears in the upstream narinfo URL.
	// For nix-serve upstreams, this includes the narinfo hash prefix (e.g., "narinfohash-narhash").
	// When empty, NarHash is used directly.
	NarInfoNarHash string
}

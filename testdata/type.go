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
}

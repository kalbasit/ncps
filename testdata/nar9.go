package testdata

import (
	"path/filepath"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/testhelper"
)

// Nar9 is the nar replicating #868.
//
//nolint:gochecknoglobals
var Nar9 = Entry{
	NarInfoHash: "yj1wxm9hh8610iyzqnz75kvs6xl8j3my",
	NarInfoPath: filepath.Join("y", "yj", "yj1wxm9hh8610iyzqnz75kvs6xl8j3my.narinfo"),
	//nolint:lll
	NarInfoText: `StorePath: /nix/store/yj1wxm9hh8610iyzqnz75kvs6xl8j3my-source
URL: nar/504dd9a697cdfa0ddc22552ca66b3322afd52beee44642419d0fff83bb9071e2.nar.zst
Compression: zstd
FileHash: sha256:504dd9a697cdfa0ddc22552ca66b3322afd52beee44642419d0fff83bb9071e2
FileSize: 1126
NarHash: sha256:1bzg89hgcr2gvza35vqi4n1jbb2gz1yg4b8p7gry4ihsj2mnnbap
NarSize: 2440
Sig: nix-community.cachix.org-1:wKpIFGNnM2hyGkGUaX+qhMhYzEwMio7o4RayDDD/BUF0eOOzp2aRzFWFSgRoqyJf7DygEIGFHPuk1gFDbhyJBQ==`,

	NarHash:        "504dd9a697cdfa0ddc22552ca66b3322afd52beee44642419d0fff83bb9071e2",
	NarCompression: nar.CompressionTypeZstd,
	NarPath: filepath.Join(
		"5",
		"50",
		"504dd9a697cdfa0ddc22552ca66b3322afd52beee44642419d0fff83bb9071e2.nar.xz",
	),
	NarText: testhelper.MustRandString(1126),
}

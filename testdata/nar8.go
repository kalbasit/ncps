package testdata

import (
	"path/filepath"

	"github.com/kalbasit/ncps/pkg/helper"
	"github.com/kalbasit/ncps/pkg/nar"
)

// Nar8 is the nar representing hello from release-25.05.
//
//nolint:gochecknoglobals
var Nar8 = Entry{
	NarInfoHash: "swxfvpa96x0qc9v2g7jvil2301dflvhg",
	NarInfoPath: filepath.Join("s", "sw", "swxfvpa96x0qc9v2g7jvil2301dflvhg.narinfo"),
	NarInfoText: `StorePath: /nix/store/swxfvpa96x0qc9v2g7jvil2301dflvhg-hello-2.12.1
URL: nar/1hng5pfbqi227z363yjr73rrk3v4064yx6b0c6vn9z6b4px032y3.nar.xz
Compression: xz
FileHash: sha256:1hng5pfbqi227z363yjr73rrk3v4064yx6b0c6vn9z6b4px032y3
FileSize: 25484
NarHash: sha256:0ps9ym8hmi59q2dah0hgmaqh8k5d7xw67ncn6m78q7ar4pvh48v5
NarSize: 112712
References: 6ib8qg0filxrwsghdpm04f6i7hlvp482-libiconv-109
Deriver: 4pfsbms43j6iqbhjn7j8xzgvjgnrbqkm-hello-2.12.1.drv
Sig: cache.nixos.org-1:VGbeHlz0xW8VHDFYtnu+gBFPQZqaOjgvPq2GF8Z6dweFe4jPGTzzKnBwYM1R2MTOwaYYlTOGhC7QEPMtVTlyBQ==`,

	NarHash:        "1hng5pfbqi227z363yjr73rrk3v4064yx6b0c6vn9z6b4px032y3",
	NarCompression: nar.CompressionTypeXz,
	NarPath:        filepath.Join("1", "1h", "1hng5pfbqi227z363yjr73rrk3v4064yx6b0c6vn9z6b4px032y3.nar.xz"),
	NarText:        helper.MustRandString(25484, nil),
}

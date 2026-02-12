package testdata

import (
	"path/filepath"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/testhelper"
)

// Nar5 is the nar representing hello from release-22.11 with its FileHash
// converted to base16.
//
//nolint:gochecknoglobals
var Nar5 = Entry{
	NarInfoHash: "1gxz5nfzfnhyxjdyzi04r86sh61y4i00",
	NarInfoPath: filepath.Join("1", "1g", "1gxz5nfzfnhyxjdyzi04r86sh61y4i00.narinfo"),
	NarInfoText: `StorePath: /nix/store/1gxz5nfzfnhyxjdyzi04r86sh61y4i00-hello-2.12.1
URL: nar/0fn02ls73n5ndgvvclll1lkg0viq4cbmhx8xcgr5flmzrcvjiarn.nar.xz
Compression: xz
FileHash: sha256:36ab2837cbbf5257f2631d75581723386ef0260d9452b6f76bb6d8713415c03a
FileSize: 50264
NarHash: sha256:1fivi78qzgq3xlm3z59lia9qxw0idwaqmf3ffam83p4392biy5jy
NarSize: 226504
References: 1gxz5nfzfnhyxjdyzi04r86sh61y4i00-hello-2.12.1 vnwdak3n1w2jjil119j65k8mw1z23p84-glibc-2.35-224
Deriver: pf9ff9imvbxb3l4gmav93gbhpx0fj1hv-hello-2.12.1.drv
Sig: cache.nixos.org-1:zfh0dR5lqsbDKBris0zyDThbw1G1Yb0POTiI0QA9OQRd6FskmYUqAJd85CjS/Lm7eREwCdNnAbkytEj/xw14Bw==`,

	NarHash:        "0fn02ls73n5ndgvvclll1lkg0viq4cbmhx8xcgr5flmzrcvjiarn",
	NarCompression: nar.CompressionTypeXz,
	NarPath:        filepath.Join("0", "0f", "0fn02ls73n5ndgvvclll1lkg0viq4cbmhx8xcgr5flmzrcvjiarn.nar.xz"),
	NarText:        testhelper.MustRandString(50264),
}

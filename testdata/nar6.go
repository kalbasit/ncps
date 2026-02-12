package testdata

import (
	"path/filepath"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/testhelper"
)

// Nar6 is the nar representing hello from release-22.04 with its Deriver made
// into unknown-deriver.
//
//nolint:gochecknoglobals,lll
var Nar6 = Entry{
	NarInfoHash: "6lwdzpbig6zz8678blcqr5f5q1caxjw2",
	NarInfoPath: filepath.Join("6", "6l", "6lwdzpbig6zz8678blcqr5f5q1caxjw2.narinfo"),
	NarInfoText: `StorePath: /nix/store/6lwdzpbig6zz8678blcqr5f5q1caxjw2-hello-2.12
URL: nar/1z2a10f88f36n0iqkl831drchx3f04cs96kypjyrj0rrbcpww28n.nar.xz
Compression: xz
FileHash: sha256:1z2a10f88f36n0iqkl831drchx3f04cs96kypjyrj0rrbcpww28n
FileSize: 43624
NarHash: sha256:08n38jlm2m2wlsskaav1mcvsgp42nm7cv8x9yga84l9rgnxsz8lz
NarSize: 181368
References: 6lwdzpbig6zz8678blcqr5f5q1caxjw2-hello-2.12 b2hc0i92l22ir2kavnjn3z5z6mzabbvm-glibc-2.34-210
Deriver: unknown-deriver
Sig: cache.nixos.org-1:J3pwQAMB5Pzi4PpxyTPOugN8sl8cVbU/XjC1WVj5MKwZDIVdUHTV3IwLido9XMHe3xL6sWVdlk76hhQwaMVNDg==
Sig: nix-cache.cluster.nasreddine.com:B+Ceczbz+qLqCqTPbb8PKOyCMaOOA4jEv5VNU0d5RzPxdCLlr5vg8sgqdfAyib/itoRXrW8CSzwHjwHQBnHOBw==`,

	NarHash:        "1z2a10f88f36n0iqkl831drchx3f04cs96kypjyrj0rrbcpww28n",
	NarCompression: nar.CompressionTypeXz,
	NarPath:        filepath.Join("1", "1z", "1z2a10f88f36n0iqkl831drchx3f04cs96kypjyrj0rrbcpww28n.nar.xz"),
	NarText:        testhelper.MustRandString(43624),
}

package testdata

import (
	"path/filepath"

	"github.com/kalbasit/ncps/pkg/helper"
)

// Nar1 is the nar representing hello from release-23.11.
var Nar1 = Entry{
	NarInfoHash: "n5glp21rsz314qssw9fbvfswgy3kc68f",
	NarInfoPath: filepath.Join("n", "n5", "n5glp21rsz314qssw9fbvfswgy3kc68f.narinfo"),
	NarInfoText: `StorePath: /nix/store/n5glp21rsz314qssw9fbvfswgy3kc68f-hello-2.12.1
URL: nar/1lid9xrpirkzcpqsxfq02qwiq0yd70chfl860wzsqd1739ih0nri.nar.xz
Compression: xz
FileHash: sha256:1lid9xrpirkzcpqsxfq02qwiq0yd70chfl860wzsqd1739ih0nri
FileSize: 50160
NarHash: sha256:07kc6swib31psygpmwi8952lvywlpqn474059yxl7grwsvr6k0fj
NarSize: 226552
References: n5glp21rsz314qssw9fbvfswgy3kc68f-hello-2.12.1 qdcbgcj27x2kpxj2sf9yfvva7qsgg64g-glibc-2.38-77
Deriver: 1zpqmcicrg8smi9jlqv6dmd7v20d2fsn-hello-2.12.1.drv
Sig: cache.nixos.org-1:MadTCU1OSFCGUw4aqCKpLCZJpqBc7AbLvO7wgdlls0eq1DwaSnF/82SZE+wJGEiwlHbnZR+14daSaec0W3XoBQ==`,

	NarHash: "1lid9xrpirkzcpqsxfq02qwiq0yd70chfl860wzsqd1739ih0nri",
	NarPath: filepath.Join("1", "1l", "1lid9xrpirkzcpqsxfq02qwiq0yd70chfl860wzsqd1739ih0nri.nar.xz"),
	NarText: helper.MustRandString(50160, nil),
}

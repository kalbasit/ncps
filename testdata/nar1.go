package testdata

import (
	"fmt"
	"strings"

	"github.com/kalbasit/ncps/pkg/helper"
	"github.com/nix-community/go-nix/pkg/narinfo"
)

// Nar1 is the nar representing hello from release-23.11.
var Nar1 = Entry{
	NarInfoHash: "n5glp21rsz314qssw9fbvfswgy3kc68f",
	NarHash:     "1lid9xrpirkzcpqsxfq02qwiq0yd70chfl860wzsqd1739ih0nri",

	NarInfoText: `StorePath: /nix/store/n5glp21rsz314qssw9fbvfswgy3kc68f-hello-2.12.1
URL: nar/1lid9xrpirkzcpqsxfq02qwiq0yd70chfl860wzsqd1739ih0nri.nar.xz
Compression: xz
FileHash: sha256:1lid9xrpirkzcpqsxfq02qwiq0yd70chfl860wzsqd1739ih0nri
FileSize: 50160
NarHash: sha256:07kc6swib31psygpmwi8952lvywlpqn474059yxl7grwsvr6k0fj
NarSize: 226552
References: n5glp21rsz314qssw9fbvfswgy3kc68f-hello-2.12.1 qdcbgcj27x2kpxj2sf9yfvva7qsgg64g-glibc-2.38-77
Deriver: 1zpqmcicrg8smi9jlqv6dmd7v20d2fsn-hello-2.12.1.drv
Sig: cache.nixos.org-1:MadTCU1OSFCGUw4aqCKpLCZJpqBc7AbLvO7wgdlls0eq1DwaSnF/82SZE+wJGEiwlHbnZR+14daSaec0W3XoBQ==
Sig: nix-cache.cluster.nasreddine.com:zcbCC5rjkEtQvM/XckDfBk9+q/XsbEuX/Z0YblZ/THjL5lnQgteQSI2u6fXirdF+bfTv4PvSEbPqeIYIFQNSCA==`,
}

func init() {
	ni, err := narinfo.Parse(strings.NewReader(Nar1.NarInfoText))
	if err != nil {
		panic(fmt.Errorf("error parsing the narinfo: %w", err))
	}

	Nar1.NarText, err = helper.RandString(int(ni.FileSize), nil)
	if err != nil {
		panic(fmt.Errorf("error generating NAR text: %w", err))
	}
}

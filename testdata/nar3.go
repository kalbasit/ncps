package testdata

import (
	"fmt"
	"strings"

	"github.com/kalbasit/ncps/pkg/helper"
	"github.com/nix-community/go-nix/pkg/narinfo"
)

// Nar3 is the nar representing hello from release-24.11.
var Nar3 = Entry{
	NarInfoHash: "1q8w6gl1ll0mwfkqc3c2yx005s6wwfrl",
	NarHash:     "1dglqjx5wm3sdq0ggngcyh4gpcwykngkxps0a8v4v1f1f2lzdwd1",

	NarInfoText: `StorePath: /nix/store/1q8w6gl1ll0mwfkqc3c2yx005s6wwfrl-hello-2.12.1
URL: nar/1dglqjx5wm3sdq0ggngcyh4gpcwykngkxps0a8v4v1f1f2lzdwd1.nar.xz
Compression: xz
FileHash: sha256:1dglqjx5wm3sdq0ggngcyh4gpcwykngkxps0a8v4v1f1f2lzdwd1
FileSize: 50364
NarHash: sha256:1bn7c3bf5z32cdgylhbp9nzhh6ydib5ngsm6mdhsvf233g0nh1ac
NarSize: 226560
References: 1q8w6gl1ll0mwfkqc3c2yx005s6wwfrl-hello-2.12.1 wn7v2vhyyyi6clcyn0s9ixvl7d4d87ic-glibc-2.40-36
Deriver: k1rx7pnkdlzfscv6jqzwl4x89kcknfy1-hello-2.12.1.drv
Sig: cache.nixos.org-1:qt4d4o04/cklIMANVntoLYHh36t1j+y/35qWoK2GeeeEeYU5RElnV/gpXrc5jgx4p2MQ38TasPhHg8rN6O+5Dw==
Sig: nix-cache.cluster.nasreddine.com:vzh0W8CrV0waXOQU7Ai3klxsvsO7vG5CEoi2u4QtN0h0h30MoMbt+cW5R7N5HblUa2ChIz+/xMflrsZiJOriDQ==`,
}

func init() {
	ni, err := narinfo.Parse(strings.NewReader(Nar3.NarInfoText))
	if err != nil {
		panic(fmt.Errorf("error parsing the narinfo: %w", err))
	}

	Nar3.NarText, err = helper.RandString(int(ni.FileSize), nil)
	if err != nil {
		panic(fmt.Errorf("error generating NAR text: %w", err))
	}
}

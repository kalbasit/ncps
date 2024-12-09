package testdata

import (
	"path/filepath"

	"github.com/kalbasit/ncps/pkg/helper"
)

// Nar3 is the nar representing hello from release-24.11.
var Nar3 = Entry{
	NarInfoHash: "1q8w6gl1ll0mwfkqc3c2yx005s6wwfrl",
	NarInfoPath: filepath.Join("1", "1q", "1q8w6gl1ll0mwfkqc3c2yx005s6wwfrl.narinfo"),
	NarInfoText: `StorePath: /nix/store/1q8w6gl1ll0mwfkqc3c2yx005s6wwfrl-hello-2.12.1
URL: nar/1dglqjx5wm3sdq0ggngcyh4gpcwykngkxps0a8v4v1f1f2lzdwd1.nar.xz
Compression: xz
FileHash: sha256:1dglqjx5wm3sdq0ggngcyh4gpcwykngkxps0a8v4v1f1f2lzdwd1
FileSize: 50364
NarHash: sha256:1bn7c3bf5z32cdgylhbp9nzhh6ydib5ngsm6mdhsvf233g0nh1ac
NarSize: 226560
References: 1q8w6gl1ll0mwfkqc3c2yx005s6wwfrl-hello-2.12.1 wn7v2vhyyyi6clcyn0s9ixvl7d4d87ic-glibc-2.40-36
Deriver: k1rx7pnkdlzfscv6jqzwl4x89kcknfy1-hello-2.12.1.drv
Sig: cache.nixos.org-1:qt4d4o04/cklIMANVntoLYHh36t1j+y/35qWoK2GeeeEeYU5RElnV/gpXrc5jgx4p2MQ38TasPhHg8rN6O+5Dw==`,

	NarHash: "1dglqjx5wm3sdq0ggngcyh4gpcwykngkxps0a8v4v1f1f2lzdwd1",
	NarPath: filepath.Join("1", "1d", "1dglqjx5wm3sdq0ggngcyh4gpcwykngkxps0a8v4v1f1f2lzdwd1.nar.xz"),
	NarText: helper.MustRandString(50364, nil),
}

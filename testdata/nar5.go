package testdata

import (
	"path/filepath"

	"github.com/kalbasit/ncps/pkg/helper"
)

// Nar5 is the nar representing a nar from nix-community that was failing to
// parse because the FileHash is encoded in sha256/base-16.
//
//nolint:gochecknoglobals,lll
var Nar5 = Entry{
	NarInfoHash: "7k3i624w0rfrvcrbbdrw0zrvasywxmz4",
	NarInfoPath: filepath.Join("7", "7k", "7k3i624w0rfrvcrbbdrw0zrvasywxmz4.narinfo"),
	NarInfoText: `StorePath: /nix/store/7k3i624w0rfrvcrbbdrw0zrvasywxmz4-check-link-targets.sh
URL: nar/abf8c1a50684c2d706c991f0d332dec9eff89eb3a3c17687141ce1ddb795cc38.nar.zst
Compression: zstd
FileHash: sha256:abf8c1a50684c2d706c991f0d332dec9eff89eb3a3c17687141ce1ddb795cc38
FileSize: 1156
NarHash: sha256:0v4mgmqzp5s7mscgad21nr49svk97y7pq0y49i11k1hii04syj74
NarSize: 2312
References: 7m5x92fbfc3zxqmbkhl5fqqydsmdpggb-hm-modules-messages zhrjg6wxrxmdlpn6iapzpp2z2vylpvw5-home-manager.sh
Deriver: 2w91y8xfpfpamqjzy223i8ivhz4dviwz-check-link-targets.sh.drv
Sig: nix-community.cachix.org-1:RcmngYq9PZMjZVwQdZK8mUmOjmj964GqM18zWkj/Qpw17ns1CmGnYGCrvbj/Q/+K1jU3HbFH9ABtft+3TUgHAA==`,

	NarHash: "abf8c1a50684c2d706c991f0d332dec9eff89eb3a3c17687141ce1ddb795cc38",
	NarPath: filepath.Join("a", "ab", "abf8c1a50684c2d706c991f0d332dec9eff89eb3a3c17687141ce1ddb795cc38.nar.xz"),
	NarText: helper.MustRandString(1156, nil),
}

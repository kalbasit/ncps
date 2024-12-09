package testdata

import (
	"path/filepath"

	"github.com/kalbasit/ncps/pkg/helper"
)

// Nar4 is the nar representing a nar from nix-community that was failing to parse.
var Nar4 = Entry{
	NarInfoHash: "7k3i624w0rfrvcrbbdrw0zrvasywxmz4",
	NarInfoPath: filepath.Join("7", "7k", "7k3i624w0rfrvcrbbdrw0zrvasywxmz4.narinfo"),
	NarInfoText: `StorePath: /nix/store/7k3i624w0rfrvcrbbdrw0zrvasywxmz4-check-link-targets.sh
URL: nar/0b9q5xq96nszj51jnazj20ag9wbl3b9mn1ki73c8l2kjvxw3x1km.nar.xz
Compression: xz
FileHash: sha256:0b9q5xq96nszj51jnazj20ag9wbl3b9mn1ki73c8l2kjvxw3x1km
FileSize: 1164
NarHash: sha256:0v4mgmqzp5s7mscgad21nr49svk97y7pq0y49i11k1hii04syj74
NarSize: 2312
References: 7m5x92fbfc3zxqmbkhl5fqqydsmdpggb-hm-modules-messages zhrjg6wxrxmdlpn6iapzpp2z2vylpvw5-home-manager.sh
Deriver: 2w91y8xfpfpamqjzy223i8ivhz4dviwz-check-link-targets.sh.drv
Sig: nix-community.cachix.org-1:RcmngYq9PZMjZVwQdZK8mUmOjmj964GqM18zWkj/Qpw17ns1CmGnYGCrvbj/Q/+K1jU3HbFH9ABtft+3TUgHAA==`,

	NarHash: "0b9q5xq96nszj51jnazj20ag9wbl3b9mn1ki73c8l2kjvxw3x1km",
	NarPath: filepath.Join("0", "0b", "0b9q5xq96nszj51jnazj20ag9wbl3b9mn1ki73c8l2kjvxw3x1km.nar.xz"),
	NarText: helper.MustRandString(1164, nil),
}

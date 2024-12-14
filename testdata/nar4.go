package testdata

import (
	"path/filepath"

	"github.com/kalbasit/ncps/pkg/helper"
	"github.com/kalbasit/ncps/pkg/nar"
)

// Nar4 is the nar representing hello from release-23.05 as returned by Harmonia.
//
//nolint:gochecknoglobals
var Nar4 = Entry{
	NarInfoHash: "jiwdym6f9w6v5jcbqf5wn7fmg11v5q0j",
	NarInfoPath: filepath.Join("j", "ji", "jiwdym6f9w6v5jcbqf5wn7fmg11v5q0j.narinfo"),
	NarInfoText: `StorePath: /nix/store/jiwdym6f9w6v5jcbqf5wn7fmg11v5q0j-hello-2.12.1
URL: nar/14vg46h9nbbqgbrbszrqm48f0bgzj6c4q3wkkcjf6gp53g8b21gh.nar?hash=jiwdym6f9w6v5jcbqf5wn7fmg11v5q0j
Compression: none
FileHash: sha256:14vg46h9nbbqgbrbszrqm48f0bgzj6c4q3wkkcjf6gp53g8b21gh
FileSize: 226488
NarHash: sha256:14vg46h9nbbqgbrbszrqm48f0bgzj6c4q3wkkcjf6gp53g8b21gh
NarSize: 226488
References: 9v5d40jyvmwgnq1nj8f19ji2rcc5dksd-glibc-2.37-45 jiwdym6f9w6v5jcbqf5wn7fmg11v5q0j-hello-2.12.1
Deriver: ar3xg083a9gqx3jh5y9d6drvrfz56xw0-hello-2.12.1.drv
Sig: cache.nixos.org-1:r4K0R7wHd5JnbP+BtO7LoAO65petsBxwfIxd6Dqh4fShZHw8Ot8NNfKvO2EuRP+KWtvSLYEotUGMDGtcLqxyCQ==`,

	NarHash:        "14vg46h9nbbqgbrbszrqm48f0bgzj6c4q3wkkcjf6gp53g8b21gh",
	NarCompression: nar.CompressionTypeZstd,
	NarPath:        filepath.Join("1", "14", "14vg46h9nbbqgbrbszrqm48f0bgzj6c4q3wkkcjf6gp53g8b21gh.nar.zst"),
	NarText:        helper.MustRandString(226488, nil),
}

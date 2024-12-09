package testdata

import (
	"path/filepath"

	"github.com/kalbasit/ncps/pkg/helper"
)

// Nar2 is the nar representing hello from release-24.05.
var Nar2 = Entry{
	NarInfoHash: "3acqrvb06vw0w3s9fa3wci433snbi2bg",
	NarInfoPath: filepath.Join("3", "3a", "3acqrvb06vw0w3s9fa3wci433snbi2bg.narinfo"),
	NarInfoText: `StorePath: /nix/store/3acqrvb06vw0w3s9fa3wci433snbi2bg-hello-2.12.1
URL: nar/1xqqdh1yn5sz3d6wcz3qz3azm5mbypwq6mv8g2dal1v042h0sprf.nar.xz
Compression: xz
FileHash: sha256:1xqqdh1yn5sz3d6wcz3qz3azm5mbypwq6mv8g2dal1v042h0sprf
FileSize: 50308
NarHash: sha256:188g68hrjilbsjifcj70k8729zqhm9sl1q336vg5wxwzw0qp0sk4
NarSize: 226560
References: 3acqrvb06vw0w3s9fa3wci433snbi2bg-hello-2.12.1 kpy2cyd05vdr6j1h200av81fnlxl1jw0-glibc-2.39-52
Deriver: 3gdc0qnmn6srg32sx019az6ll2mz1cda-hello-2.12.1.drv
Sig: cache.nixos.org-1:eGSj5WPpZRjwzx7eWpCyZdNsFHjhtGTZF8T4FccYXjHNkTOZoGPfplgFP1w5bEST0/FtfV7f3AmQUVEv1NAEDg==`,

	NarHash: "1xqqdh1yn5sz3d6wcz3qz3azm5mbypwq6mv8g2dal1v042h0sprf",
	NarPath: filepath.Join("1", "1x", "1xqqdh1yn5sz3d6wcz3qz3azm5mbypwq6mv8g2dal1v042h0sprf.nar.xz"),
	NarText: helper.MustRandString(50308, nil),
}

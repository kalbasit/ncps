package testdata

import (
	"path/filepath"

	"github.com/kalbasit/ncps/pkg/helper"
	"github.com/kalbasit/ncps/pkg/nar"
)

// Nar7 is the nar representing hello from release-25.11.
//
//nolint:gochecknoglobals
var Nar7 = Entry{
	NarInfoHash: "c12lxpykv6sld7a0sakcnr3y0la70x8w",
	NarInfoPath: filepath.Join("c", "c1", "c12lxpykv6sld7a0sakcnr3y0la70x8w.narinfo"),
	NarInfoText: `StorePath: /nix/store/c12lxpykv6sld7a0sakcnr3y0la70x8w-hello-2.12.2
URL: nar/09xizkfyvigl5fqs0dhkn46nghfwwijbpdzzl4zg6kx90prjmsg0.nar
Compression: none
NarHash: sha256:1yf3p87fsqig07crd9sj9wh7i9jpsa0x86a22fqbls7c81lc7ws2
NarSize: 113256
References: 7h6icyvqv6lqd0bcx41c8h3615rjcqb2-libiconv-109.100.2
Deriver: msnhw2b4dcn9kbswsfz63jplf7ncnxik-hello-2.12.2.drv
Sig: cache.nixos.org-1:oPqkkDFlniUh1BaGWwWd7LY2EfUh3r/GBxriDGE7vCfvJ3fKsnIDg1L4QFkuHKWIfwWxWy4FlpO6/5FHPx00AQ==`,

	NarHash:        "09xizkfyvigl5fqs0dhkn46nghfwwijbpdzzl4zg6kx90prjmsg0",
	NarCompression: nar.CompressionTypeXz,
	NarPath:        filepath.Join("0", "09", "09xizkfyvigl5fqs0dhkn46nghfwwijbpdzzl4zg6kx90prjmsg0.nar"),
	NarText:        helper.MustRandString(113256, nil),
}

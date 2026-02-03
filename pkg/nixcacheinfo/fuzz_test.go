package nixcacheinfo_test

import (
	"testing"

	"github.com/kalbasit/ncps/pkg/nixcacheinfo"
)

func FuzzParse(f *testing.F) {
	tests := []string{
		`StoreDir: /nix/store
WantMassQuery: 1
Priority: 40`,
		"",
		"StoreDir: /nix/store",
		"Priority: 100",
	}

	for _, tc := range tests {
		f.Add(tc)
	}

	f.Fuzz(func(_ *testing.T, data string) {
		_, _ = nixcacheinfo.ParseString(data)
	})
}

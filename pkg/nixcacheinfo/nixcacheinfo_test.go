package nixcacheinfo_test

import (
	"testing"

	"github.com/kalbasit/ncps/pkg/nixcacheinfo"
)

func TestParse(t *testing.T) {
	t.Parallel()

	t.Run("parse correctly", func(t *testing.T) {
		const nixCacheInfoText = `StoreDir: /nix/store
WantMassQuery: 1
Priority: 40`

		nci, err := nixcacheinfo.ParseString(nixCacheInfoText)
		if err != nil {
			t.Fatalf("expected no error but got %s", err)
		}

		if want, got := "/nix/store", nci.StoreDir; want != got {
			t.Errorf("want %q got %q", want, got)
		}

		if want, got := uint64(1), nci.WantMassQuery; want != got {
			t.Errorf("want %d got %d", want, got)
		}

		if want, got := uint64(40), nci.Priority; want != got {
			t.Errorf("want %d got %d", want, got)
		}
	})
}

package testdata

import "strconv"

const (
	nixStoreInfo = `StoreDir: /nix/store
WantMassQuery: 1
Priority: `
)

func NixStoreInfo(priority int) string {
	return nixStoreInfo + strconv.Itoa(priority)
}

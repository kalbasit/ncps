module github.com/kalbasit/ncps

go 1.23.3

replace github.com/nix-community/go-nix => github.com/kalbasit/go-nix v1.0.0

require (
	github.com/go-chi/chi/v5 v5.1.0
	github.com/inconshreveable/log15/v3 v3.0.0-testing.5
	github.com/mattn/go-colorable v0.1.13
	github.com/mattn/go-sqlite3 v1.14.24
	github.com/nix-community/go-nix v0.0.0-20241202132706-bf395042f3ee
	github.com/urfave/cli/v3 v3.0.0-beta1
	golang.org/x/term v0.27.0
)

require (
	github.com/go-stack/stack v1.8.1 // indirect
	github.com/inconshreveable/log15 v3.0.0-testing.5+incompatible // indirect
	github.com/klauspost/cpuid/v2 v2.2.9 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/minio/sha256-simd v1.0.1 // indirect
	github.com/mr-tron/base58 v1.2.0 // indirect
	github.com/multiformats/go-multihash v0.2.3 // indirect
	github.com/multiformats/go-varint v0.0.7 // indirect
	github.com/spaolacci/murmur3 v1.1.0 // indirect
	golang.org/x/crypto v0.30.0 // indirect
	golang.org/x/sys v0.28.0 // indirect
	lukechampine.com/blake3 v1.3.0 // indirect
)

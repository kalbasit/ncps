## 1. Core verification logic

- [x] 1.1 Add `ErrUntrustedNarInfo` sentinel error in `pkg/cache/cache.go`
- [x] 1.2 Add `requireTrustedSignature` field and `SetCacheRequireTrustedSignature` setter (mirroring `shouldSignNarinfo`)
- [x] 1.3 Add `PublicKeys()` accessor on the upstream `Cache` in `pkg/cache/upstream/cache.go`
- [x] 1.4 Add `trustedPublicKeys()` helper aggregating keys across upstreams under `upstreamCachesMu.RLock()`
- [x] 1.5 Add `verifyNarInfoTrusted()` helper: no-op when disabled, fail-closed when no keys, `signature.VerifyFirst` over `narInfo.Fingerprint()` otherwise
- [x] 1.6 Call `verifyNarInfoTrusted` in `PutNarInfo` after parse and before CDC normalization / signing

## 2. Flag wiring

- [x] 2.1 Add `--cache-require-trusted-signature` flag (env `CACHE_REQUIRE_TRUSTED_SIGNATURE`, config `cache.require-trusted-signature`, default off) in `pkg/ncps/serve.go`
- [x] 2.2 Wire the flag to `SetCacheRequireTrustedSignature` in `createCache`

## 3. Tests

- [x] 3.1 Add `PutNarInfoRequireTrustedSignature` cache test: disabled-accepts, no-keys-rejects, untrusted-rejects, trusted-accepts (asserting nothing persisted on rejection)
- [x] 3.2 Register the new subtest in `runCacheTestSuite`

## 4. Configuration & documentation

- [x] 4.1 Document `require-trusted-signature` in `config.example.yaml`
- [x] 4.2 Add `config.signing.requireTrustedSignature` to `charts/ncps/values.yaml`
- [x] 4.3 Add the field to `charts/ncps/values.schema.json`
- [x] 4.4 Render `require-trusted-signature` in `charts/ncps/templates/configmap.yaml`
- [x] 4.5 Add helm unit-test assertions in `charts/ncps/tests/configmap_test.yaml` (default false, enabled true)
- [x] 4.6 Add the flag row to `docs/docs/User Guide/Configuration/Reference.md`
- [x] 4.7 Add the value row to `docs/docs/User Guide/Installation/Helm Chart/Chart Reference.md`

## 5. Verification

- [x] 5.1 `task fmt` clean (0 changed); `golangci-lint run ./pkg/... ./cmd/... ./ent/...` → 0 issues (only stray lint hits are in gitignored `var/ncps/nix-tmp` dev cruft, not project code); PR Go tests already green per PR #1382
- [x] 5.2 `helm template` confirms `require-trusted-signature` defaults to false and flips to true when `config.signing.requireTrustedSignature=true`
- [x] 5.3 `openspec validate verify-trusted-narinfo-signature-on-put --no-interactive --strict` → "is valid"

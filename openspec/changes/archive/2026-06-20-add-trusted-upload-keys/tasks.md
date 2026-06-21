## 1. Cache: trusted upload keys (TDD)

- [x] 1.1 Write failing tests in `pkg/cache/cache_test.go` for `verifyNarInfoTrusted` using a dedicated trusted-upload-key set: (a) gate off → accept regardless of signature; (b) gate on + empty upload keys → `ErrUntrustedNarInfo`; (c) gate on + narinfo signed by a trusted upload key → accept; (d) gate on + narinfo signed only by an upstream key (not in upload keys) → reject; (e) gate on + unsigned narinfo → reject.
- [x] 1.2 Add `trustedUploadKeys []signature.PublicKey` field and `SetCacheTrustedUploadKeys(...)` setter to `Cache` (`pkg/cache/cache.go`).
- [x] 1.3 Change `verifyNarInfoTrusted` to consult `c.trustedUploadKeys` instead of `c.trustedPublicKeys()`, preserving the default-off and empty-list fail-closed branches; update the doc comment.
- [x] 1.4 Run `task test` for the cache package and confirm 1.1 tests pass.

## 2. Serve wiring: new flag (TDD)

- [x] 2.1 Write/extend failing tests in `pkg/ncps/serve_test.go` covering: flag/env/config parse of `cache-trusted-upload-key` into the cache's upload key set, and a startup error on a malformed key string.
- [x] 2.2 Add the repeatable `cache-trusted-upload-key` `StringSliceFlag` (`cache.trusted-upload-keys`, `CACHE_TRUSTED_UPLOAD_KEYS`) in `pkg/ncps/serve.go` near `cache-require-trusted-signature`.
- [x] 2.3 Parse each entry with `signature.ParsePublicKey` (no host filtering), failing startup with a clear error on a bad key, and call `SetCacheTrustedUploadKeys` with the parsed slice.
- [x] 2.4 Run `task test` for the ncps package and confirm 2.1 tests pass.

## 3. Config + docs

- [x] 3.1 Update `config.example.yaml`: revise the `require-trusted-signature` comment to reference upload keys and add a documented `trusted-upload-keys: []` example under `cache:`.
- [x] 3.2 Update `docs/docs/User Guide/Configuration/Reference.md`: document `cache.trusted-upload-keys`, its relationship to `require-trusted-signature`, the empty-list fail-closed behavior, and the self-build signing workflow (`secret-key-files` + matching public key).
- [x] 3.3 Add a CHANGELOG `[Unreleased]` entry describing the PUT signature gate in its shipped form: `require-trusted-signature` verifies uploads against operator-configured `cache.trusted-upload-keys` (decoupled from upstream pull-keys), default off, fail-closed on empty list.

## 4. Helm chart

- [x] 4.1 Add `config.signing.trustedUploadKeys: []` to `charts/ncps/values.yaml` with an explanatory comment; update the `requireTrustedSignature` comment to reference upload keys.
- [x] 4.2 Render `cache.trusted-upload-keys` from `signing.trustedUploadKeys` in `charts/ncps/templates/configmap.yaml` (range-emit, mirroring `upstream.public-keys`), omitting the key when the list is empty.
- [x] 4.3 Add Helm unit tests under `charts/ncps/tests/` asserting the configmap output for empty (key omitted) and populated cases; run `helm unittest charts/ncps`.

## 5. Verify

- [x] 5.1 Run `task fmt`, `task lint`, `task test` and confirm all exit zero.
- [x] 5.2 Run `openspec validate add-trusted-upload-keys --no-interactive` (with `OPENSPEC_TELEMETRY=0`) and confirm it passes.
- [x] 5.3 Manual smoke per design Migration Plan: gate on + own key in `trusted-upload-keys` + `nix store sign` → `nix copy` to `/upload` succeeds; gate on + empty list → upload rejected.

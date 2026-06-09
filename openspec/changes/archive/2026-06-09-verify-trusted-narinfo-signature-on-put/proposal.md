## Why

ncps re-signs every narinfo it ingests with its own secret key and serves it
under that key. The upstream-fetch path verifies incoming signatures against the
configured trusted public keys before doing so, but the client `PutNarInfo`
(`PUT`) path performs **zero** verification. A client can upload a narinfo with a
forged or absent upstream signature; ncps signs it and serves it, and downstream
clients trusting ncps's key accept it without ever learning the original
signature was invalid. This closes that laundering gap for the unverified `PUT`
path (issue #1269).

## What Changes

- Add a new operator flag `--cache-require-trusted-signature` (env
  `CACHE_REQUIRE_TRUSTED_SIGNATURE`, config `cache.require-trusted-signature`),
  **default off**, mirroring the existing `sign-narinfo` toggle.
- When enabled, `PutNarInfo` rejects any uploaded narinfo that does not carry at
  least one signature validating against the aggregated trusted public keys of
  all configured upstream caches, returning a new `ErrUntrustedNarInfo` sentinel
  before the narinfo is signed or persisted.
- Verification is **fail-closed**: an upload is also rejected when zero trusted
  upstream keys are configured, so an operator cannot accidentally run a no-op
  verifier (matching nix's own posture).
- Document the flag in `config.example.yaml`, the Helm chart
  (`values.yaml` → `config.signing.requireTrustedSignature`,
  `values.schema.json`, `configmap.yaml`, `configmap_test.yaml`), and the docs
  (Configuration Reference and Helm Chart Reference).

## Non-goals

- Changing the default ingestion behavior. With the flag off (default), client
  uploads pass through exactly as before — no verification, full backward
  compatibility.
- Re-verifying the upstream-fetch path, which already verifies signatures.
- Verifying NAR file contents or hashes — this gate covers narinfo signatures
  only.
- Per-upstream or per-key policy granularity; the trusted set is the union of
  all configured upstream public keys.

## Capabilities

### New Capabilities
- `narinfo-trusted-signature`: Optional, fail-closed verification of trusted
  upstream signatures on client `PUT` narinfo ingestion, gated by an operator
  flag and defaulting to off.

### Modified Capabilities
<!-- None. No existing capability's requirements change. -->

## Impact

- Code: `pkg/cache/cache.go` (`requireTrustedSignature` field, setter,
  `trustedPublicKeys`/`verifyNarInfoTrusted` helpers, `ErrUntrustedNarInfo`,
  call inside `PutNarInfo`), `pkg/cache/upstream/cache.go` (new `PublicKeys()`
  accessor), `pkg/ncps/serve.go` (flag wiring).
- Config/docs: `config.example.yaml`, `charts/ncps/**`, `docs/docs/User Guide/**`.
- I/O / network / memory: negligible. Verification runs in-memory over the
  already-parsed narinfo's signatures and the in-memory key set on the `PUT`
  path only; no additional network calls, storage reads, or per-stream
  allocations. Behavior on the default (off) path is unchanged.

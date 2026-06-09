## Context

ncps signs narinfos on ingestion (both upstream-fetch and client `PUT`) and
re-serves them under its own key. The upstream-fetch path
(`pkg/cache/upstream/cache.go`) already verifies incoming signatures against the
configured trusted public keys via `signature.VerifyFirst` before accepting a
narinfo. The client `PutNarInfo` path (`pkg/cache/cache.go`) parses, signs, and
stores uploads with no verification at all. Because ncps then signs with its own
key, downstream clients trusting ncps cannot tell that the original upstream
signature was forged or absent. nix itself validates signatures before
substituting from a cache, so this gate restores that guarantee for the `PUT`
path (issue #1269).

The trusted key material already exists in memory: each upstream `Cache` parses
its configured public keys at construction. The only missing piece on the cache
side was an accessor and an aggregation + verification step on the `PUT` path,
plus an operator toggle.

## Goals / Non-Goals

**Goals:**
- Give operators a way to reject `PUT`-uploaded narinfos that are not signed by a
  trusted upstream key, closing the signature-laundering gap.
- Keep the default behavior identical to today (off → passthru), so existing
  deployments are unaffected.
- Reuse the exact verification primitive (`signature.VerifyFirst` over
  `narInfo.Fingerprint()`) already used on the upstream path for consistency.

**Non-Goals:**
- Modifying the upstream-fetch verification, which already works.
- Per-upstream or per-key trust policy; the trusted set is the union of all
  configured upstream keys.
- Verifying NAR bytes/hashes; this gate is narinfo-signature only.

## Decisions

- **Fail-closed when no keys are configured.** Unlike the upstream path's
  `if len(c.publicKeys) > 0` guard (which skips verification when no keys
  exist), the `PUT` gate rejects when zero trusted keys are configured. Rationale:
  an operator who turns on "require trusted signature" but has no keys would
  otherwise run a silent no-op verifier — a dangerous false sense of security.
  Failing closed matches nix's substitution posture. Alternative considered:
  mirror the upstream skip-when-empty behavior — rejected because it makes the
  gate a no-op in a plausible misconfiguration.
- **Aggregate keys across all upstreams under the existing RWMutex.**
  `trustedPublicKeys()` takes `upstreamCachesMu.RLock()` and appends each
  upstream's `PublicKeys()` into a fresh slice. Rationale: upstreams can be added
  concurrently; reuse the same lock that already guards `upstreamCaches`.
- **Verify after parse, before normalization/signing.** The call sits
  immediately after `narinfo.Parse` in `PutNarInfo`, before CDC compression
  normalization and `signNarInfo`. Rationale: `Fingerprint()` is derived from
  store path / NarHash / NarSize / References — none of which the CDC block
  mutates — so the fingerprint verified is the authentic uploaded one.
- **New exported accessor `Cache.PublicKeys()` on the upstream package.** The
  parsed keys were unexported with no getter; expose them so the cache layer can
  aggregate. Returns an empty slice when none configured.
- **New `ErrUntrustedNarInfo` sentinel** so callers and tests can match the
  rejection with `errors.Is`.
- **Setter mirrors `SetCacheSignNarinfo`.** `SetCacheRequireTrustedSignature`
  wires the boolean from the flag in `createCache`, matching the established
  configuration pattern.

## Risks / Trade-offs

- [Operator enables the gate but only some uploads are signed by trusted keys] →
  Those uploads are rejected with a clear `ErrUntrustedNarInfo`. This is the
  intended fail-closed behavior; documented in config and chart references so
  operators understand they must configure upstream public keys first.
- [Accessor returns the internal slice by reference] → The sole caller only reads
  and appends into a new slice, so there is no mutation hazard today; a future
  caller could mutate the backing array. Low impact; can be hardened with
  `slices.Clone` if a mutating caller ever appears.
- [Performance] → Verification is in-memory over already-parsed signatures and an
  in-memory key set, on the `PUT` path only. No extra network or storage I/O; the
  default (off) path adds a single boolean check.

## Migration Plan

- Ship default-off. No data migration, no schema change, no behavior change for
  existing deployments.
- Operators opt in by setting `--cache-require-trusted-signature` (or the env /
  config equivalent) after confirming their upstream public keys are configured.
- Rollback is a config flip back to off; no persisted state is affected.

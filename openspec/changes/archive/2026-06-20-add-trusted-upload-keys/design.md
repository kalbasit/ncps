## Context

`cache.require-trusted-signature` (issue #1269) closes a signature-laundering
gap: ncps re-signs every ingested narinfo with its own key and serves it as
trusted, so an unverified `PUT` upload would be laundered into a trusted
artifact. The gate verifies `PUT` narinfos before signing.

The flaw is the **key set** it verifies against. `verifyNarInfoTrusted`
(`pkg/cache/cache.go:1102`) calls `trustedPublicKeys()`, which aggregates the
public keys of the configured upstream caches (cache.go:1084). A locally-built
store path is unsigned, or signed by the operator's own key — never by an
upstream key — so the gate rejects every self-built upload with
`ErrUntrustedNarInfo` → HTTP 500. PUT itself has no per-uploader auth (only the
`putPermitted` on/off boolean, server.go:107), so this gate is the only
upload-authenticity control, and it points at the wrong keys.

Upstream public keys are parsed today via `signature.ParsePublicKey` in the
`upstream` package (upstream/cache.go:192-193) from nix-format `name:base64`
strings into `[]signature.PublicKey`.

## Goals / Non-Goals

**Goals:**
- Decouple upload-trust from pull-trust: verify `PUT` narinfos against an
  operator-designated key set, not the upstream pull keys.
- Let operators accept their own signed self-builds while keeping the
  anti-laundering guarantee (only operator-trusted signatures get re-signed).
- Keep `require-trusted-signature` as the single enable switch; preserve
  default-off and fail-closed semantics.
- Update all config surfaces: example config, reference docs, Helm chart, Helm
  unit tests.

**Non-Goals:**
- Per-token / per-uploader authentication on `PUT`.
- Auto-trusting ncps's own signing key or upstream keys for uploads.
- Changing the upstream-fetch verification path or GET/HEAD Bearer auth.

## Decisions

### D1: New `trustedUploadKeys []signature.PublicKey` on `Cache`, replacing the upstream-key source in `verifyNarInfoTrusted`

Add a field plus `SetCacheTrustedUploadKeys([]signature.PublicKey)` setter.
`verifyNarInfoTrusted` keeps its structure but consults `c.trustedUploadKeys`
instead of `c.trustedPublicKeys()`:

```go
func (c *Cache) verifyNarInfoTrusted(narInfo *narinfo.NarInfo) error {
    if !c.requireTrustedSignature {
        return nil
    }
    keys := c.trustedUploadKeys           // was c.trustedPublicKeys()
    if len(keys) == 0 {
        return ErrUntrustedNarInfo         // fail closed, unchanged
    }
    if !signature.VerifyFirst(narInfo.Fingerprint(), narInfo.Signatures, keys) {
        return ErrUntrustedNarInfo
    }
    return nil
}
```

`trustedPublicKeys()` stays (still used elsewhere if needed) but is no longer
the PUT-verification source.

**Alternative considered — keep upstream keys and add a bypass flag**
(`allow-unsigned-uploads`): rejected. Two interacting booleans with unclear
precedence is a footgun, and it still can't *authorize* a specific uploader key
— it only weakens the gate to "accept anything".

### D2: No new enable flag — reuse `require-trusted-signature`

`require-trusted-signature` remains the master toggle. Enabling it with an empty
`trusted-upload-keys` list rejects all uploads (fail-closed), exactly mirroring
today's "gate on + no trusted keys" behavior. Operators opt in to specific
uploads by populating `trusted-upload-keys`. This keeps a single, well-understood
switch and avoids a confusing second flag.

### D3: Parse `--cache-trusted-upload-key` in serve.go with `signature.ParsePublicKey`

A repeatable `StringSliceFlag` `cache-trusted-upload-key`
(`cache.trusted-upload-keys` / `CACHE_TRUSTED_UPLOAD_KEYS`). In serve.go, parse
each entry with `signature.ParsePublicKey` (same as upstream keys); a parse
error fails startup with a clear message. Pass the parsed slice to
`SetCacheTrustedUploadKeys`. Keys are NOT host-filtered (unlike upstream keys,
which regex-match a host) — upload keys are a flat global trust set.

### D4: Helm surface — `config.signing.trustedUploadKeys` list

Add `signing.trustedUploadKeys: []` to `values.yaml`, render it under
`cache.trusted-upload-keys` in `configmap.yaml` (range emit, like
`upstream.public-keys`). Add Helm unit-test coverage for empty (omitted/empty)
and populated cases.

## Risks / Trade-offs

- **No existing users.** The PUT signature gate (`require-trusted-signature`) is
  unreleased — it does not appear in the CHANGELOG `[Unreleased]` section — so
  there is no deployed behavior to preserve and this is not a breaking change.
  It ships in its correct form before any release.
- **Operator must sign uploads** (set `secret-key-files` in nix.conf and add
  the matching public key) to use the gate → this is the intended, correct
  trust model; documented in the reference docs.
- **Empty-list fail-closed could surprise** an operator who flips the gate on
  without keys → preserved deliberately (matches nix posture); the rejection
  error and docs make the cause explicit.

## Migration Plan

No operator migration: the feature is unreleased, so no deployed config relies
on the old behavior. Ship code + config/docs/chart + CHANGELOG together as one
change. No persisted-data or schema changes, so rollback is a clean release
revert.

## Open Questions

- None. The empty-list-fail-closed and no-second-flag decisions are settled per
  the operator's direction.

## Why

The `cache.require-trusted-signature` gate verifies client-uploaded (`PUT`)
narinfos against the configured **upstream** public keys. Upstream keys can
never match a locally-built store path (it is unsigned, or signed by the
operator's own key), so enabling the gate makes it impossible to `nix copy`
your own builds — every upload fails with HTTP 500 `rejecting untrusted
narinfo`. The gate conflates "content an upstream already signed" with
"uploader I authorize", which are different trust relationships. Pull-trust
(which upstreams I mirror) and upload-trust (whose uploads I re-sign and serve)
must be decoupled.

## What Changes

- Introduce a new operator-configured key set, `cache.trusted-upload-keys`
  (flag `--cache-trusted-upload-key`, env `CACHE_TRUSTED_UPLOAD_KEYS`), holding
  nix-format `name:base64` public keys that authorize `PUT` uploads.
- When `require-trusted-signature` is enabled, verify `PUT` narinfos against
  `trusted-upload-keys` **instead of** the upstream public keys. The operator
  adds their own signing key here; signed self-builds are then accepted.
- Preserve the existing master switch and fail-closed posture: gate stays off
  by default; when on with an empty `trusted-upload-keys` list, every upload is
  rejected (no silent no-op verifier). No second enable flag is added —
  `require-trusted-signature` remains the single toggle, only its key source
  changes.
- Not breaking: `require-trusted-signature` and the PUT signature gate are
  **unreleased** (absent from the CHANGELOG `[Unreleased]` section), so there
  are no existing users relying on the old upstream-key behavior. This change
  ships the gate in its correct form before any release.
- Update `config.example.yaml`, the Configuration Reference docs, the Helm
  chart (`values.yaml` + `configmap.yaml`), Helm unit tests, and the CHANGELOG
  `[Unreleased]` section.

## Capabilities

### New Capabilities
<!-- none; the new config surface belongs to the existing capability below -->

### Modified Capabilities
- `narinfo-trusted-signature`: the verification key source changes from the
  union of upstream public keys to the dedicated `trusted-upload-keys` set, and
  the fail-closed condition keys on that set being empty. The anti-laundering
  goal (issue #1269) is preserved — only narinfos signed by an operator-trusted
  uploader key are re-signed and served.

## Non-goals

- No per-token or per-uploader authentication on `PUT` (still governed solely
  by `putPermitted`); this change only governs which narinfo signatures are
  trusted.
- No automatic trust of ncps's own signing key, nor of upstream keys, for
  uploads — the operator lists exactly what they intend to trust.
- No change to the upstream-fetch verification path or to GET/HEAD Bearer auth.
- No change to the default behavior (gate remains off).

## Impact

- Code: `pkg/cache/cache.go` (`verifyNarInfoTrusted`, new `trustedUploadKeys`
  field/setter), `pkg/ncps/serve.go` (new flag + wiring).
- Config/docs/chart: `config.example.yaml`, `docs/.../Configuration/Reference.md`,
  `charts/ncps/values.yaml`, `charts/ncps/templates/configmap.yaml`, Helm tests.
- I/O, network latency, memory: negligible — verification is an in-memory
  signature check over a small fixed key set on the already-buffered narinfo;
  no added network calls, allocations, or storage.

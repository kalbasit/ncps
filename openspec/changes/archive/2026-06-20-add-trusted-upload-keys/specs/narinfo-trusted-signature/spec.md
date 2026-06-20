## ADDED Requirements

### Requirement: Operator-configured trusted upload keys

The cache MUST expose an operator-configured set of trusted upload public keys,
distinct from the upstream caches' public keys, that authorizes which client
`PUT` narinfo signatures are accepted when the gate is enabled. The set is
configurable via the `--cache-trusted-upload-key` flag (repeatable), the
`CACHE_TRUSTED_UPLOAD_KEYS` environment variable, and the
`cache.trusted-upload-keys` config key. Each entry MUST be a nix-format
`name:base64` public key. The set defaults to empty.

#### Scenario: Upload keys are wired from flag, env, and config

- **WHEN** an operator supplies one or more nix-format public keys via
  `--cache-trusted-upload-key`, `CACHE_TRUSTED_UPLOAD_KEYS`, or
  `cache.trusted-upload-keys`
- **THEN** the cache parses them into its trusted upload key set and uses them
  to verify `PUT` narinfo signatures when the gate is enabled

#### Scenario: Upload keys are independent of upstream pull keys

- **WHEN** the trusted upload key set is configured
- **THEN** verification of `PUT` uploads consults only the trusted upload keys
  and never the upstream caches' public keys, so pull-trust and upload-trust are
  decoupled

## MODIFIED Requirements

### Requirement: Operator gate for trusted-signature verification on PUT ingestion

The cache MUST expose an operator toggle, default off, that controls whether
client-uploaded narinfos (the `PutNarInfo` / `PUT` path) are verified against
the configured trusted upload keys before being signed and persisted. The
toggle is configurable via the `--cache-require-trusted-signature` flag, the
`CACHE_REQUIRE_TRUSTED_SIGNATURE` environment variable, and the
`cache.require-trusted-signature` config key. The toggle remains the single
enable switch; enabling it changes only which key set is consulted, never adds a
second flag.

#### Scenario: Disabled by default preserves passthru

- **WHEN** the gate is not configured and a client uploads a narinfo via `PUT`
- **THEN** the narinfo is accepted, signed, and persisted exactly as before with
  no signature verification performed

#### Scenario: Toggle is wired from flag, env, and config

- **WHEN** an operator sets `--cache-require-trusted-signature`,
  `CACHE_REQUIRE_TRUSTED_SIGNATURE`, or `cache.require-trusted-signature` to true
- **THEN** the cache enables trusted-signature verification on the `PutNarInfo`
  path, using the configured trusted upload keys as the verification key set

### Requirement: Reject untrusted narinfos when verification is enabled

The cache MUST, when the gate is enabled, reject any narinfo uploaded via `PUT`
that does not carry at least one signature validating against the configured
trusted upload keys. Verification MUST occur after the narinfo is parsed and
before it is signed or persisted, and rejection MUST return the
`ErrUntrustedNarInfo` sentinel and leave no narinfo persisted.

#### Scenario: Untrusted signature is rejected

- **WHEN** the gate is enabled and a client uploads a narinfo whose signatures
  do not validate against any configured trusted upload key
- **THEN** the upload is rejected with `ErrUntrustedNarInfo` and no narinfo
  record is persisted

#### Scenario: Trusted signature is accepted and persisted

- **WHEN** the gate is enabled and a client uploads a narinfo carrying at least
  one signature that validates against a configured trusted upload key
- **THEN** the narinfo is accepted, signed with the cache's key, and persisted

### Requirement: Fail closed when no trusted keys are configured

The cache MUST, when the gate is enabled and no trusted upload keys are
configured, reject every `PUT` narinfo upload with `ErrUntrustedNarInfo` rather
than silently accepting uploads. This prevents an operator from accidentally
running a no-op verifier and matches nix's fail-closed substitution posture.

#### Scenario: No trusted upload keys configured rejects all uploads

- **WHEN** the gate is enabled, zero trusted upload keys are configured, and a
  client uploads a narinfo via `PUT`
- **THEN** the upload is rejected with `ErrUntrustedNarInfo` and no narinfo
  record is persisted

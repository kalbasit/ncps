## ADDED Requirements

### Requirement: Operator gate for trusted-signature verification on PUT ingestion

The cache MUST expose an operator toggle, default off, that controls whether
client-uploaded narinfos (the `PutNarInfo` / `PUT` path) are verified against
the configured trusted upstream public keys before being signed and persisted.
The toggle is configurable via the `--cache-require-trusted-signature` flag, the
`CACHE_REQUIRE_TRUSTED_SIGNATURE` environment variable, and the
`cache.require-trusted-signature` config key.

#### Scenario: Disabled by default preserves passthru

- **WHEN** the gate is not configured and a client uploads a narinfo via `PUT`
- **THEN** the narinfo is accepted, signed, and persisted exactly as before with
  no signature verification performed

#### Scenario: Toggle is wired from flag, env, and config

- **WHEN** an operator sets `--cache-require-trusted-signature`,
  `CACHE_REQUIRE_TRUSTED_SIGNATURE`, or `cache.require-trusted-signature` to true
- **THEN** the cache enables trusted-signature verification on the `PutNarInfo`
  path

### Requirement: Reject untrusted narinfos when verification is enabled

The cache MUST, when the gate is enabled, reject any narinfo uploaded via `PUT`
that does not carry at least one signature validating against the union of the
trusted public keys of all configured upstream caches. Verification MUST occur
after the narinfo is parsed and before it is signed or persisted, and rejection
MUST return the `ErrUntrustedNarInfo` sentinel and leave no narinfo persisted.

#### Scenario: Untrusted signature is rejected

- **WHEN** the gate is enabled and a client uploads a narinfo whose signatures
  do not validate against any configured trusted upstream public key
- **THEN** the upload is rejected with `ErrUntrustedNarInfo` and no narinfo
  record is persisted

#### Scenario: Trusted signature is accepted and persisted

- **WHEN** the gate is enabled and a client uploads a narinfo carrying at least
  one signature that validates against a configured trusted upstream public key
- **THEN** the narinfo is accepted, signed with the cache's key, and persisted

### Requirement: Fail closed when no trusted keys are configured

The cache MUST, when the gate is enabled and no trusted upstream public keys are
configured, reject every `PUT` narinfo upload with `ErrUntrustedNarInfo` rather
than silently accepting uploads. This prevents an operator from accidentally
running a no-op verifier and matches nix's fail-closed substitution posture.

#### Scenario: No trusted keys configured rejects all uploads

- **WHEN** the gate is enabled, zero trusted upstream public keys are
  configured, and a client uploads a narinfo via `PUT`
- **THEN** the upload is rejected with `ErrUntrustedNarInfo` and no narinfo
  record is persisted

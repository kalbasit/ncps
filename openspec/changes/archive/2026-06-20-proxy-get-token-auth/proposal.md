## Why

On shared or public-facing ncps deployments, the read paths (GET/HEAD for
`.narinfo` and `.nar`) are unauthenticated, exposing the cache to anonymous
abuse and bandwidth scraping. Issue #1136 requested an opt-in way to require a
token on read paths; the maintainer agreed. PR #1406 began this work; this
change captures it and rounds it out to full project scope (security hardening,
docs, Helm, changelog).

## What Changes

- Add a `--cache-get-token` flag (env `CACHE_GET_TOKEN`) that, when set,
  requires `Authorization: Bearer <token>` on every GET and HEAD request.
  Unauthenticated/incorrect requests get `401 Unauthorized`. Empty (default)
  preserves today's open behavior, so this is non-breaking.
- A `requireGetToken` chi middleware enforces this. Infra routes (`/healthz`,
  `/metrics`) are always exempt; PUT/DELETE are unaffected (they keep their
  existing `putPermitted`/`deletePermitted` guards).
- **Security:** compare the presented token with `crypto/subtle.ConstantTimeCompare`
  to avoid leaking the secret via a timing side-channel.
- The 401 response carries a `WWW-Authenticate: Bearer` header per RFC 7235.
- **Docs:** document the flag in the Configuration Reference (Security & Signing)
  and Cache Management usage guide; add `cache.get-token` to `config.example.yaml`.
- **Helm:** because the token is a secret, expose it as the `CACHE_GET_TOKEN`
  env var sourced from a managed/existing Kubernetes Secret (mirroring the
  `redis-password` pattern) rather than placing it in the plaintext ConfigMap.
  Add `values.yaml` keys, a `values.schema.json` entry, and a chart unit test.
- **Changelog:** add an `Added` entry under `[Unreleased]`.

## Capabilities

### New Capabilities
- `read-path-auth`: optional Bearer-token authentication gate for GET/HEAD
  cache read paths, configured via flag/env and (in Kubernetes) a Secret-backed
  env var, with infra routes always exempt and write verbs unaffected.

### Modified Capabilities
<!-- None: write-verb guards, signing, and the API surface keep their existing requirements. -->

## Impact

- Code: `pkg/server/server.go` (field, setter, middleware), `pkg/ncps/serve.go`
  (flag wiring), `pkg/server/server_test.go` (tests).
- Config/docs: `config.example.yaml`, `docs/.../Reference.md`,
  `docs/.../Cache Management.md`, `CHANGELOG.md`.
- Helm: `charts/ncps/values.yaml`, `templates/secret.yaml`,
  `templates/deployment.yaml`, `templates/statefulset.yaml`,
  `values.schema.json`, and chart tests.
- Out of scope: NixOS module docs (`NixOS.md`) — that module lives in upstream
  nixpkgs, not this repo.

## Non-goals

- No per-user/multi-token, scopes, JWT, or OAuth — a single shared static token.
- No authentication for PUT/DELETE (already governed by their own verb guards).
- No change to signing, trusted-signature, or upstream-auth behavior.

## I/O, latency, memory

Negligible: when the token is empty the middleware short-circuits with a single
string check; when set it adds one header read and a constant-time byte compare
per GET/HEAD request. No additional I/O, network calls, or allocations on the
hot path.

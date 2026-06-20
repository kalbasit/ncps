## 1. Server hardening (TDD)

- [x] 1.1 Extend `TestSetGetToken` (or add cases) asserting `401` responses carry a `WWW-Authenticate: Bearer` header — RED.
- [x] 1.2 Add `WWW-Authenticate: Bearer` header before writing `401` in `requireGetToken` (`pkg/server/server.go`) — GREEN.
- [x] 1.3 Replace the `!= s.getToken` string comparison in `requireGetToken` with `crypto/subtle.ConstantTimeCompare` (guarded by the existing `Bearer ` prefix check); add the `crypto/subtle` import.
- [x] 1.4 Run `task test` for `pkg/server` and confirm all `TestSetGetToken` subtests pass (including no-token, GET/HEAD, missing/wrong/malformed, `/healthz` exempt, PUT unaffected, and the new WWW-Authenticate case).

## 2. Configuration & docs

- [x] 2.1 Add `get-token` under `cache:` in `config.example.yaml` with an explanatory comment (sibling to `allow-put-verb`).
- [x] 2.2 Add `--cache-get-token` row to the Security & Signing table in `docs/docs/User Guide/Configuration/Reference.md` (env `CACHE_GET_TOKEN`, default empty).
- [x] 2.3 Add a short "Authenticating read access" subsection in `docs/docs/User Guide/Usage/Cache Management.md` explaining the flag, the `Authorization: Bearer` requirement, and the `/healthz` + `/metrics` exemption.

## 3. Helm chart (token as a Secret)

- [x] 3.1 Add `config.permissions.getToken` (inline value, default `""`) and `config.permissions.getTokenExistingSecret` (default `""`) to `charts/ncps/values.yaml` with comments.
- [x] 3.2 Add a `values.schema.json` entry for the two new `permissions` keys (string types).
- [x] 3.3 In `templates/secret.yaml`, render a `get-token` key (b64-encoded) when `getToken` is set and no `getTokenExistingSecret` is provided; update the top-level `{{- if or ... }}` guard so the Secret is emitted in that case.
- [x] 3.4 In `templates/deployment.yaml` and `templates/statefulset.yaml`, inject `CACHE_GET_TOKEN` via `secretKeyRef` (existing secret name + key `get-token`, falling back to the chart-managed secret) when `getToken` or `getTokenExistingSecret` is set — mirroring the `CACHE_REDIS_PASSWORD` pattern.
- [x] 3.5 Ensure the ConfigMap does NOT contain the token (no change needed beyond confirming `get-token` is absent from `templates/configmap.yaml`).
- [x] 3.6 Add a chart unit test under `charts/ncps/tests/` asserting: env var present with `secretKeyRef` when `getToken` set; existing-secret reference when `getTokenExistingSecret` set; no env var / no Secret key when unset; and ConfigMap has no plaintext token.

## 4. Changelog & verification

- [x] 4.1 Add an `Added` entry under `## [Unreleased]` in `CHANGELOG.md` describing `--cache-get-token` / `CACHE_GET_TOKEN`, the infra-route exemption, PUT/DELETE being unaffected, and the Helm `config.permissions.getToken` / `getTokenExistingSecret` keys (reference #1136).
- [x] 4.2 Run `task fmt`, `task lint`, `task test` and confirm all exit zero.
- [x] 4.3 Run `helm unittest charts/ncps` and confirm the new and existing chart tests pass.

## Context

ncps serves cache reads (`.narinfo`, `.nar`, build-trace, pins) over plain
unauthenticated HTTP. On shared or public deployments this allows anonymous
bandwidth scraping. The codebase already has an opt-in verb-permission pattern
(`SetPutPermitted` / `SetDeletePermitted` + chi middleware) that read-path auth
can follow for consistency. PR #1406 introduced the core flag, field, setter and
`requireGetToken` middleware; this change adopts that work, hardens it, and
completes the surrounding docs/chart/changelog so the feature is usable and
discoverable.

## Goals / Non-Goals

**Goals:**
- Opt-in, non-breaking Bearer-token gate on GET/HEAD read paths.
- Constant-time secret comparison (no timing side-channel).
- RFC 7235-compliant `401` (`WWW-Authenticate: Bearer`).
- Infra routes (`/healthz`, `/metrics`) always reachable for probes/scrape.
- First-class Helm support that treats the token as a secret, not config.
- Documentation parity with the existing verb flags.

**Non-Goals:**
- Multi-token, scopes, JWT/OAuth, or per-route policies (single shared token).
- Authenticating PUT/DELETE (own guards) or changing signing behavior.
- Touching the upstream nixpkgs NixOS module (`NixOS.md`).

## Decisions

- **Middleware placement.** `requireGetToken` is registered via `s.router.Use`
  after `skipTelemetryForInfraRoutes` and before route registration, matching
  the existing middleware chain. `middleware.Heartbeat("/healthz")` already
  short-circuits `/healthz` earlier in the chain, so the explicit `/healthz`
  exemption in the gate is belt-and-suspenders; the `/metrics` exemption is
  load-bearing because `/metrics` is a real route handler that runs after the
  middleware. We keep both path checks for clarity and defense-in-depth.
- **Constant-time compare.** Use `subtle.ConstantTimeCompare([]byte(got), []byte(s.getToken)) == 1`
  guarded by a `strings.HasPrefix(authHeader, "Bearer ")` check first.
  Rationale: ordinary `!=` short-circuits on the first differing byte and leaks
  token bytes via response timing; `ConstantTimeCompare` does not. Alternative
  (hashing both sides and comparing) was rejected as unnecessary complexity for
  a single static secret.
- **401 header.** Emit `WWW-Authenticate: Bearer` before writing the status, as
  RFC 7235 §4.1 requires a challenge on `401`.
- **Helm secret handling.** The token is sensitive, so it is delivered as the
  `CACHE_GET_TOKEN` env var via `secretKeyRef`, mirroring `redis-password`:
  `config.permissions.getToken` (inline → chart-managed Secret key
  `get-token`) and `config.permissions.getTokenExistingSecret` (operator Secret;
  key `get-token`). It is deliberately NOT written into the ConfigMap, which is
  plaintext. Alternative (ConfigMap value, like the verb booleans) was rejected
  because the verb flags are non-secret booleans whereas this is a credential.
- **Env precedence.** The flag reads from `cache.get-token` (file) and
  `CACHE_GET_TOKEN` (env). The chart sets only the env var; the ConfigMap omits
  `get-token`, so there is no plaintext-vs-secret conflict.

## Risks / Trade-offs

- **Operator misconfig leaving reads open** → Default empty preserves current
  behavior; documented prominently in Reference + Cache Management so operators
  know it is opt-in.
- **Token in ConfigMap by accident** → Chart sources it only from a Secret; a
  chart unit test asserts the ConfigMap has no plaintext token and the env var
  uses `secretKeyRef`.
- **Probes/scrape breaking under auth** → Infra-route exemption + an explicit
  `/healthz` test guard against regressions.
- **Clients that cannot send Authorization** → Out of scope; this is opt-in and
  intended for deployments whose clients can set the header (e.g. via netrc /
  `nix.conf` `netrc-file` on the consuming side).

## Migration Plan

Additive and opt-in; no data migration. Operators enable by setting
`--cache-get-token` / `CACHE_GET_TOKEN` (or the chart `getToken` /
`getTokenExistingSecret` values) and distributing the token to clients. Rollback
is unsetting the flag/value (reverts to open reads). No schema or storage
changes.

## Open Questions

- None blocking. A future enhancement could support multiple/rotating tokens,
  but that is explicitly out of scope here.

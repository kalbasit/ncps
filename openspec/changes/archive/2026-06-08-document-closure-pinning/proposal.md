## Why

Closure pinning shipped in #1108 (the `POST/DELETE /pin/{hash}.narinfo` and
`GET /pins` HTTP endpoints that protect a narinfo and its transitive references
from LRU eviction), but it was never documented. Issue #1021 — the original
request for cachix-style pins — stays open only because users have no way to
discover or use the feature. The behavior is stable and fully specified; the
remaining gap is purely documentation.

## What Changes

- Add a new user-facing page **User Guide → Features → Pinning** (sibling of the
  existing `Features/CDC.md`) describing what pinning is, the three HTTP
  endpoints, request/response shapes and status codes, idempotency, transitive
  closure protection, and worked `curl` examples.
- Register the new page in `docs/!!!meta.json` (Trilium export index) under the
  `features` note so it appears in the published documentation tree.
- Cross-link the new page from **Usage → Cache Management** (which documents LRU
  cleanup) and from **Getting Started → Concepts** where eviction is introduced,
  so operators managing cache size discover pinning as the way to protect paths.
- No code, API, schema, or runtime behavior changes.

## Capabilities

### New Capabilities

- _(none)_

### Modified Capabilities

- `closure-pinning`: add a documentation requirement stating that the pin/unpin/
  list endpoints and their LRU-protection semantics SHALL be described in the
  user-facing documentation. This makes the docs a verifiable part of the spec
  (single source of truth) without altering any existing behavioral requirement.

## Impact

- **Docs**: new `docs/docs/User Guide/Features/Pinning.md`; edits to
  `docs/!!!meta.json`, `Usage/Cache Management.md`, and
  `Getting Started/Concepts.md`. Content/structure only — no doc tooling change.
- **Code / API / database**: none. The endpoints, `pinned_closures` table, and
  eviction logic are untouched.
- **I/O, network latency, memory**: no measurable change — documentation only,
  nothing in the serving or eviction path is modified.
- **Issue tracking**: closes #1021.

## Non-goals

- Changing pinning behavior, endpoint paths, payloads, or status codes.
- Adding a CLI subcommand, authentication, or a TTL/expiry for pins (the feature
  has none today; documenting a non-existent capability would mislead users).
- Documenting internal implementation details (BFS closure traversal, Ent
  queries) beyond what an operator needs — those belong to the developer guide
  and the existing `closure-pinning` spec, not the user feature page.
- Regenerating or restructuring the Trilium doc export beyond adding the one new
  note and its cross-links.

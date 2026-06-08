## Context

Closure pinning (#1108) is fully implemented and behaviorally specified in the
`closure-pinning` spec, but has no entry in the published documentation. The
docs live in `docs/` as a Trilium export: markdown files under
`docs/docs/<Section>/<Page>.md` plus a single `docs/!!!meta.json` that defines
the note tree, titles, and `shareAlias` slugs. A page that is not registered in
`!!!meta.json` is not published.

The implementation surface to document (verified against source):

- `pkg/server/server.go`: `POST /pin/{hash}.narinfo` → `pinClosure`,
  `DELETE /pin/{hash}.narinfo` → `unpinClosure`, `GET /pins` → `listPins`.
- Handlers return `200 OK` on success; pin returns `404 Not Found` when the
  narinfo hash is unknown; `GET /pins` returns a JSON array of hash strings with
  `Content-Type: application/json` (empty array when nothing is pinned).
- `pkg/cache/cache.go`: pin/unpin are idempotent; pinning protects the narinfo
  **and all transitive references** from LRU eviction; missing references are
  fetched from upstream at pin time.
- The closest existing analog page is `docs/docs/User Guide/Features/CDC.md`,
  which establishes the section, tone, and the `> [!NOTE]`/`> [!CAUTION]`
  callout style and "Related Documentation" footer used across the site.

## Goals / Non-Goals

**Goals:**

- Give operators a single discoverable page that explains what pinning does, the
  three endpoints, and copy-paste `curl` examples that match the real contract.
- Wire the page into the published tree (`!!!meta.json`) and cross-link it from
  the two places eviction is discussed (Cache Management, Concepts).
- Close #1021 by making the feature documented and findable.

**Non-Goals:**

- Changing any pinning behavior or its spec's behavioral requirements.
- Documenting internals (BFS traversal, Ent queries) on the user page.
- Restructuring the Trilium export or its tooling.

## Decisions

- **Place the page under `User Guide → Features` as `Pinning.md`, parallel to
  `CDC.md`.** Pinning is a user-facing, opt-in cache feature exactly like CDC, so
  it belongs beside it rather than buried inside Cache Management. Alternatives
  considered: (a) a subsection inside `Usage/Cache Management.md` — rejected
  because pinning is a distinct feature with its own endpoints and deserves a
  stable slug; (b) `Operations/` — rejected, that section is for migrations/fsck
  maintenance tasks, not feature usage.

- **Register one new note in `!!!meta.json` under the `features` parent** with
  `title: "Pinning"`, `dataFileName: "Pinning.md"`, and
  `shareAlias: "pinning"`, mirroring the existing CDC note's attribute shape.
  The `noteId` is a Trilium-style 12-char alphanumeric id; pick a fresh value not
  already present in the file. This is the only structural edit.

- **Derive all documented behavior from source, not from the PR description.**
  Status codes, idempotency, the 404-on-unknown-hash case, and the JSON
  array-of-strings response are taken from `pkg/server/server.go` and
  `pkg/cache/cache.go` so the docs cannot drift from the spec.

- **Express the spec delta as an added documentation requirement on
  `closure-pinning`**, not a new capability, so the docs are verifiable against
  the same spec that defines the behavior (single source of truth).

## Risks / Trade-offs

- [Docs drift from behavior over time] → Anchor the spec delta requirement to the
  endpoints/status codes so future behavior changes must update the doc page too;
  reference the `closure-pinning` spec from the page's footer.
- [`!!!meta.json` is large and hand-edited; a malformed edit breaks the export] →
  Insert the new note object next to the CDC note, copy its exact attribute
  shape, and validate the file parses as JSON before completing.
- [Chosen `noteId` collides with an existing id] → Grep the file for the chosen
  id and confirm zero matches before writing.

## Migration Plan

Pure documentation; no deployment, no runtime change, nothing to roll back.
Revert is a single-commit removal of the page, the meta entry, and the
cross-links. Verification is by inspection: the page renders, `!!!meta.json`
parses, and every documented endpoint/status code matches source.

## Open Questions

- None. Endpoint contract, status codes, and response shapes are all confirmed
  in source; placement follows the existing CDC precedent.

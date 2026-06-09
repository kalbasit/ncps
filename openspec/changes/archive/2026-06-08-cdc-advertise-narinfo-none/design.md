## Context

Under **eager** CDC, the durable representation of a NAR is its uncompressed chunk sequence. ncps has no NAR (re-)compressor (`pkg/xz` decompresses only; `pkg/nar` exposes `DecompressReader` only), and chunks are stored decompressed. Yet while chunking is in progress the pull-path narinfo still advertises the **upstream** compression (e.g. `xz`), so clients request `.nar.xz` — a representation that cannot be reliably served during the chunking window. Observed outcomes today: a 404 → upstream fallback (wasteful) or, worse, decompressed bytes mislabeled as `xz` (silent corruption, proven by the contention e2e in the prior change).

Two facts discovered during investigation reframe this change:

- **The upload path already advertises `none` predictively.** `PutNarInfo` (cache.go:4196-4215) normalizes *every* CDC narinfo to `Compression: none` / `.nar` the moment it is pushed, before any chunk exists. Predictive-`none` is therefore already in production — the **pull path is simply inconsistent with it**.
- **The pull path deliberately refuses to, for a now-obsolete reason.** The store-time switch (cache.go:4092-4118) keeps CDC narinfos at their upstream compression, with a comment justifying it: "a GET of the none URL 404s and a `nix copy` reference check aborts." That failure mode predates `narServability` becoming the single source of truth: a non-servable `none` request now **re-downloads** rather than 404s (the placeholder-regression fix, ncps #1255/#1263/#1279/#1290), and the merged in-flight staging feature makes `none` servable throughout the pull+chunk window.

**Servability model (cache.go:4487 `narServability`)** — the foundation this design leans on:
- whole file in store → servable **and** finished;
- `nar_file` with `total_chunks > 0` → servable **and** finished;
- `nar_file` with `chunking_started_at` set + live producer lock → servable, **not** finished (eager chunking window);
- bare placeholder row (`chunking_started_at == nil`) → **not** servable → triggers re-download, never a terminal 404.

**Eager vs lazy is a global mode** (`cdcLazyChunkingEnabled`, `GetCDCLazyChunkingEnabled()`). Lazy mode intentionally keeps the whole `xz` file in the store and serves `.nar.xz` correctly; eager mode's durable form is chunks-only.

## Goals / Non-Goals

**Goals:**
- For **eager** CDC NARs, advertise `Compression: none` / `.nar` **consistently and predictively** on the pull path — at store time and at serve time — so clients request `.nar` and never `.nar.xz`.
- Bring the pull path into parity with the already-shipping upload path (`PutNarInfo`).
- Eliminate the chunking-window `.nar.xz` request at its source, removing both the 404→fallback and the corruption.
- Cover the cold/triggering client (narinfo fetched before any `nar_file` row exists) and legacy already-stored `xz`-advertised CDC narinfos.

**Non-Goals:**
- No NAR (re-)compressor. This change makes one unnecessary, not possible.
- No change to **lazy** CDC: the whole `xz` file stays and is served as `.nar.xz`. Lazy narinfos are NOT predictively normalized.
- No change to CDC-disabled / non-CDC serving.
- No chunk storage-format change, no flags, no schema, no migrations.
- No redesign of the contention/lifecycle e2e harness (separate follow-up).

## Decisions

### D1 — Normalize eager-CDC narinfos to `none` at store time, mirroring `PutNarInfo`
Add a CDC-eager case to the pull-path store-time switch (cache.go:4102-4118) that rewrites `URL`→`nar/<hash>.nar`, `Compression`→`none`, and nils `FileHash`/`FileSize` — exactly what `PutNarInfo` (4200-4214) already does — **gated on `c.isCDCEnabled() && !c.GetCDCLazyChunkingEnabled()`**.

*Rationale:* the cold/triggering client fetches the narinfo before any `nar_file` row exists; only a persisted (predictive) `none` reaches that client. Store-time is where the upload path already proves this is safe.

*Alternatives considered:*
- **Serve-time only** (extend `maybeCDCNormalizeNarInfoURL` to actively-chunking rows): rejected as the *sole* mechanism — it cannot help the cold client (no row to inspect) and so leaves the most common triggering path on `.nar.xz`. Retained as a backstop (D2), not the primary fix.
- **Leave the asymmetry, fix only the e2e expectation:** rejected — it concedes the corruption/fallback on the pull path, which is the actual production bug.

### D2 — Broaden serve-time `maybeCDCNormalizeNarInfoURL` from "finished" to "eager-CDC"
Today it normalizes only when `HasNarInChunks` (`total_chunks > 0`, finished). Broaden it to also normalize for eager-CDC NARs that are not yet finished, gated identically (`isCDCEnabled && !lazy`). This is the backstop for (a) legacy narinfos already persisted as `xz` before this change and (b) any window where the row exists but chunking has not completed.

*Rationale:* `GetNarInfo` (cache.go:3815-3819) already calls the normalizer for any non-`none` stored narinfo; broadening the gate is a localized change and keeps a single serve-time normalization point.

### D3 — Scope strictly to eager via the global lazy flag
All normalization (store-time and serve-time) is gated on `!c.GetCDCLazyChunkingEnabled()`. Lazy keeps `xz` end-to-end.

*Rationale:* in lazy mode `getNarFromStore` would serve the stored `xz` bytes for a `none` request (corruption) — the exact failure we are removing for eager. The flag is the precise, already-existing discriminator.

### D4 — Rely on existing read-path machinery for truthfulness; add no new persistence
A `.nar` request for an eager-CDC NAR resolves through `narServability`: served from finished chunks, from the actively-chunking producer (in-flight staging / progressive chunks), or — if nothing is materialized (crash/restart, eviction) — routed to a fresh upstream download. No new bookkeeping is introduced.

*Rationale:* the read path already treats placeholder/absent state as "re-download, never 404"; predictive `none` is safe precisely because of this invariant.

## Risks / Trade-offs

- **Lazy-mode mis-serve if the gate is wrong** → Mitigation: single shared gate helper (`isCDCEnabled && !GetCDCLazyChunkingEnabled`) used at every normalization site; explicit unit tests asserting lazy narinfos retain `xz`.
- **Crash/restart window: narinfo says `none`, NAR not yet materialized** → Mitigation: this is no longer a 404 — `narServability` returns not-servable for a placeholder/absent NAR and the read path re-downloads (validated invariant from #1255/#1263/#1279/#1290). Add a regression test for "predictive-none narinfo + no chunks → re-download, not 404."
- **`nix copy` upload reference-check abort (the historical highest-risk area)** → Mitigation: the upload *write* path already advertises `none` (`PutNarInfo`), so this change does not newly alter it; the upload-only *read* path is governed by `isServable` + `narFileBytesStored` (cache.go:4945), which already trusts a peer's stored bytes. Add an explicit upload-path test to lock the behavior in.
- **Client downloads uncompressed `.nar` (larger on the wire than `.nar.xz`) during the eager window** → Trade-off accepted: this merely moves the existing post-chunk `none` advertisement earlier, and removes redundant upstream `.nar.xz` re-fetches; net upstream traffic decreases.
- **Legacy `xz`-advertised CDC narinfos already persisted in the DB** → Mitigation: D2 serve-time backstop normalizes them in-memory on read; no migration/backfill required.

## Migration Plan

- Forward-only behavior change; no schema or data migration. Existing rows are handled by the D2 serve-time backstop.
- Deploy is single-step and HA-safe: a replica on the old code advertises `xz` (today's behavior) while a new replica advertises `none`; both are servable because chunks are uncompressed and `narServability` is shared via the DB. Mixed-version fleets converge without coordination.
- **Rollback:** revert the change; narinfos re-advertise `xz` on next serve (serve-time normalization is in-memory). No persisted state to undo beyond store-time `none` rows, which remain valid (chunks are uncompressed and `.nar`-servable regardless).

## Open Questions

- **Backfill of store-time `none`?** D2 makes a serve-time backstop sufficient, so we likely do NOT persist a rewrite for pre-existing rows. Confirm we are content leaving old rows as `xz`-in-DB (normalized in-memory on read) rather than issuing an async UPDATE — leaning yes (no backfill).
- **`PutNarInfo` lazy behavior is pre-existing and unscoped:** it normalizes to `none` for *all* CDC modes including lazy. This change does not touch it, but should we file a follow-up to gate `PutNarInfo` on `!lazy` for symmetry? (Out of scope here; note for tracking.)
- **Exact gate helper placement:** introduce a small `c.isEagerCDC()` (or inline `isCDCEnabled && !GetCDCLazyChunkingEnabled`) helper used by store-time, serve-time, and tests — decide naming during implementation.

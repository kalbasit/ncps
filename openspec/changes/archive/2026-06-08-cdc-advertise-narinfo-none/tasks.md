## 1. Eager-CDC gate helper

- [x] 1.1 Add a failing unit test for an `isEagerCDC()` (or equivalently named) predicate: true when `isCDCEnabled()` and `!GetCDCLazyChunkingEnabled()`, false when CDC disabled, false when lazy chunking enabled.
- [x] 1.2 Implement the `isEagerCDC()` helper in `pkg/cache/cache.go` and make the test green. This single helper is the shared gate used by store-time and serve-time normalization (design D3).

## 2. Pull-path store-time predictive normalization (design D1)

- [x] 2.1 Add a failing test asserting that storing a narinfo on the pull path with eager CDC active and an upstream `Compression: xz` persists `Compression: none`, URL `nar/<hash>.nar`, and null `FileHash`/`FileSize` (spec: "Pull-path store advertises none for eager CDC"). Done by repurposing the former "Fix B" guard into `TestPullNarInfo_EagerCDC_AdvertisesNoneURL`.
- [x] 2.2 Add a failing test asserting that with lazy chunking enabled the pull-path store retains `Compression: xz` and the `.nar.xz` URL (spec: "Lazy CDC narinfo is NOT predictively normalized"). Done: `TestPullNarInfo_LazyCDC_RetainsXzURL`.
- [x] 2.3 Add a CDC-eager case to the store-time switch (cache.go:4102-4118), gated on `isEagerCDC()`, rewriting URL/Compression and nulling FileHash/FileSize — mirroring `PutNarInfo` (4200-4214). Make 2.1 and 2.2 green. Preserve the existing `none`-upstream and opaque cases unchanged. Done (placed after the opaque case so cachix handling is untouched). Updated the phantom-recovery test to the decompressed payload.
- [x] 2.4 Add a failing test for the cold/triggering client: with eager CDC and no pre-existing `nar_file` row, `GetNarInfo` returns a narinfo advertising `Compression: none` (spec: "Cold client receives none before any nar_file row exists"). Covered by the store-then-return path; explicit returned-narinfo assertion added in slice 3.

## 3. Serve-time normalization broadening (design D2)

- [x] 3.1 Add a failing test asserting that for a legacy narinfo persisted as `Compression: xz` under eager CDC with `HasNarInChunks` false, `GetNarInfo` returns `Compression: none` / `nar/hash.nar` (spec: "Eager chunking normalizes predictively before chunks exist"). Done: `TestGetNarInfo_EagerCDC_NormalizesLegacyXzRowToNone` + flipped `testGetNarInfoCDCEagerNormalizesWhenNotChunked` (placeholder-row variant).
- [x] 3.2 Add failing/guard tests pinning the unchanged lazy behavior: lazy + not-chunked → retain xz, CDC disabled → unchanged. Done: `TestGetNarInfo_LazyCDC_LegacyXzRowNotChunkedStaysXz`, `TestGetNarInfo_CDCDisabled_XzRowStaysXz` (lazy+chunked→none already covered by existing migration tests).
- [x] 3.3 Broaden `maybeCDCNormalizeNarInfoURL` (cache.go:8236-8275) so that under `isEagerCDC()` it normalizes regardless of `HasNarInChunks`, while the lazy/drain path keeps the `HasNarInChunks` gate. Make 3.1 and 3.2 green. Done.

## 4. Read-path truthfulness regression (design D4)

- [x] 4.1 Predictive-none + unmaterialized bytes → `GetNar(nar/hash.nar)` re-downloads (via `lookupPreferredUpstreamURL`: re-fetch upstream narinfo → recover xz URL → re-download → decompress) and serves the full DECOMPRESSED payload, NOT a terminal 404. Covered end-to-end with real xz fixtures by `testCDCBackingLessRecordRecoversAfterTransientFailure` (updated to the decompressed payload). A standalone orphan test was prototyped but removed: `testdata.Nar1.NarText` is random bytes mislabeled as xz, so it can't validate decompression — phantom-recovery (real xz) is the correct guard.
- [x] 4.2 Confirmed the existing read-path machinery satisfies 4.1 — no production change needed. The earlier spike was a false-green (it ran against `main` where the narinfo stayed xz); instrumentation confirmed `lookupPreferredUpstreamURL` correctly recovers the xz upstream for a predictive-none orphan.

## 5. Cross-pod staging interaction (inflight-nar-staging spec)

- [x] 5.1 Cross-pod reader under eager CDC gets `Compression: none`, requests `.nar`, and serves with HTTP 200 — no `.nar.xz`, no fallback. Delivered + verified by the 6.2 contention-e2e assertions (narinfo `none` + all 6 cross-pod readers byte-correct). (The "served specifically from staging" sub-assertion is conformant-but-timing-dependent — the spec permits "staging OR progressive chunks"; see 6.2 note.)
- [x] 5.2 Stale `.nar.xz` request still returns not-found and falls back to upstream, never serving mislabeled bytes (spec: "Stale xz narinfo still falls back defensively"). The behavior already ships as the merged Guard A (`compressedRequestNeedsUpstreamFallback`, unit-tested in `inflight_staging_reader_internal_test.go`); the NEW directly-constructed e2e variant is the deferred additive item (9.b).

## 6. Harness strengthening — assert the Compression VALUE, not just bytes

> Rationale: all three e2e harnesses currently follow whatever the narinfo advertises and assert only on decompressed byte-identity. They never assert the `Compression` field value, so a regression that advertises the wrong compression but is still byte-servable passes silently. These tasks pin the change's intent and restore the `.nar.xz` defensive coverage that becomes unreachable once clients stop requesting `.nar.xz`.

- [x] 6.1 `test-cdc-lifecycle-e2e.py` phase 1 (eager): assert the fetched narinfo advertises `Compression: none` and a URL ending in `.nar` (covers paths A/D). Done + **verified e2e** (sqlite/local): "✓ eager CDC narinfo advertises Compression: none". Lazy-phase compression-value assert intentionally NOT added — under lazy, background chunking races so the narinfo is non-deterministically xz-or-none; lazy is covered deterministically by the Go unit tests instead.
- [x] 6.2 `test-inflight-staging-contention-e2e.py` `--window chunking`: before racing, assert the fetched narinfo advertises `Compression: none`/`.nar` (pins path F's intent, not just its bytes), gated on `cdc`. Done + **verified e2e** (local, 2 replicas, redis): "✓ eager-CDC chunking-window narinfo advertises Compression: none" AND all 6 cross-pod readers returned HTTP 200 with byte-identical-to-canonical content (this window served *corrupt* bytes before this lineage — now correct). NOTE: the phase's separate `staging-must-activate` precondition flaked (`no lock contention observed`) because gcc-unwrapped was cached after the first run, so there was no in-flight download window to contend on — a pre-existing harness timing/caching fragility, orthogonal to this change, and conformant with the inflight-nar-staging spec ("staging OR progressive chunks").
  (Heavier additive harness scenarios moved to §9 Deferred.)

## 7. End-to-end validation

- [x] 7.1 Ran `dev-scripts/test-cdc-lifecycle-auto.sh --db sqlite --storage local`: **full pass** (`✅ sqlite-local: pass`) including the new eager-none asserts (6.1) and the drain/restart/fsck phases — no regression.
- [x] 7.2 Ran `dev-scripts/test-inflight-staging-contention-auto.sh --storage local --window chunking`: the new narinfo-none asserts (6.2) pass and all cross-pod readers serve byte-correct content; the orthogonal staging-activation precondition flaked on package caching (see 6.2 note). Stale-xz/lazy variants deferred (§9).

## 8. Verification and housekeeping

- [x] 8.1 Ran `task fmt`, `task lint` (0 issues), and `task test` (full unit suite green).
- [x] 8.2 Updated CHANGELOG.md (eager-CDC narinfo now advertised as Compression: none / .nar).
- [x] 8.3 Deferred follow-ups recorded — see §9.

## 9. Deferred to a follow-up change (out of scope here)

These are additive harness scenarios and design notes, not part of the production behavior change. The eager→none behavior is fully implemented and verified by the Go unit tests + the cdc-lifecycle and contention e2e runs; these items only broaden test coverage further.

- **(a) No legacy-row backfill.** Existing xz-persisted CDC narinfos are normalized in-memory by the serve-time backstop, so no async UPDATE/migration is issued. Confirmed sufficient.
- **(b) Stale-`xz` defensive e2e variant.** A contention-driver scenario that constructs `.nar.xz` directly (bypassing the narinfo) and asserts 404→upstream-fallback. The behavior already ships as merged Guard A (`compressedRequestNeedsUpstreamFallback`, unit-tested); this is a new e2e to restore path-G coverage at the cross-pod level.
- **(c) Cross-pod lazy coverage.** A `chunking-lazy` window in the contention driver asserting `.nar.xz` serves correctly from the retained whole file across replicas.
- **(d) k8s compression assertion.** `_test_http_endpoints` gated on `cdc_enabled` asserting narinfo `Compression: none` for eager-CDC permutations — pending confirmation that the k8s test-data narinfos flow through the eager-CDC store path (else risks a test-data-dependent false failure); redundant with the lifecycle e2e which pins eager→none via a real `nix copy`.
- **(e) `PutNarInfo` lazy-symmetry.** `PutNarInfo` normalizes CDC narinfos to none for ALL modes including lazy; consider gating it on `!lazy` for symmetry with the pull path. Pre-existing; not regressed by this change.

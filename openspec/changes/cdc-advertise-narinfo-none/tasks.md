## 1. Eager-CDC gate helper

- [ ] 1.1 Add a failing unit test for an `isEagerCDC()` (or equivalently named) predicate: true when `isCDCEnabled()` and `!GetCDCLazyChunkingEnabled()`, false when CDC disabled, false when lazy chunking enabled.
- [ ] 1.2 Implement the `isEagerCDC()` helper in `pkg/cache/cache.go` and make the test green. This single helper is the shared gate used by store-time and serve-time normalization (design D3).

## 2. Pull-path store-time predictive normalization (design D1)

- [ ] 2.1 Add a failing test asserting that storing a narinfo on the pull path with eager CDC active and an upstream `Compression: xz` persists `Compression: none`, URL `nar/<hash>.nar`, and null `FileHash`/`FileSize` (spec: "Pull-path store advertises none for eager CDC").
- [ ] 2.2 Add a failing test asserting that with lazy chunking enabled the pull-path store retains `Compression: xz` and the `.nar.xz` URL (spec: "Lazy CDC narinfo is NOT predictively normalized").
- [ ] 2.3 Add a CDC-eager case to the store-time switch (cache.go:4102-4118), gated on `isEagerCDC()`, rewriting URL/Compression and nulling FileHash/FileSize — mirroring `PutNarInfo` (4200-4214). Make 2.1 and 2.2 green. Preserve the existing `none`-upstream and opaque cases unchanged.
- [ ] 2.4 Add a failing test for the cold/triggering client: with eager CDC and no pre-existing `nar_file` row, `GetNarInfo` returns a narinfo advertising `Compression: none` (spec: "Cold client receives none before any nar_file row exists"). Confirm it passes given 2.3 (store-then-return path); adjust if the returned in-memory narinfo needs normalization parity with the persisted one.

## 3. Serve-time normalization broadening (design D2)

- [ ] 3.1 Add a failing test asserting that for a legacy narinfo persisted as `Compression: xz` under eager CDC with `HasNarInChunks` false, `GetNarInfo` returns `Compression: none` / `nar/hash.nar` (spec: "Eager chunking normalizes predictively before chunks exist").
- [ ] 3.2 Add failing/guard tests pinning the unchanged lazy behavior: lazy + chunked → normalize (existing scenario), lazy + not-chunked → retain xz + trigger migration, CDC disabled → unchanged.
- [ ] 3.3 Broaden `maybeCDCNormalizeNarInfoURL` (cache.go:8236-8275) so that under `isEagerCDC()` it normalizes regardless of `HasNarInChunks`, while the lazy path keeps the `HasNarInChunks` gate. Make 3.1 and 3.2 green.

## 4. Read-path truthfulness regression (design D4)

- [ ] 4.1 Add a failing test for "predictive none + unmaterialized bytes": a narinfo advertising `none` for a hash with no servable `nar_file` (placeholder/absent) → `GetNar(nar/hash.nar)` routes to an upstream (re-)download via `narServability`, NOT a terminal `storage.ErrNotFound` (spec: "Predictive none with unmaterialized bytes triggers re-download, not 404").
- [ ] 4.2 Confirm the existing read-path machinery already satisfies 4.1 (no code change expected); if a gap is found, fix it in the GetNar/`isServable` routing rather than weakening the placeholder invariant (#1255/#1263/#1279/#1290).

## 5. Cross-pod staging interaction (inflight-nar-staging spec)

- [ ] 5.1 Add a failing test/e2e step asserting that under eager CDC a cross-pod reader that fetches the narinfo gets `Compression: none`, requests `.nar`, and serves from staging with HTTP 200 — without requesting `.nar.xz` or falling back to upstream (spec: "Eager-CDC cross-pod reader fetches narinfo then serves .nar from staging").
- [ ] 5.2 Add a guard test that a directly-constructed stale `.nar.xz` request under eager CDC still returns not-found from staging and falls back to upstream, never serving mislabeled uncompressed bytes (spec: "Stale xz narinfo still falls back defensively").

## 6. Harness strengthening — assert the Compression VALUE, not just bytes

> Rationale: all three e2e harnesses currently follow whatever the narinfo advertises and assert only on decompressed byte-identity. They never assert the `Compression` field value, so a regression that advertises the wrong compression but is still byte-servable passes silently. These tasks pin the change's intent and restore the `.nar.xz` defensive coverage that becomes unreachable once clients stop requesting `.nar.xz`.

- [ ] 6.1 `test-cdc-lifecycle-e2e.py` phase 1 (eager): assert the fetched narinfo advertises `Compression: none` and a URL ending in `.nar` (covers paths A/D). Phase 2 (lazy): assert the narinfo retains `Compression: xz` and a `.nar.xz` URL (covers path B). Phase 5 / fsck re-fetches: assert the eager-mode narinfo is still `none`.
- [ ] 6.2 `test-inflight-staging-contention-e2e.py` `--window chunking`: before racing, assert the fetched narinfo advertises `Compression: none`/`.nar` (pins path F's intent, not just its bytes). Keep the byte-identity + staging-activation asserts.
- [ ] 6.3 Add a stale-`xz` defensive variant to the contention driver that constructs the `.nar.xz` URL **directly** (bypassing the narinfo) and asserts 404 → upstream fallback with no mislabeled bytes — restoring path G, which the predictive-none change makes unreachable via the normal narinfo-following path.
- [ ] 6.4 Add cross-pod **lazy** coverage to the contention driver (e.g. a `chunking-lazy` window or `--lazy` flag): assert a `.nar.xz` request is served correctly from the retained whole file across replicas (path B at the cross-pod level).
- [ ] 6.5 k8s `nix/k8s-tests/src/k8s_tests_tester.py` `_test_http_endpoints`: gated on `cdc_enabled`, parse the narinfo `Compression:` line and assert `none` for the eager-CDC permutations (`single-s3-postgres-cdc`, and the 2-replica `ha-s3-postgres-cdc` for the cross-pod case). Low-effort: the harness already fetches and parses the narinfo for its `URL:` line.

## 7. End-to-end validation

- [ ] 7.1 Re-enable and run `dev-scripts/test-cdc-lifecycle-auto.sh` against eager CDC (and lazy phase); confirm readers fetch `.nar` (none) for eager / `.nar.xz` for lazy and serve byte-correct content, with the new compression-value asserts (6.1) green.
- [ ] 7.2 Re-run `dev-scripts/test-inflight-staging-contention-auto.sh --window chunking` (and the new stale-xz + lazy variants); confirm the eager-CDC path serves `.nar` from staging with no digest mismatch and the defensive `.nar.xz` path still falls back. If any remaining harness expectation is wrong by construction, note it as the separate harness-redesign follow-up (out of scope per design Non-Goals).
- [ ] 7.3 Run the CDC k8s permutations (`single-s3-postgres-cdc`, `ha-s3-postgres-cdc`) with the new compression assertion (6.5) green.

## 8. Verification and housekeeping

- [ ] 8.1 Run `task fmt`, `task lint`, and `task test`; resolve any failures.
- [ ] 8.2 Update CHANGELOG / docs if narinfo advertising behavior for eager CDC is user-observable.
- [ ] 8.3 File the two deferred follow-ups noted in design Open Questions (no legacy-row backfill confirmed; `PutNarInfo` lazy-symmetry gating) as tracking notes; do not implement here.

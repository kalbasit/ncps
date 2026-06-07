## 1. Reproduce (TDD red)

- [x] 1.1 Write a failing test: a chunked `nar_file` for `H`, an **unlinked** narinfo with a nix-serve-style prefixed URL `nar/<narinfoHash>-<H>.nar.xz` carrying a NarHash; invoke `MigrateChunksToNar(H)` and assert it resolves the verification NarHash (currently it can't — `LinkedNarinfoNarHash` misses the prefixed URL)
- [x] 1.2 Write a failing test: after de-chunk, the prefixed-URL unlinked narinfo's URL is normalized to `nar/<H>.nar` / Compression none (currently left as `.nar.xz`, so a later serve 404s)

## 2. Hash-aware match helper (TDD green)

- [x] 2.1 Add a shared helper that decides whether a narinfo references a NAR hash by parsing+normalizing its URL (`Normalize(ParseURL(url)).Hash == H`), covering canonical and prefixed URLs; treat unparseable URLs as non-matching
- [x] 2.2 Use the join link as the primary path; fall back to the hash-aware scan only when no link exists

## 3. Apply at all three sites

- [x] 3.1 `LinkedNarinfoNarHash` — resolve the verification NarHash via the hash-aware match
- [x] 3.2 `MigrateChunksToNar` (flip transaction) — normalize the URLs of hash-aware-matched referencing narinfos
- [x] 3.3 `NormalizeChunkedNarInfoURL` — same hash-aware match
- [x] 3.4 Confirm the join-link primary path and canonical-URL matches still behave identically (no regression in existing de-chunk tests)

## 4. Verify

- [x] 4.1 The red tests from section 1 now pass
- [x] 4.2 `task fmt`, `task lint`, and `task test` all exit zero

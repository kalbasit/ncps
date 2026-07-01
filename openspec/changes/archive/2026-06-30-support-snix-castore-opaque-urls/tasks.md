## 1. Parser: tolerate `.nar`-less opaque upstream URLs (TDD)

- [x] 1.1 Add failing unit tests in `pkg/nar/url_test.go` for `ParseUpstreamURL` on a snix-castore URL `nar/snix-castore/<blob>?narsize=7415800` with a valid fallback hash: asserts no error, `IsOpaque()` true, `Compression == none`, `Hash == fallbackHash`, `OpaquePath()` == `nar/snix-castore/<blob>`, and `Query` contains `narsize=7415800`.
- [x] 1.2 Add failing test that `ParseURL` (strict) STILL rejects the `.nar`-less URL with `ErrInvalidURL` (serve/storage parser unchanged).
- [x] 1.3 Add failing test that a `.nar`-less URL with an empty/invalid fallback hash returns a parse error and does not fabricate a key.
- [x] 1.4 Add failing test that reconstructing the upstream URL via `JoinURL` round-trips the query string (`...?narsize=7415800`).
- [x] 1.5 Implement in `pkg/nar/url.go`: when `parseURLParts` finds no `.nar` token, `ParseUpstreamURL` builds an opaque `URL` (opaquePath = path sans query, `Query` retained, `Compression = none`, `Hash = fallbackHash`); keep `ParseURL` strict. Make 1.1–1.4 green.
- [x] 1.6 Run `task lint` and `task test` for `pkg/nar`; confirm green.

## 2. Cache pull/serve path wiring (TDD)

- [x] 2.1 Add a failing `pkg/cache` unit test: a stub upstream returning a snix-style narinfo (opaque `.nar`-less `URL:`, `Compression: none`, valid `NarHash`) is fetched via `GetNarInfo` without a 500, and the re-served narinfo `URL:` is ncps's own `nar/<narhash>.nar` with `Compression: none`.
- [x] 2.2 Verify/adjust `pkg/cache/cache.go` pull path (`pullNarInfo` around the `ParseUpstreamURL` call and the compression switch) so `upstreamNarPath` (incl. query) is captured before the compression branch and the opaque path is persisted regardless of the `none` branch being taken.
- [x] 2.3 Add a failing test for eviction/re-fetch: an opaque-`none` NAR whose local bytes are gone is re-fetched using the persisted opaque path (with query) rather than ncps's local hash-named URL.
- [x] 2.4 Implement any wiring needed to make 2.1 and 2.3 green.
- [x] 2.5 Run `task lint` and `task test` for `pkg/cache`; confirm green.

## 3. Integration coverage

- [x] 3.1 Add an integration test exercising the full loop against a snix-castore-shaped upstream fixture: narinfo pull → NAR GET (query preserved) → store as `none` → serve `<hash>.narinfo` (200) → serve `nar/<narhash>.nar` and verify identical NAR bytes.
- [x] 3.2 Ensure the fixture asserts the upstream GET received the `?narsize=N` query (regression guard for the snix `400`-without-query behavior).

## 4. Docs and changelog

- [x] 4.1 Add a `## [Unreleased]` → `### Fixed` entry to `CHANGELOG.md` describing snix-castore / `.nar`-less opaque upstream URL support and the fixed `HTTP 500 "invalid nar URL"`.
- [x] 4.2 Update `docs/` (Developer Guide → Architecture → Request Flow, and any upstream/URL section) to document that narinfo `URL:` is treated as an opaque path per the Nix binary-cache protocol, including the snix-castore `.nar`-less form.
- [x] 4.3 If docs are generated/aggregated (`docs/docs.md`, `docs/!!!meta.json`), regenerate or update them consistently.

## 5. Verify

- [x] 5.1 Run `task fmt`, `task lint`, and `task test`; confirm each exits 0.
- [x] 5.2 Run `openspec validate support-snix-castore-opaque-urls --no-interactive` (with `OPENSPEC_TELEMETRY=0`, `HOME=$TMPDIR`) and confirm it passes.
- [x] 5.3 Manually confirm against a real snix narinfo shape (e.g. `nar/snix-castore/<blob>?narsize=N`, `Compression: none`) that parsing succeeds and no 500 is produced.

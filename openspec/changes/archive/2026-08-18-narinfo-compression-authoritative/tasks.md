## 1. Lock in the failing tests

- [x] 1.1 Confirm the existing RED test `TestAtticNarInfoURLWithoutCompressionExtension` (`pkg/cache/attic_narinfo_url_compression_test.go`) fails on both subtests for the reasons expected: the desync assertion on both, and the byte-contract assertion on the transport-level subtest only.
- [x] 1.2 Add a RED unit test in `pkg/nar` asserting `ParseUpstreamURL` resolves compression from a declared `Compression:` when the URL carries no extension, covering: bare `.nar` + `zstd`, bare `.nar` + `none`, `.nar`-less opaque + `none`, and an explicit `.nar.xz` extension winning over a conflicting declared value.
- [x] 1.3 Add a RED unit test asserting `ParseURL` (the strict parser for ncps's own URLs) is unchanged by the new resolution.

## 2. Resolve compression at the parse boundary

- [x] 2.1 Extend `nar.ParseUpstreamURL` to take the narinfo's declared compression and apply it when `parseURLParts` yields no extension, for both the opaque and the conventional hash-named paths. Update the doc comment to state the precedence rule.
- [x] 2.2 Update the caller in `pullNarInfo` (`cache.go:4276`) to pass `narInfo.Compression`.
- [x] 2.3 Update the caller in `lookupPreferredUpstreamURL` (`cache.go:1821`) to pass `upstreamNarInfo.Compression`, and verify its `originalURL.Compression == none` gate now behaves correctly for a bare-`.nar` compressed upstream.
- [x] 2.4 Run tasks 1.2 and 1.3 to green.

## 3. Emit a truthful narinfo URL

- [x] 3.1 Ensure the narinfo ncps re-serves carries the resolved compression extension when the upstream URL had none — verify whether the existing `case narURL.IsOpaque()` arm already produces this once fed the resolved value, and extend the switch to cover the conventional hash-named shape if it does not.
- [x] 3.2 Correct the "preserving compression" comment on the `IsOpaque` arm to state what is actually preserved.
- [x] 3.3 Verify `storeInDatabase`'s re-parse of the emitted URL now yields the resolved compression, so the `nar_file` row and the narinfo agree. Add an assertion to the test from 1.1 if not already covered.

## 4. Confirm the downstream effects land without further edits

- [x] 4.1 Verify `putNarInStore` no longer re-compresses an already-compressed NAR (assert the stored object is a single zstd layer, not nested).
- [x] 4.2 Verify `ensureNarFileRecord` reconciles `file_size` to the stored byte count on the resolved-compression row, confirming no `narFileSize` change is needed.
- [x] 4.3 Run the content-level subtest of `TestAtticNarInfoURLWithoutCompressionExtension` to green.

## 5. Guard the unaffected paths

- [x] 5.1 Confirm the snix-castore `.nar`-less scenario still resolves to `none` and its query string still survives verbatim.
- [x] 5.2 Confirm cachix (`nar/<uuid>.nar.zst`), Harmonia (`Compression: none` + transport zstd), nix-serve prefixed URLs, and cache.nixos.org (`.nar.xz`) are unchanged — the existing tests for each must stay green.
- [x] 5.3 Confirm the transport-level subtest still fails, and annotate it so it is unmistakably the separate follow-up change rather than a regression in this one.

## 6. Verify and finish

- [x] 6.1 Run `task fmt`, `task lint`, and `task test`; all must exit zero apart from the deliberately-still-red transport-level subtest handled in 5.3.
- [x] 6.2 Run `openspec validate --no-interactive` for this change.
- [x] 6.3 Update `CHANGELOG.md` with the fix, referencing issue #1470.

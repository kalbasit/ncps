## 1. Pin the request-side contract

- [x] 1.1 Add a RED table test in `pkg/cache/upstream` asserting what `GetNar` puts on the wire per resolved compression: `none` sends `Accept-Encoding: zstd`; `zstd` and `xz` do not. Assert against the header the fake upstream actually receives, not against internals.
- [x] 1.2 Confirm it fails only on the compressed rows (the `none` row must already pass, proving the test is not vacuous).

## 2. Gate the negotiation

- [x] 2.1 In `upstream.GetNar`, build the `Accept-Encoding: zstd` mutator only when `narURL.Compression == nar.CompressionTypeNone`, leaving caller-supplied mutators applied in both branches.
- [x] 2.2 Update the `GetNar` doc comment to state the rule and why it exists (a compressing proxy wrapping already-compressed content).
- [x] 2.3 Run task 1.1 to green.
- [x] 2.4 Confirm the transparent `Content-Encoding: zstd` decompression below is untouched and still unconditional.

## 3. Close the gap the skipped test described

- [x] 3.1 Make the fake upstream in `TestAtticNarInfoURLWithoutCompressionExtension` apply `Content-Encoding: zstd` only when the request advertises it, so it models a real compressing proxy rather than an upstream that lies unconditionally.
- [x] 3.2 Delete the `t.Skip` from the transport-level subtest and confirm it now passes: the served bytes must decode under the `Compression:` the narinfo advertises.
- [x] 3.3 Update the test's header comment — the transport variant is now covered rather than deferred — and remove the wording that describes it as an open gap.

## 4. Guard the paths that must not change

- [x] 4.1 Confirm Harmonia (`Compression: none` + transport zstd) still negotiates and still round-trips: the `none` row of 1.1 plus the existing upstream tests must stay green.
- [x] 4.2 Confirm the single production call site (`getNarFromUpstream`) needs no change, and that the CDC preferred-download path — which fetches the compressed URL — is correct under the new rule.
- [x] 4.3 Confirm no downstream/server-side `Accept-Encoding` behaviour changed (`pkg/server`): its tests must stay green.

## 5. Verify and finish

- [x] 5.1 Run `task fmt`, `task lint`, and `task test`; all must exit zero, with no remaining `t.Skip` in `pkg/cache/attic_narinfo_url_compression_test.go`.
- [x] 5.2 Run `openspec validate --no-interactive --strict` for this change.
- [x] 5.3 Add a `CHANGELOG.md` entry under Fixed, describing the proxy interaction rather than just the header change.

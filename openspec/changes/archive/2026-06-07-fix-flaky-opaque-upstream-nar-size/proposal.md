## Why

`TestGetNarInfoOpaqueUpstreamURL` (`pkg/cache/cache_test.go:529`) flakes in CI with `expected: 50308, actual: -1`. The test asserts the exact NAR size returned by the first `GetNar`, but `GetNar` legitimately returns `-1` (unknown size) when it serves the NAR via the live-streaming path while the upstream download is still in flight. Whether the download finishes before `GetNar` chooses its path is timing-dependent, so under CPU contention (e.g. the parallel integration-test cohorts) the assertion fails even though the streamed bytes are always correct. The contract that `-1` is a valid return is real behavior but was never written down, which is why the test author assumed the concrete size.

## What Changes

- Make the size assertion in `TestGetNarInfoOpaqueUpstreamURL` tolerate the streaming path: keep asserting on the streamed bytes (always reliable) and accept either the concrete size or the `-1` unknown-size sentinel for the in-flight download case.
- Document the `GetNar` returned-size contract in the `nar-concurrent-streaming` capability so the live-streaming `-1` return is an explicit, testable requirement rather than undocumented behavior.
- Correct the stale `Cache.GetNar` and `storage.NarStore`/`NarInfoStore` signatures in the `api-surface` capability to match the real code (the documented `GetNar` signature predates the `(nar.URL, int64, io.ReadCloser, error)` return; the `NarStore` block omits `StatNar`/`WalkNars`, lists a phantom `GetNarSize`, and shows outdated `GetNar`/`PutNar` shapes). Spec-doc accuracy only — no code change.

## Capabilities

### New Capabilities
<!-- none -->

### Modified Capabilities
- `nar-concurrent-streaming`: add a requirement specifying that `GetNar` returns `-1` for the size when serving a download that is still in flight (live-streaming path), and the concrete `nar_files.file_size` when serving a fully-available NAR from store or chunks. Consumers (and tests) MUST treat `-1` as "size unknown".
- `api-surface`: correct the `Cache NAR Operations` and `Storage Interfaces` requirement code blocks so the documented `Cache.GetNar`, `NarStore`, and `NarInfoStore` signatures match the real Go types.

## Impact

- `pkg/cache/cache_test.go` — relax the racy size assertion (test-only; no production behavior change).
- `openspec/specs/nar-concurrent-streaming/spec.md` — new requirement documenting the existing `GetNar` size contract (`cache.go:1449` returns `size = -1` on the streaming path).
- `openspec/specs/api-surface/spec.md` — corrected stale `Cache.GetNar`/`NarStore`/`NarInfoStore` signatures (documentation only).
- No production code changes; `pkg/cache/cache.go` and `pkg/storage` behavior is unchanged.

## Context

`Cache.GetNar` (`pkg/cache/cache.go:1096`) returns `(nar.URL, int64, io.ReadCloser, error)`. The `int64` size is the concrete `nar_files.file_size` when the NAR is served from store or chunks, but it is `-1` when the NAR is served via the per-client live-streaming path — the branch reached when the artifact is not yet fully available and an upstream download is in flight (`size = -1` at `cache.go:1449`). At that point the final size is genuinely unknown; the size is delivered implicitly as the streamed bytes.

`TestGetNarInfoOpaqueUpstreamURL` calls `GetNar` immediately after `GetNarInfo` and asserts `assert.Equal(int64(len(narBody)), size)` (`cache_test.go:529`). Which path `GetNar` takes depends on whether the small (~50 KB) upstream download finishes before `GetNar` evaluates `ds.closed`. Without load it usually finishes first (size known); under CPU contention the streaming path wins and `size == -1`, failing the assertion. The streamed bytes are correct in both cases (the `got == narBody` assertion on the next line passes).

## Goals / Non-Goals

**Goals:**
- Make the test deterministic regardless of download/scheduling timing.
- Write down the `GetNar` returned-size contract so the `-1` streaming sentinel is documented, not folklore.

**Non-Goals:**
- Changing any production behavior in `pkg/cache/cache.go` (the `-1` sentinel is intentional and correct).
- Forcing `GetNar` to always serve from store (would defeat progressive streaming and reintroduce TTFB latency).
- Rewriting the stale `GetNar` signature elsewhere in `api-surface` (out of scope).

## Decisions

- **Assert on bytes, tolerate the size sentinel.** Keep the authoritative `assert.Equal([]byte(narBody), got)` check, and change the size assertion to accept either the concrete size or `-1`, e.g. `assert.True(t, size == int64(len(narBody)) || size == -1)`. The byte-equality check already proves correctness regardless of path.
  - *Alternative — block until the NAR is in store before asserting* (poll `HasNarInStore`): rejected; it tests a different path than the one production clients hit and adds timing assumptions of its own.
  - *Alternative — assert only `len(got)`* and drop the size check: viable, but explicitly allowing `-1` keeps the size return covered and documents intent.
- **Document the contract in `nar-concurrent-streaming`.** Add a requirement that `GetNar` returns `-1` (size unknown) on the live-streaming path and the concrete `file_size` on the from-store/chunks path, with a scenario asserting bytes are correct in both. This turns the previously implicit behavior into a checkable spec, preventing the next test from making the same wrong assumption.

## Risks / Trade-offs

- [Relaxing the assertion could mask a genuine regression where size is wrongly `-1` on the from-store path] → Mitigation: the byte-equality assertion still fails on any actual data corruption, and the accepted set is exactly `{file_size, -1}` — any other value (e.g. `0`) still fails.

## Open Questions

- None.

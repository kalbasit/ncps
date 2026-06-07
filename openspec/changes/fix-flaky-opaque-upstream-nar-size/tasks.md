## 1. Reproduce

- [x] 1.1 Confirm the flake locally: run `go test ./pkg/cache -run '^TestGetNarInfoOpaqueUpstreamURL$' -count=60 -race` while saturating all CPU cores, and observe `cache_test.go:529 expected: 50308 actual: -1`.

## 2. Fix the test

- [x] 2.1 In `pkg/cache/cache_test.go`, replace the racy `assert.Equal(int64(len(narBody)), size)` at line ~529 with an assertion that accepts both the from-store size and the streaming sentinel, e.g. `assert.True(t, size == int64(len(narBody)) || size == -1, "size should be the file size or -1 (streaming)")`.
- [x] 2.2 Keep the authoritative byte-equality assertion (`assert.Equal([]byte(narBody), got)`) immediately after it unchanged — it proves correctness on both paths.
- [x] 2.3 Audit the rest of the test (the post-eviction re-fetch at ~line 539+) for the same assumption and relax any other exact-size assertion that can hit the streaming path the same way.

## 3. Verify

- [x] 3.1 Re-run the stress reproduction from 1.1 and confirm it now passes deterministically under load.
- [x] 3.2 Run `task fmt`, `task lint`, and `task test` and confirm all exit 0.

## 4. Spec accuracy (api-surface signatures)

- [x] 4.1 Cross-check the `Cache.GetNar` signature in the delta against `pkg/cache/cache.go` and the `NarStore`/`NarInfoStore` blocks against `pkg/storage/store.go` to confirm they match exactly (no code change — spec docs only).

## 5. Spec sync

- [x] 5.1 Confirm `openspec validate fix-flaky-opaque-upstream-nar-size --no-interactive` passes.
- [ ] 5.2 During archive, fold the `nar-concurrent-streaming` delta into `openspec/specs/nar-concurrent-streaming/spec.md` (the `GetNar` size contract) and the `api-surface` deltas into `openspec/specs/api-surface/spec.md` (corrected signatures) so both land in the canonical specs.

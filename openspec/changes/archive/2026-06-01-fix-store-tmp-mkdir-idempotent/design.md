## Context

`pkg/storage/local/local.go` `setupDirs()` is called on every pod startup. It currently calls `os.RemoveAll(s.storeTMPPath())` before creating the tmp directory, intending to start each process with a clean tmp directory.

In production, the local storage backend runs on a shared NFS RWX PVC across two replicas. During a rolling update, the incoming pod's `setupDirs()` runs while the outgoing pod is still serving and may have in-flight downloads in `store/tmp`. `RemoveAll` on a shared PVC races with those active writes and removes files the outgoing pod is using — either crashing the incoming pod (if `RemoveAll` returns an error) or corrupting the outgoing pod's in-flight work.

The write path already uses an atomic tmp-then-rename pattern, so partial tmp files left behind by crashed pods are benign.

## Goals / Non-Goals

**Goals:**
- Make `setupDirs()` idempotent and safe to call when the tmp directory already exists on the shared PVC.
- Eliminate the rolling-update crashloop.

**Non-Goals:**
- Per-pod tmp namespacing (not required; leftover partials are harmless).
- Cleaning up stale tmp files on startup (out of scope; acceptable operational trade-off).
- S3 or chunk storage backends (not affected by this change).

## Decisions

### Remove `os.RemoveAll` from `setupDirs()`

**Decision:** Delete the `os.RemoveAll(s.storeTMPPath())` call and its error check from `setupDirs()`. The immediately following `os.MkdirAll(p, dirMode)` loop already handles both "directory exists" and "directory doesn't exist" correctly.

**Alternatives considered:**
- *Rename to per-pod tmp dir (e.g. `store/tmp/<pod-uuid>`)* — would scope cleanup correctly but requires plumbing pod identity into the store, adds complexity, and makes no difference for correctness since partials are already safe.
- *Keep `RemoveAll` but only on non-shared mounts* — requires detecting mount type at runtime; fragile and unnecessary.

## Risks / Trade-offs

- [Stale tmp files accumulate if pods crash mid-download] → Acceptable: files are partial and never promoted to the NAR path. An operator can run `rm -rf store/tmp/*` manually during a maintenance window. A future periodic-cleanup task could address this, but is out of scope here.

## Migration Plan

1. Remove the three lines (`RemoveAll` call + error check + blank line) from `setupDirs()`.
2. Unit test: add a test that calls `setupDirs()` twice on the same directory and asserts it succeeds both times.
3. Deploy the new image. ArgoCD rolling update will no longer trigger the crashloop.
4. No rollback complexity — the removed code only worsened availability; reverting would reintroduce the bug.

## Open Questions

_(none)_

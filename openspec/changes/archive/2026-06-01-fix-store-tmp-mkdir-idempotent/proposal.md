## Why

During a rolling update, the incoming pod calls `setupDirs()` which unconditionally deletes the `store/tmp` directory on the shared PVC — wiping any in-flight downloads the outgoing pod owns and returning an error if `RemoveAll` races with active writes, causing an immediate crashloop. We need to fix this before ArgoCD re-applies and re-triggers the crash.

## What Changes

- Remove the `os.RemoveAll(s.storeTMPPath())` call from `setupDirs()` in `pkg/storage/local/local.go`. The subsequent `os.MkdirAll` is already idempotent and handles both the "dir exists" and "dir doesn't exist" cases.
- Each pod will inherit whatever tmp files prior pods left behind; stale partial files are harmless — the write path uses atomic rename (tmp → final) so they are never surfaced as valid entries.

## Non-goals

- Per-pod tmp namespacing (subdirectory per pod UID) — out of scope; leftover tmp files from crashes already converge safely.
- S3/chunk storage backends — not affected; this is local-backend only.
- Startup tmp-dir validation (`EnsureDirWritable`) — kept as-is; it only writes and removes a probe file, which is safe on a shared PVC.

## Capabilities

### Modified Capabilities

- **Local store initialization** (`pkg/storage/local/local.go` `setupDirs`): no longer purges `store/tmp` on startup; directory creation is idempotent via `os.MkdirAll`.

### New Capabilities

_(none)_

## Impact

- `pkg/storage/local/local.go` — remove 3 lines (`RemoveAll` call + error check).
- No API, config, or migration changes.
- No I/O, latency, or memory impact.
- Rolling updates on a shared PVC become safe; single-node deployments are unaffected.

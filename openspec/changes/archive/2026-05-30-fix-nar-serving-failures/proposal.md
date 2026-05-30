## Why

Production clients still fail `nix build` against ncps despite the #1296/#1297/#1298 recovery work. A fresh client (no local nix cache) hit three distinct server-side faults in one run: hundreds of fast `404 does not exist in binary cache`, many `unexpected end of nar` / `truncated input` mid-stream, and `504 Gateway Time-out`. Two existing specs already forbid exactly these outcomes, so the implementation is out of compliance — these are bugs, not new features.

## What Changes

- **Orphaned-narinfo NAR requests must recover, not 404.** When a narinfo is in the DB but its NAR is absent from storage (`cache.go:4240` "requesting a purge"), a subsequent `GET /nar/<hash>.nar.xz` 404s in ~1.5ms instead of re-downloading from upstream. The purge path leaves the system unable to heal the NAR on the in-flight request. `GetNar` must drive an upstream re-download for a known-but-backing-less NAR rather than short-circuiting to `ErrNotFound`.
- **CDC chunk serving must never emit a truncated 200.** Reassembly stalls (`cache.go:7430` "timeout waiting for chunk N after 30s") fire *after* the `200 OK` and partial body are flushed, so the stream is reset and the client sees a corrupt/short `.nar.xz`. Verify chunk availability before committing bytes and/or cap total stall well under the gateway timeout so a stalled chunk is surfaced as a failed transfer, not a silent short body.
- **Bound per-NAR serving latency below nginx's gateway timeout** so chunk/upstream stalls return a retryable error instead of a 504. Expose the chunk-wait/serving deadline as configuration (today the 30s wait is implicit) so operators can align it with their gateway.
- **Document storage-backend selection guidance.** The `local` filesystem backend assumes single-writer POSIX semantics; pointing it at a network filesystem (NFS/SMB) shared by more than one replica yields close-to-open-consistency hazards (false cache-miss detection, stale-size reads). Multi-replica deployments should use an object-store backend; CDC chunking suits low-latency storage and is counter-productive on high-latency/spinning backends. Framed as what-to-do-and-why, not a post-mortem.
- Add regression tests reproducing each fault (orphaned-narinfo → recovery; stalled chunk after first byte → no short 200; serving deadline → bounded error).

## Capabilities

### New Capabilities
<!-- none -->

### Modified Capabilities
- `nar-cache-miss-recovery`: extend beyond backing-less `nar_file` rows to cover the **narinfo-in-DB / NAR-missing-in-storage** purge path — the direct `/nar` request that follows a purge MUST recover from upstream, not 404.
- `nar-concurrent-streaming`: strengthen the "MUST NOT deliver a truncated NAR body" requirement to cover stalls that occur **after the first byte is committed**, and bound total serving time below the gateway timeout.

## Impact

- Code: `pkg/cache/cache.go` (`GetNar`, `getNarInfoFromDatabase` purge path, `getNarFromChunks`/`getNarFromStore` reassembly), `pkg/server/server.go` (`getNar` handler error mapping).
- Behavior: a NAR whose chunks/whole-file are genuinely gone is re-fetched from upstream on demand; stalls become retryable errors instead of truncated 200s or 504s. No schema/migration change.
- **I/O / latency / memory**: on-demand recovery adds an upstream round-trip for orphaned NARs (one-time per heal). A new per-request serving deadline trades an unbounded stall for a fast retryable failure. No additional buffering — streaming stays O(1) memory.

## Non-goals

- Not redesigning CDC chunking, the distributed lock, or upstream retry/backoff.
- Not eliminating benign `http2: timeout awaiting response headers` retries or download-lock contention warnings.
- Not changing nginx/ingress configuration; the fix keeps ncps responses within whatever timeout the gateway enforces.
- Not providing the reverse `migrate-chunks-to-nar` (de-chunking) tool — that is a net-new feature tracked as a **separate change**. This change's docs reference it as the supported CDC-exit path.

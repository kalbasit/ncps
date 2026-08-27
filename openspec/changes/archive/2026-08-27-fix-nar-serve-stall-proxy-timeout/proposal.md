## Why

Production ncps (v0.10.0-rc17) answers NAR requests with `HTTP 200` and a truncated body; nix
reports `Truncated zstd input` and `curl error 92: HTTP/2 stream reset by server`. A goroutine
dump captured mid-stall proves the cause: `GetNar`'s storage-presence probe blocks for ~57 s in a
single uncancellable `fstatat` syscall, and the ingress's 60 s `proxy_read_timeout` aborts the
stream first. A 2,021-byte NAR took 56.87 s to first byte. See `investigation.md` and
`goroutine-stall-dump.txt`.

ncps places no bound on a NAR request's time-to-first-byte, and emits no log, span, or metric
while it waits. A slow storage layer therefore becomes silent data corruption at the client
instead of a fast, honest error.

## What Changes

- The storage presence probe used by `GetNar` gains a **deadline**. When it expires, the request
  resolves promptly instead of blocking indefinitely.
- The probe runs so that the **request context can abandon the wait**. `os.Stat` takes no context
  and cannot be cancelled, so the local backend must not block the request goroutine on it.
- A NAR request gains a **bounded time-to-first-byte**: either bytes begin flowing or the request
  fails cleanly, always well inside a reverse proxy's read timeout.
- The probe becomes **observable**: a slow or timed-out probe emits a log line, span, and metric
  rather than 57 s of silence.
- Regression coverage: a store wrapper whose `StatNar` blocks for N seconds, asserting `GetNar`
  still yields a first byte or a clean error within a budget far below N; and an e2e scenario
  asserting time-to-first-byte rather than byte-correctness alone.

## Non-goals

- Fixing the NFS substrate. Mount tuning (`nconnect`), and migrating multi-replica deployments to
  the S3 backend, are infrastructure work tracked outside this change.
- Correcting the invalid `nginx.ingress.kubernetes.io/proxy-read-timeout: "300s"` annotation
  (deployment repo).
- Making slow storage fast. This change bounds and surfaces the latency; it does not remove it.
- Changing the default storage backend or deprecating the `local` backend.
- Altering CDC, chunking, in-flight staging, or download-coordination behaviour.

## Capabilities

### New Capabilities

- `nar-serving-latency-bounds`: bounded time-to-first-byte for NAR requests, a deadline on the
  storage presence probe, and the observability required to distinguish a slow probe from an
  absent asset.

### Modified Capabilities

- `unified-e2e-harness`: gains a scenario requirement asserting time-to-first-byte on NAR reads,
  so a correct-but-slow response is a FAIL rather than a PASS.

## Impact

- **Code**: `pkg/cache/cache.go` (`GetNar`, `HasNarInStore`, `statNarInStore`),
  `pkg/storage/local` (`StatNar`), plus test scaffolding and the e2e harness assertions.
- **I/O**: unchanged in volume. The same presence probes are issued; only their wait is bounded.
- **Network latency**: strictly improved. Requests that would have hung ~57 s and been killed by
  the proxy now resolve inside the budget.
- **Memory**: a bounded probe that outlives its deadline leaves one goroutine (and, for the
  uncancellable `os.Stat`, one OS thread) parked until the syscall returns. This is bounded by
  concurrent NAR requests against stalled storage and must be capped and measured in `design.md`.

# Investigation: "Truncated zstd input" / HTTP/2 INTERNAL_ERROR on v0.10.0-rc17

Date: 2026-08-26. Cluster: pve-cluster-prod0, ns `ncps`, image `ghcr.io/kalbasit/ncps:v0.10.0-rc17`,
2 replicas (`ncps-5f9fc6fff8-4d45d` = 10.244.9.222, `ncps-5f9fc6fff8-w76sd` = 10.244.19.89).

## Client symptom

```
error: unable to download 'https://ncps.nasreddine.com/nar/<h>.nar.zst':
  HTTP error 200 () (curl error code=92: HTTP/2 stream N reset by server (error 0x2 INTERNAL_ERROR))
error (ignored): error: failed to read compressed data (Truncated zstd input)
```

## Evidence

### 1. The stored bytes are fine
All six NARs from the failing run serve a complete, zstd-valid body on retry, with
`Content-Length` matching the narinfo `FileSize` exactly. This is **not** corruption
and **not** a compression-desync.

### 2. nginx-ingress aborted the streams
`ingress-nginx-controller-58949cdb9-v8jmv` error log, six entries on one connection
(`*19013357`), one per failing NAR, all upstream `10.244.19.89:8501`:

```
[error] upstream timed out (110: Operation timed out) while reading upstream,
  client: 192.168.100.10, request: "GET /nar/1w7dvc....nar.zst HTTP/2.0"
192.168.100.10 - - "GET /nar/1w7dvc....nar.zst HTTP/2.0" 200 1621071 "curl/8.21.0 Lix/2.94.2"
   57 76.111 [ncps-ncps-http] [] 10.244.19.89:8501 1621944 76.111 200
```

- `body_bytes_sent` 1621071 vs `Content-Length` 1624995 -> **3924 bytes short**.
- `upstream_response_time` 76.1 s; the other five were 96.5 / 103.2 / 107.3 / 111.0 / 112.1 s.

### 3. The proxy read timeout is 60 s, not the configured 300 s
Rendered vhost for `ncps.nasreddine.com`:

```
proxy_connect_timeout 5s;  proxy_send_timeout 60s;  proxy_read_timeout 60s;
proxy_buffering off;       proxy_request_buffering off;  proxy_http_version 1.1;
```

The Ingress carries `nginx.ingress.kubernetes.io/proxy-read-timeout: 300s`. That annotation
takes a **bare integer of seconds**; `"300s"` fails validation and is silently dropped, so the
60 s default applies. Every ncps response slower than 60 s is killed mid-flight.

### 4. ncps itself stalls far past 60 s
ncps app log, same rollout: 15 `/nar/` requests with `elapsed` ~= 60000 ms, **8 of them with
`status: 0, bytes: 0`** -- killed at exactly the nginx timeout having emitted nothing.

### 5. Reproduced deterministically, bypassing nginx
Port-forward straight to each pod. Request the narinfo on pod A (A pulls + stores the NAR),
then request the NAR on pod B, measuring true TTFB:

| package  | NAR size    | TTFB on pod B |
|----------|-------------|---------------|
| blender  | 108,662,723 | **107.5 s**   |
| openjdk17| 349,188,671 | **57.4 s**    |
| gcc14    | **12,524**  | **56.8 s**    |
| wine64   |  66,189,221 | **57.8 s**    |
| godot_4  |  69,967,331 | **57.5 s**    |

`gcc14` is decisive: a **12 KB** NAR, 56.8 s to first byte. Not bandwidth, not NAR size.
ncps's own request log agrees: `elapsed: 56800.09 ms`.

Controls: the same NAR re-fetched once cached -> 8-170 ms on both pods. Same-pod request -> 8 ms.
So neither pod is broadly slow.

### 6. Pod B is idle and completely silent during the stall
Pod A finished the entire narinfo + NAR download **within one second** (03:17:50) -- before pod B's
request even started. Pod B's full debug log for the stall window:

```
03:17:49 info  /nar/1yg0qcp3...nar.zst  handled request      <- previous request
03:18:37 debug upstream is healthy   (x5, the 1-minute healthcheck tick)
03:18:48 debug /nar/1w68g6h9...nar.zst withEntTransaction: starting transaction
03:18:48 info  /nar/1w68g6h9...nar.zst handled request       elapsed=56800 ms
```

Nothing between 03:17:51 and 03:18:48. No lock-contention warning, no upstream fetch, no DB call.
The handler is parked somewhere that logs nothing and is not gated on the peer finishing.

## Failure chain (established)

1. A NAR GET lands on the replica that is **not** the one that pulled it (2 replicas, no session
   affinity: nix fetches the narinfo, which triggers the pull, then fetches the NAR).
2. That replica's `GetNar` blocks for ~57 s (up to 107 s observed) emitting nothing.
3. nginx's 60 s `proxy_read_timeout` expires and it aborts the upstream read.
4. nginx RST_STREAMs the client's HTTP/2 stream with INTERNAL_ERROR. Because a `200` +
   `Content-Length` had already gone out, nix reports `HTTP error 200` + `Truncated zstd input`.

## Why it survived every previous fix

Every prior change (in-flight staging, progressive chunks, compression desync, ...) targeted **which
bytes get served**. None bounded **how long a waiter may sit silent**. The existing
`staging-contention` e2e races 8 clients and asserts byte-identical NARs -- with
`urllib.request.urlopen(..., timeout=900)`. A 900 s tolerance cannot see a 57 s stall, but a
reverse proxy tolerates 60 s. Correct-but-slow reads as PASS in the harness and as a hard
failure in production.

## Local reproduction attempt: NEGATIVE (this is the key new signal)

Stood up the prod-shaped topology locally via `dev-scripts/run.py`: **2 replicas, redis locker,
postgres, local storage, in-flight staging on, CDC off**, backends from `nix run .#deps`.

1. Sequential cross-replica race (narinfo on replica 0, NAR on replica 1), 5 packages:
   TTFB **1.3-3.3 ms** on every one.
2. Concurrent load, 14 large packages (12 KB - 349 MB), all narinfos fired at replica 0 and all
   NARs at replica 1 simultaneously: **0 / 14** with TTFB > 20 s. Worst TTFB **0.65 s**; a 349 MB
   NAR began streaming cross-replica in **0.59 s**; every body byte-exact.

So the cross-replica coordination path, in-flight streaming, the redis locker, the download lock
and the DB pool are all **exonerated** at this topology. The stall needs something prod has that
the local stack does not.

## Prime suspect: the NFS RWX shared storage

`ncps-storage` is a manually-provisioned RWX PV, `org.democratic-csi.node-manual`, `fsType: nfs`,
server `nfs.truenas.pve.nasreddine.com`, share `/mnt/tank/services/ncps`,
`mountOptions: [nfsvers=4, noatime]` -- no `actimeo`/`ac*` tuning, so kernel defaults apply
(`acdirmin/acdirmax` 30-60 s).

Both replicas mount the same share. `GetNar` opens with `statNarInStore`, which stats
`/storage/nar/<hash>.nar.{zst,xz}` -- a plain `Stat` on that NFS mount, inside the request path,
with no timeout, no instrumentation and no log line.

The measured behaviour matches an NFS attribute/dentry-cache revalidation stall exactly:

| access | TTFB |
| --- | --- |
| pod B, first read of a file pod **A** just wrote | **56.8 - 107.5 s** |
| pod B, same file again immediately after | **8.7 ms** |
| pod A (the writer), same file | **7.9 ms** |
| either pod, file written long ago | **10 - 300 ms** |

First cross-client access to a freshly-written file stalls; every subsequent access is cached and
instant. That is the signature, and it is independent of NAR size (12,524 bytes -> 56.8 s).

This also matches a hazard already recorded in the project notes: prod runs the `local` storage
backend on a shared NFS RWX volume with 2 replicas, flagged previously as an anti-pattern.

## Defects to fix

1. **ncps** -- `GetNar` performs unbounded, uninstrumented, silent blocking storage I/O inside the
   request path. There is no deadline, no first-byte budget and no log line, so a slow storage
   layer becomes a truncated HTTP response instead of a fast clean error or a fallback. This is
   the ncps-side bug and the one this change should fix.
2. **Infra** -- NFS RWX as shared storage between replicas. Either mount with sane `actimeo` /
   `lookupcache` settings, or move to the S3 backend (already supported) for multi-replica.
3. **Infra** -- `nginx.ingress.kubernetes.io/proxy-read-timeout: "300s"` is invalid (the annotation
   takes a bare integer of seconds) and is silently dropped, so the 60 s default applies. Must be
   `"300"`.

## PINNED: goroutine dump caught the stall in the act

Ran a **standalone** `ncps-pprof` Pod (same image/config/PVC/redis/DB as prod, `PPROF_ADDR=:6060`,
third node). The managed Deployment was never touched, so Argo auto-heal had nothing to revert.

Race: narinfo on the real pod A, NAR on `ncps-pprof`. `kicad` -- a **2,021 byte** NAR --
stalled **56.87 s** to first byte. Goroutine dump captured mid-stall
(`goroutine-stall-dump.txt`):

```
goroutine 366 [syscall]:
syscall.Syscall6(0x106, ...)                       <- fstatat
syscall.Stat(...)
os.Stat({0x2774bf001e0, 0x54})
github.com/kalbasit/ncps/pkg/storage/local.(*Store).StatNar   local/local.go:367
github.com/kalbasit/ncps/pkg/cache.(*Cache).statNarInStore    cache/cache.go:4954
github.com/kalbasit/ncps/pkg/cache.(*Cache).HasNarInStore     cache/cache.go:4903
github.com/kalbasit/ncps/pkg/cache.(*Cache).GetNar.func2      cache/cache.go:1307
github.com/kalbasit/ncps/pkg/cache.(*Cache).withReadLock      cache/cache.go:7231
github.com/kalbasit/ncps/pkg/cache.(*Cache).GetNar            cache/cache.go:1294
github.com/kalbasit/ncps/pkg/server....getNar                 server/server.go:926
```

**A single `os.Stat` on the NFS mount, blocked in `fstatat` for ~57 seconds.**

The pod was otherwise idle: 81 goroutines, and **exactly one** in `[syscall]` -- this one. So it
is not ncps-internal contention, not the download lock, not the DB pool, not the RW lock.

### Why ~57 s / ~107 s specifically

Mount options: `hard,timeo=600,retrans=2` -- `timeo` is in **deciseconds**, so **60 s per RPC
timeout cycle**, with up to 2 retransmissions. Observed prod stalls: 56.8, 57.4, 57.5, 57.8 and
107.5 s -- one cycle and two cycles. Exact fit.

### NFS client RPC counters (`/proc/self/mountstats`, same node)

```
       WRITE: 35 35 0 35968240 3920   508   315    825  0
      LOOKUP: 20 20 0     4744 3896     0   656    657 13
      RENAME: 10 10 0     3600 1160 41285   280  41566  9
```
(columns: ops, trans, timeouts, bytes_sent, bytes_recv, **queue_ms**, rtt_ms, execute_ms, errors)

RENAME: **41,285 ms of client-side queue time** against **280 ms of actual round-trip**. The
server is answering fast; the requests are sitting in the client's RPC queue. That is
client-side NFSv4 session-slot/backlog queuing, and the same queuing is what parks a LOOKUP /
GETATTR long enough to blow through `timeo`.

Note the isolated probe earlier (idle mount, no concurrent ncps I/O) resolved a freshly-created
file cross-node in **0 s** -- the stall only appears when the mount is under concurrent ncps load.

## Root cause

`Cache.GetNar` calls `HasNarInStore` -> `statNarInStore` -> `os.Stat` **directly in the HTTP
request path**, on an NFS mount, with:

- no timeout or deadline (the request context is not even consulted -- `os.Stat` cannot be
  cancelled),
- no instrumentation (not a single log line or span for 57 s),
- no fallback when the storage probe is slow rather than merely absent.

Under concurrent load the NFS client queues the RPC past `timeo=600` (60 s). ncps holds the HTTP
response open the whole time; nginx's 60 s `proxy_read_timeout` fires first, aborts the upstream
read and RST_STREAMs the client -- delivering a `200` with a truncated body, which nix reports as
`Truncated zstd input`.

## Fixes

1. **ncps (this change)** -- the storage-presence probe must not be able to hang a request:
   put a deadline on it, run it off the request goroutine so the context can cancel the wait,
   instrument it (log + span + metric) so a slow probe is visible instead of silent, and decide
   what to serve when the probe times out rather than blocking indefinitely. A NAR request must
   have a bounded time-to-first-byte.
2. **Infra** -- NFS RWX shared between replicas is the wrong substrate for this access pattern.
   Move multi-replica to the S3 backend (already supported), or at minimum lower `timeo`, raise
   the session slot count, and investigate why the client backlog reaches 41 s.
3. **Infra** -- `nginx.ingress.kubernetes.io/proxy-read-timeout: "300s"` is invalid (bare integer
   seconds required), silently dropped, so the 60 s default applies. Must be `"300"`.

## Test strategy

The local (non-NFS) topology cannot reproduce this -- 0/14 slow, worst TTFB 0.65 s. A test that
races replicas over local disk is green and worthless; that is precisely why the existing
`staging-contention` scenario (which tolerates `timeout=900`) never caught it.

The regression test must therefore inject a **slow storage backend** rather than rely on real
NFS: a store wrapper whose `StatNar` blocks for N seconds, asserting that `GetNar` still produces
a first byte (or a clean error) within a bounded budget well under N. That is a deterministic
unit/integration test. The e2e layer should additionally assert **time-to-first-byte**, not just
byte-correctness, so a correct-but-slow response can never pass again.

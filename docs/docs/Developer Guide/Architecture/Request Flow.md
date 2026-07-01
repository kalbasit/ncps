# Request Flow

## NarInfo Request Flow

```
Client Request: GET /<hash>.narinfo
    ↓
[1] HTTP Server receives request
    ↓
[2] Cache Layer: Check if NarInfo exists
    ↓
    ├─ Yes → [3] Serve from cache
    │         Return 200 + NarInfo
    │
    └─ No  → [4] Acquire lock (HA: Redis, Single: Local)
              ↓
             [5] Fetch from upstream caches (try in order)
              ↓
             [6] Store in database
              ↓
             [7] Sign NarInfo with secret key
              ↓
             [8] Store in storage backend
              ↓
             [9] Release lock
              ↓
             [10] Return 200 + NarInfo
```

## NAR Download Flow

```
Client Request: GET /nar/<path>
    ↓
[1] HTTP Server receives request
    ↓
[2] Cache Layer: Check if NAR exists
    ↓
    ├─ Yes → [3] Serve from cache
    │         Stream NAR to client
    │
    └─ No  → [4] Acquire download lock
              ↓
             [5] Fetch from upstream
              ↓
             [6] Store in storage backend
              ↓
             [7] Release lock
              ↓
             [8] Stream NAR to client
```

### Upstream NAR URL handling (opaque URLs)

Per the Nix HTTP binary-cache protocol, a narinfo's `URL:` field is an **opaque
path relative to the cache root** — ncps must not assume it is
`nar/<hash>.nar[.<compression>]`. ncps handles three shapes:

- **Conventional hash-named** (e.g. cache.nixos.org `nar/<hash>.nar.xz`): parsed
  directly; storage and serving key off the URL hash.
- **Opaque with a `.nar` token** (e.g. cachix `nar/<uuid>.nar.zst`): the filename
  is not a valid Nix hash, so ncps keys local storage off the narinfo `NarHash`
  and preserves the original path for the upstream `GET`.
- **Opaque without a `.nar` token** (e.g. snix-castore
  `nar/snix-castore/<blob>?narsize=N`, `Compression: none`): tolerated the same
  way, treating compression as `none`. The full opaque path **and its query
  string** are preserved for the upstream `GET` (snix returns `400` without
  `?narsize=N`).

For both opaque shapes ncps re-serves the NAR to clients under its own hash-named
`nar/<NarHash>.nar[.<compression>]` URL, and persists the opaque upstream path
(with query) so the NAR can be re-fetched from the origin after local eviction.
The upstream query is never carried onto ncps's own hash-named serve/storage key.

## High Availability Flow

With Redis distributed locking:

```
Instance A                  Instance B
    │                           │
    ├─ Request /<hash>          ├─ Request same /<hash>
    ↓                           ↓
[Acquire Redis lock]        [Try acquire lock]
    │                           │
    ↓                           ↓
Download from upstream      [Lock held by A]
    │                           │
    ↓                           ↓
Store in S3                 [Retry with backoff]
    │                           │
    ↓                           ↓
[Release lock]              [Acquire lock]
    │                           │
    ↓                           ↓
Serve to client             Check S3 (exists!)
                                │
                                ↓
                            Serve to client
```

Result: Only one download from upstream!

## LRU Cleanup Flow

```
[Scheduled Time]
    ↓
[Try acquire LRU lock]
    ↓
    ├─ Success → [Run cleanup]
    │             - Query database for old entries
    │             - Delete from storage
    │             - Update database
    │             [Release lock]
    │
    └─ Failed  → [Skip cleanup]
                  (Another instance is running it)
```

## Related Documentation

- <a class="reference-link" href="Components.md">Components</a> - System components
- <a class="reference-link" href="../../User%20Guide/Deployment/Distributed%20Locking.md">Distributed Locking</a> - Lock details
- <a class="reference-link" href="../../User%20Guide/Deployment/High%20Availability.md">High Availability</a> - HA deployment

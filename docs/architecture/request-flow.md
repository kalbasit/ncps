[Home](../../README.md) > [Documentation](../README.md) > [Architecture](README.md) > Request Flow

# Request Flow

Detailed request processing flow.

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

- [Components](components.md) - System components
- [Distributed Locking](../deployment/distributed-locking.md) - Lock details
- [High Availability](../deployment/high-availability.md) - HA deployment

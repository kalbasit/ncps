# Pinning

## Closure Pinning

Closure pinning lets you protect specific store paths — and everything they depend on — from being removed by `ncps`'s automatic cache cleanup.

## Overview

By default, `ncps` keeps the cache within `--cache-max-size` by periodically evicting the **L**east **R**ecently **U**sed (LRU) entries. This is the right behavior for most paths, but sometimes you want a path to stay in the cache regardless of how recently it was accessed — for example:

- The toolchain or base closure for your CI, so builds never re-download it.
- A specific release you want available offline.
- A large dependency closure that is expensive to re-fetch from upstream.

Pinning a closure marks a narinfo **and all of its transitive references** as protected. While pinned, none of those paths are eligible for LRU eviction. When you no longer need the guarantee, you unpin the closure and the paths become normal eviction candidates again.

> [!NOTE]
> Pinning protects the **entire closure**, not just the single store path you name. When you pin a path, `ncps` walks its references and protects every NAR reachable from it. If some references are not yet in the local cache, `ncps` attempts to fetch them from the configured upstream caches at pin time so the whole closure is present and protected.

## API

Pinning is controlled through three HTTP endpoints. The `{hash}` segment is the store-path hash portion of the narinfo — the same hash used in the `{hash}.narinfo` URL (e.g. `2imigbs1vnh9bdyf42z9mvq23pdshgw4`).

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/pin/{hash}.narinfo` | Pin the closure rooted at `{hash}`. |
| `DELETE` | `/pin/{hash}.narinfo` | Unpin the closure rooted at `{hash}`. |
| `GET` | `/pins` | List all pinned closure hashes. |

### Pin a closure

```sh
curl -X POST http://your-ncps-hostname:8501/pin/2imigbs1vnh9bdyf42z9mvq23pdshgw4.narinfo
```

**Responses:**

- `200 OK` — the closure is pinned.
- `404 Not Found` — no narinfo with that hash exists in the cache, so there is nothing to pin. Fetch or upload the path first, then pin it.

Pinning is **idempotent**: pinning an already-pinned closure simply returns `200 OK` and changes nothing.

### Unpin a closure

```sh
curl -X DELETE http://your-ncps-hostname:8501/pin/2imigbs1vnh9bdyf42z9mvq23pdshgw4.narinfo
```

**Responses:**

- `200 OK` — the closure is no longer pinned. After unpinning, the narinfo and its references become normal LRU-eviction candidates again.

Unpinning is also **idempotent**: unpinning a closure that is not pinned returns `200 OK`.

### List pinned closures

```sh
curl http://your-ncps-hostname:8501/pins
```

Returns a JSON array of the pinned root hashes with `Content-Type: application/json`:

```json
["2imigbs1vnh9bdyf42z9mvq23pdshgw4", "1q8w7e6r5t4y3u2i1o0p9a8s7d6f5g4h"]
```

When nothing is pinned, the response is an empty array:

```json
[]
```

> [!NOTE]
> The list contains only the **roots** you pinned, not their expanded transitive references. The full set of protected paths is computed from these roots at eviction time.

## Notes and Limitations

- Pins have **no expiry or TTL**. A pinned closure stays protected until you explicitly unpin it.
- Pinning protects paths from LRU eviction only. It does not affect `--cache-max-size` accounting — pinned paths still count toward the total cache size, so pinning more than your size budget can prevent cleanup from reaching the target.
- The hash you pin must already exist as a narinfo in the cache; pinning does not by itself import a brand-new path that `ncps` has never seen.

## Related Documentation

- <a class="reference-link" href="../Usage/Cache%20Management.md">Cache Management</a> — size limits and LRU cleanup
- <a class="reference-link" href="../Getting%20Started/Concepts.md">Concepts</a> — how caching and eviction work
- <a class="reference-link" href="../Configuration/Reference.md">Reference</a> — all configuration options

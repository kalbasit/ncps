## Why

Ncps currently uses LRU (Least Recently Used) eviction to manage cache space. However, users need the ability to protect critical closures (a narinfo + all its references) from being evicted, ensuring they remain available even under space pressure. This is essential for maintaining pinned versions, critical build dependencies, or air-gapped environments where re-fetching from upstream is not possible.

## What Changes

- Add a new `pinned_closures` table to track pinned narinfo hashes
- Implement three new HTTP endpoints:
  - `POST /pin/{hash}.narinfo` - Pin a closure (the narinfo and all its transitive references)
  - `DELETE /pin/{hash}.narinfo` - Unpin a closure
  - `GET /pins` - List all pinned closures
- Modify LRU eviction queries to exclude pinned narinfos from deletion candidates
- Add database queries to support pinning operations:
  - Insert pinned closure
  - Delete pinned closure
  - List all pinned closures
  - Check if a narinfo is pinned

## Capabilities

### New Capabilities
- `closure-pinning`: API and database support for pinning/unpinping closures and protecting them from LRU eviction

### Modified Capabilities
- None - LRU eviction logic will be enhanced to skip pinned items, but this is an implementation detail, not a requirement change

## Impact

- **Database**: New `pinned_closures` table; new queries for CRUD operations
- **API**: Three new HTTP endpoints under `/pin` prefix
- **Cache layer**: Modify `GetLeastUsedNarInfos` to exclude pinned narinfos
- **Storage**: None
- **Performance**: Minimal impact - pinned check is a simple join or IN query during LRU eviction

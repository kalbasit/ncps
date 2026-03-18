## 1. Database Schema

- [x] 1.1 Create dbmate migration for `pinned_closures` table (all 3 engines: sqlite, postgres, mysql)
- [x] 1.2 Add SQL queries for pinned_closures CRUD operations (db/query.*.sql)
- [x] 1.3 Run `sqlc generate` to generate Go code
- [x] 1.4 Run `go generate ./pkg/database` to update wrappers

## 2. Database Interface Methods

- [x] 2.1 Add `CreatePinnedClosure(ctx, hash)` method to Querier interface
- [x] 2.2 Add `DeletePinnedClosure(ctx, hash)` method to Querier interface
- [x] 2.3 Add `ListPinnedClosures(ctx)` method to Querier interface
- [x] 2.4 Add `GetPinnedClosure(ctx, hash)` method to Querier interface

## 3. Cache Layer - Pinning Operations

- [x] 3.1 Add `PinClosure(ctx, hash)` method to Cache
- [x] 3.2 Add `UnpinClosure(ctx, hash)` method to Cache
- [x] 3.3 Add `ListPinnedClosures(ctx)` method to Cache
- [x] 3.4 Add `IsNarInfoPinned(ctx, hash)` method to Cache

## 4. Cache Layer - LRU Integration

- [x] 4.1 Implement `GetClosureHashes(ctx, rootHashes []string) (map[string]struct{}, error)` to compute transitive closure by:
  - Starting with pinned root hashes
  - For each narinfo, look up its references in `narinfo_references`
  - Extract hash prefix (first 52 chars) from each reference (format: `<hash>-<name>-<version>`)
  - Join with `narinfos` table to resolve references to narinfo IDs
- [x] 4.2 Modify `GetLeastUsedNarInfos` query to exclude pinned closures and their transitive references

## 5. HTTP API Endpoints

- [x] 5.1 Add `POST /pin/{hash}.narinfo` handler
- [x] 5.2 Add `DELETE /pin/{hash}.narinfo` handler
- [x] 5.3 Add `GET /pins` handler

## 6. Tests

- [x] 6.1 Write unit tests for pinning/unpinning operations
- [x] 6.2 Write unit tests for closure computation
- [x] 6.3 Write integration tests for HTTP endpoints
- [x] 6.4 Write test to verify LRU eviction skips pinned closures

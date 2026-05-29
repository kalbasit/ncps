## 1. Baseline & site inventory

- [x] 1.1 Run the `pkg/cache` suite to confirm green before changing anything.
- [x] 1.2 Enumerate the exact verbatim sites for each pattern (NarInfo `.Where(HashEQ).Only`, Chunk `.Where(HashIn).All`, NarFile aggregate-sum + unpack), confirming each candidate is byte-identical to the planned helper body and excluding eager-loaded / multi-predicate variants. (`.First`-based and `.Count`-based NarInfo queries left untouched.)

## 2. Add helpers

- [x] 2.1 Created `pkg/cache/queries.go` with `narInfoByHash(ctx, q *ent.NarInfoClient, hash string) (*ent.NarInfo, error)`.
- [x] 2.2 Added `chunksByHashes(ctx, q *ent.ChunkClient, hashes []string) ([]*ent.Chunk, error)`.
- [x] 2.3 Added `totalNarFileSize(ctx, q *ent.NarFileClient) (int64, error)` returning the summed file_size (0 when empty/NULL), no logging.

## 3. Replace call sites

- [x] 3.1 Replaced the four verbatim `narInfoByHash` `.Only` sites (three `tx.NarInfo`, one `c.dbClient.Ent().NarInfo`).
- [x] 3.2 Replaced the two chunk `HashIn(...).All` sites with `chunksByHashes`.
- [x] 3.3 Replaced the two aggregate-sum blocks with `totalNarFileSize`, preserving each caller's policy (metrics: warn-and-continue, keeping totalSize in scope for the utilization ratio; cleanup: return-on-error). Dropped the now-unused `database/sql` import from cache.go.

## 4. Verify

- [x] 4.1 Ran `task fmt` and `task lint`; gci auto-fixed import grouping in queries.go; added two justified `//nolint:gosec` comments on `uint64(narTotalSize)` conversions (file_size is a uint64 column, so the SUM is non-negative — the conversion gosec newly flagged after provenance changed is provably safe). 0 issues.
- [x] 4.2 Ran `task test` — full suite green, including `pkg/cache` (30s).
- [x] 4.3 No new test added: existing cache tests already cover get/insert-by-hash, chunk batch reads, and the cleanup/metrics size paths. Behavior is unchanged.

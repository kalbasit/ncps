## Context

ncps currently uses LRU (Least Recently Used) eviction to manage cache space. When the cache exceeds its configured maximum size, the system queries `GetLeastUsedNarInfos` to find the oldest accessed narinfos and evicts them along with their associated NAR files. This design document outlines how to add closure pinning capabilities.

## Goals / Non-Goals

**Goals:**
- Allow users to pin a narinfo hash (closure root) which protects it AND all its transitive references from LRU eviction
- Provide API endpoints to pin, unpin, and list pinned closures
- Ensure pinned closures persist across restarts (stored in database)
- Minimize performance impact on LRU eviction

**Non-Goals:**
- Pin individual NAR files without their narinfo metadata
- Automatic pinning based on usage patterns
- Pin expiry/TTL (manual unpin required)
- Distributed locking for multi-instance deployments (single-instance only)

## Decisions

### 1. Store pinned closures at root hash level only

**Decision:** When a user pins a closure by hash (e.g., `abc123`), we store only that root hash in `pinned_closures`. The transitive closure (all references) is computed dynamically during LRU eviction.

**Rationale:** Storing every transitive reference would lead to data duplication and consistency issues. If a new reference is added to a narinfo, we'd need to update all pinned closures that include it.

**Alternative considered:** Store all transitive references at pin time. Rejected because it creates consistency drift and increases write amplification.

### 2. Compute transitive closure at LRU eviction time

**Decision:** During LRU eviction, we compute the full set of protected hashes by traversing from each pinned root through `narinfo_references`.

**Critical detail - Reference format:** The `narinfo_references.reference` column stores the full store path base name (e.g., `2imigbs1vnh9bdyf42z9mvq23pdshgw4-nghttp2-1.67.1-dev`), not just the hash. To traverse references, we must:
1. Extract the hash prefix (first 52 characters, Nix base32) from each reference
2. Look up the corresponding narinfo by that hash
3. Continue traversing from those narinfos' references

This requires a JOIN between `narinfo_references` and `narinfos` tables to resolve references to their narinfo IDs.

**Rationale:** This ensures the protection is always current - any new references added to a pinned narinfo are automatically protected.

**Performance consideration:** For caches with many pinned closures and deep dependency graphs, this could be slow. We'll optimize by:
- Caching the computed closure during a single eviction run
- Using a visited set to avoid infinite loops (cycles shouldn't exist in valid Nix graphs, but we protect against malformed data)

### 3. Use database join to exclude pinned narinfos

**Decision:** Modify `GetLeastUsedNarInfos` to use a LEFT JOIN or NOT IN subquery against `pinned_closures`.

**Rationale:** This is a simple SQL change that leverages the database's query optimizer. For SQLite, NOT IN with a small subquery performs well.

### 4. API endpoint design follows existing patterns

**Decision:** Use the same URL pattern as other narinfo endpoints (`/{hash}.narinfo`) under a `/pin` prefix.

**Rationale:** Consistent with existing ncps API patterns. The `.narinfo` suffix validates the hash format.

## Risks / Trade-offs

**[Risk] Large closure graphs slow down LRU eviction**

→ **Mitigation:** Add a maximum traversal depth (default: 1000). Log warning if depth exceeded. This prevents runaway queries on malformed data.

**[Risk] Pinned closures accumulate over time without cleanup**

→ **Mitigation:** Provide no automatic cleanup. Users must explicitly unpin. Consider adding a `/pins` endpoint that returns narinfo metadata including reference counts to help users audit pins.

**[Risk] Database migration required**

→ **Mitigation:** Use dbmate for schema migration. The change is additive (new table only), making rollback straightforward.

**[Risk] Concurrent pin/unpin operations**

→ **Mitigation:** Database constraints (UNIQUE on hash) handle duplicate pins gracefully. Use standard transaction patterns for delete operations.

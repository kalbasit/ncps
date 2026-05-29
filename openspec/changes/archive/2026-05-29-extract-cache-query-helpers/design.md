## Context

`pkg/cache/cache.go` inlines identical Ent queries across many call sites. Three patterns are genuinely verbatim and safe to extract:

1. **NarInfo by hash** — `q.NarInfo.Query().Where(entnarinfo.HashEQ(hash)).Only(ctx)` at the bare sites (e.g. lines ~3976, ~4496, ~4514, ~4804). Note: several *other* NarInfo queries chain `.With...()` eager-loads or extra `Where` predicates — those are **not** the same and are out of scope.
2. **Chunks by hashes** — `q.Chunk.Query().Where(entchunk.HashIn(hashes...)).All(ctx)` (lines ~2483, ~2532).
3. **Total NAR-file size** — `var x []struct{ Sum sql.NullInt64 }` / `q.NarFile.Query().Aggregate(ent.Sum(entnarfile.FieldFileSize)).Scan(ctx, &x)` / `len(x)>0 && x[0].Sum.Valid` unpack (lines ~724 and ~5748). The two copies differ only in their surrounding error policy (one warns and continues for a metric, the other returns the error for cleanup).

Both `*ent.Client` and `*ent.Tx` expose per-entity clients of the same type (`*ent.NarInfoClient`, `*ent.NarFileClient`, `*ent.ChunkClient`). So a helper that takes the entity client works unchanged for transactional and non-transactional callers.

## Goals / Non-Goals

**Goals:**
- Remove verbatim duplication for the three patterns above.
- One helper serves both `tx.X` and `c.dbClient.Ent().X` callers.
- Strictly behavior-preserving: same SQL, same `Only`/`All`/`Scan` semantics, same returned error values.

**Non-Goals:**
- Touching eager-loaded or multi-predicate query variants.
- Changing any caller's error-handling policy (the total-size callers keep their own warn-vs-return logic).
- Relocating helpers to `database.Client`.

## Decisions

**Decision 1 — Helpers take the entity client, not `*ent.Client`/`*ent.Tx`.**
Signature shape: `narInfoByHash(ctx context.Context, q *ent.NarInfoClient, hash string) (*ent.NarInfo, error)`. Callers pass `tx.NarInfo` or `c.dbClient.Ent().NarInfo`. This is the narrowest abstraction that unifies both call modes without an interface. Alternative (a `querier` interface) rejected as overkill — Ent already gives a shared concrete type.

**Decision 2 — `totalNarFileSize` returns `(int64, error)`, not the metric/cleanup-specific shaping.**
The helper owns the duplicated core: declare the `[]struct{ Sum sql.NullInt64 }`, run the aggregate scan, return the extracted `int64` (0 when no rows / NULL). Each caller keeps its existing logging/early-return behavior around the call. This preserves both call sites' distinct policies while removing the copy-pasted query+unpack.

**Decision 3 — New file `pkg/cache/queries.go`.**
Keep the helpers together and avoid growing `cache.go`. Same package, so no export needed and call sites change minimally.

## Risks / Trade-offs

- [Accidentally folding a non-identical query (eager-load/extra-predicate) into a helper changes behavior] → Only replace sites whose text is byte-identical to the helper body; leave the rest. Verified site list is in the design context above.
- [`totalNarFileSize` subtly changing the zero/NULL handling] → Mirror the exact `len(x)>0 && x[0].Sum.Valid` guard; cover with the existing cleanup/metrics tests which already assert sizes.

## Migration Plan

Pure code refactor, single stacked PR, straight revert to roll back. No DB or runtime state.

## Open Questions

- None. The helper set is fixed; if a fourth verbatim pattern surfaces during implementation it can be added under the same contract.

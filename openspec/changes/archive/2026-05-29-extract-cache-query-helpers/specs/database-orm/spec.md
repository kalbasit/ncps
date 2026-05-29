## ADDED Requirements

### Requirement: Common by-hash and aggregate reads go through shared cache query helpers

The cache layer SHALL obtain the common single-entity-by-hash lookups and the total-NAR-file-size aggregate through shared `pkg/cache` query helpers rather than re-inlining the equivalent Ent query at each call site. Each helper SHALL accept the Ent entity client (e.g. `*ent.NarInfoClient`) so the same helper serves both `*ent.Client` and `*ent.Tx` callers, and SHALL preserve the underlying Ent semantics exactly.

#### Scenario: NarInfo-by-hash helper returns the row or an Ent not-found error

- **WHEN** the NarInfo-by-hash helper is called with a hash that exists
- **THEN** it SHALL return the matching `*ent.NarInfo`
- **AND WHEN** called with a hash that does not exist
- **THEN** it SHALL return an error for which `ent.IsNotFound` reports true — identical to the inline `Query().Where(HashEQ).Only(ctx)` it replaces

#### Scenario: Total-NAR-file-size helper sums file_size and is zero-safe

- **WHEN** the total-size helper is called and `nar_files` contains rows
- **THEN** it SHALL return the sum of `file_size` across all rows as an `int64`
- **AND WHEN** there are no rows (or the sum is SQL NULL)
- **THEN** it SHALL return `0` without error

#### Scenario: Same helper serves transactional and non-transactional callers

- **WHEN** a helper is invoked with `tx.<Entity>` inside a transaction and elsewhere with `c.dbClient.Ent().<Entity>` outside one
- **THEN** both invocations SHALL execute the same query against their respective connection/transaction
- **AND** no call site SHALL re-declare the extracted query inline

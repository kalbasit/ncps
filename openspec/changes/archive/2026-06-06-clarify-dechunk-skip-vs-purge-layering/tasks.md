## 1. Confirm the authoritative behavior

- [x] 1.1 Re-confirm the single operation `Cache.MigrateChunksToNar` returns `ErrNoNarHashToVerify` and leaves the NAR chunked (no delete/truncate) when no NarHash resolves (`pkg/cache/cache.go:8398-8408`)
- [x] 1.2 Re-confirm the batch pass purges on `err != nil` (incl. `ErrNoNarHashToVerify`) and leaves the NAR chunked on context cancellation (`pkg/ncps/migrate_chunks_to_nar.go:419-462`)

## 2. Reconcile the spec wording

- [x] 2.1 Update "De-chunk MUST resolve the verification NarHash via the narinfo URL when no join link exists" to scope skip to the single operation (signals `ErrNoNarHashToVerify`, leaves chunked) and cross-reference the pass for purging
- [x] 2.2 Update "The de-chunk pass MUST always drive the chunked count to zero" to state it purges what the single operation declined to de-chunk, and that context-cancellation leaves the NAR chunked
- [x] 2.3 Reword the "…is skipped" scenario into "the single operation leaves an unverifiable NAR chunked (the pass purges it)" so it no longer contradicts the purge requirement

## 3. Verify

- [x] 3.1 `openspec validate clarify-dechunk-skip-vs-purge-layering` passes
- [x] 3.2 No code change required (confirm the implementation already matches the documented layered policy); if a mismatch is found, raise it rather than silently editing code

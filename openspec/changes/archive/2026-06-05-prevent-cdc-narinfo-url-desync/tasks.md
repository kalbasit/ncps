## 1. Prevention (cdc-chunking)

- [x] 1.1 Remove the eager-CDC trigger from the `pullNarInfo` store-time normalization (condition becomes `if narInfo.Compression == none`)
- [x] 1.2 Test: an eager-CDC xz pull persists the truthful xz URL (`TestPullNarInfo_EagerCDC_DoesNotPrematurelyNormalizeXzURL`)
- [x] 1.3 Update the two CDC contract tests that asserted store-time `none` to assert the narinfo↔storage consistency invariant
- [x] 1.4 Update the phantom-recovery test to address the NAR via the URL the narinfo now advertises

## 2. Data repair

- [x] 2.1 Generate forward-only per-dialect migration `repair_url_none_xz_narinfos` (sqlite/postgres/mysql); rehash atlas.sum
- [x] 2.2 Reconstruct url/compression/file_hash/file_size from the joined nar_file; exclude narinfos with a servable none/chunked backing (`NOT EXISTS`)
- [x] 2.3 Validate idempotency against a live CDC database (UPDATE N then re-run affects 0)

## 3. Verification

- [x] 3.1 `task fmt`, `task lint`, `task test` pass
- [x] 3.2 Apply the migration to production and confirm the stranded narinfos serve again

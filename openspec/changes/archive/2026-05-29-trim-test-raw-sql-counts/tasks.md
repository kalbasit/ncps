## 1. Inventory

- [x] 1.1 Confirmed `pkg/cache` and `pkg/server` suites green before changes.
- [x] 1.2 Listed the `SELECT COUNT(*)` `DB()` sites: bare table counts in `cache_test.go` (5) and by-hash counts in `server_test.go` (5, all standalone). Found additional by-hash counts in `cache_test.go` interleaved with raw-SQL flows (rebind/ExecContext/Eventually/wg) — these are retained.

## 2. Convert cache_test.go

- [x] 2.1 Replaced the 5 bare-table `dbClient.DB().QueryRowContext(..., "SELECT COUNT(*) FROM <table>").Scan(&count)` assertions with `count, err := dbClient.Ent().<Entity>.Query().Count(context.Background())` (NarInfo / NarFile), preserving the surrounding `require.NoError` / `assert.Equal`.

## 3. Convert server_test.go

- [x] 3.1 Replaced all 5 `SELECT COUNT(*) FROM narinfos WHERE hash = ?` assertions with `dbClient.Ent().NarInfo.Query().Where(entnarinfo.HashEQ(testdata.Nar1.NarInfoHash)).Count(newContext())`, adding the `entnarinfo` import.

## 4. Verify

- [x] 4.1 Ran `task fmt` and `task lint` (gci auto-fixed the new import grouping) — 0 issues.
- [x] 4.2 Ran `task test` — full suite green (0 failures); `pkg/cache` (30s) and `pkg/server` pass with identical assertions.
- [x] 4.3 Confirmed retained raw-SQL categories are untouched (migration verification, adversarial setup, timestamp inspection, pool tuning, schema probes, testhelper admin, and the rebind/Eventually/wg-interleaved cache_test counts), and the only production `DB()` use (`pkg/ncps/migrate.go`) is unchanged.

## 1. Schema fix

- [x] 1.1 In `ent/schema/narinfo.go`, change `last_accessed_at`'s annotation from `entsql.Annotation{DefaultExpr: "CURRENT_TIMESTAMP"}` to `entsql.Default("CURRENT_TIMESTAMP")`.
- [x] 1.2 In `ent/schema/nar_file.go`, apply the same change to `last_accessed_at`.
- [x] 1.3 Run `task ent:generate` and confirm `git diff ent/migrate/schema.go` shows only the two `last_accessed_at` entries flipping from `Default: schema.Expr("CURRENT_TIMESTAMP")` to `Default: "CURRENT_TIMESTAMP"`.

## 2. Verify no spurious diff (all dialects)

- [x] 2.1 With dev DBs running, run `task migrations:gen NAME=verify_no_phantom_diff` and confirm **no** new `.sql` file is produced under `migrations/sqlite/`, `migrations/postgres/`, or `migrations/mysql/`. _Done: ran against live deps (PG+MySQL on random ports) into a temp root — 43→43 files, zero new for all three dialects._
- [x] 2.2 If any tooling created a probe/`atlas.sum` change, revert it; re-run to confirm the diff is clean and the working tree shows no migration files. _N/A: ran in a temp root; repo `migrations/` untouched._
- [x] 2.3 Add/confirm a generator-level test (extending `cmd/generate-migrations/main_test.go`) asserting the SQLite diff against the committed schema is empty — i.e. no `ModifyTable` for `narinfos`/`nar_files` when no field changed. Write it test-first (red) before relying on the fix (green). _Done: `TestSQLiteNoPhantomDiff` (RED 18→19 before fix, GREEN after)._

## 3. Regression guard (A6 lint)

- [x] 3.1 Write a failing `cmd/ent-lint` test (red): a schema fixture using `entsql.Annotation{DefaultExpr: "CURRENT_TIMESTAMP"}` MUST produce a `[FAIL] A6` line and non-zero exit. _Done: `testdata/a6_bad` + case (RED: exited 0 before impl)._
- [x] 3.2 Implement the A6 AST check in `cmd/ent-lint`: flag any field whose annotation is `entsql.Annotation{DefaultExpr: "CURRENT_TIMESTAMP"}`, emit a checklist line naming file + field and recommending `entsql.Default("CURRENT_TIMESTAMP")`. _Done: `checkA6`._
- [x] 3.3 Add a passing-case test: the fixed `ent/schema/*.go` tree emits `[PASS] A6` and the check exits zero. _Done: `testdata/a6_good` + `TestEntLintRealSchemas` passes._
- [x] 3.4 Confirm A6 runs inside `nix flake check` via the existing `ent-lint-check` derivation (extend its output set/identifiers as needed). _Confirmed: derivation runs `ent-lint --root .` and greps `[FAIL]`; A6 auto-covered, no change needed._

## 4. Documentation

- [x] 4.1 Update `.claude/rules/ent-migrations.md` (or `CLAUDE.md`) to record the convention: DB `CURRENT_TIMESTAMP` defaults MUST use `entsql.Default("CURRENT_TIMESTAMP")`, never `DefaultExpr`, citing issue #1328. _Done: added invariant §6._

## 5. Verification

- [x] 5.1 Run `task ent:check` (generate + lint + drift) and confirm it exits zero. _Done: exit 0; all A6 lines PASS; ent drift clean._
- [x] 5.2 Run `task fmt`, `task lint`, and `task test`; confirm each exits zero. _Done: fmt OK; lint 0 issues; test exit 0 (29 pkgs, 0 fail)._
- [x] 5.3 Confirm `git status` shows no new or modified files under `migrations/` (proving the fix needs no migration). _Done: 0 changed files under migrations/._

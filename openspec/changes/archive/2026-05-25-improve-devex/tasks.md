## 1. Taskfile — Core Tasks

- [x] 1.1 Create `Taskfile.yml` at repo root with `set: [errexit, nounset, pipefail]` and `default` task that runs `task --list`
- [x] 1.2 Add `fmt` task: `nix fmt`
- [x] 1.3 Add `lint` task: `golangci-lint run`
- [x] 1.4 Add `lint:fix` task: `golangci-lint run --fix`
- [x] 1.5 Add `test` task: `go test -race ./...` (no backend env vars)
- [x] 1.6 Add `build` task: `go build .`
- [x] 1.7 Add `dev` task: `./dev-scripts/run.py`
- [x] 1.8 Add `deps` task: `nix run .#deps`
- [x] 1.9 Verify `task fmt`, `task lint`, and `task test` all exit 0 on a clean checkout

## 2. Taskfile — Ent and Migration Tasks

- [x] 2.1 Add `ent:generate` task: `go generate ./ent/...`
- [x] 2.2 Add `ent:lint` task: `go run ./cmd/ent-lint --root .`
- [x] 2.3 Add `ent:check` task with `deps: [ent:generate, ent:lint]`
- [x] 2.4 Add `migrations:gen` task: `go run ./cmd/generate-migrations --name={{.NAME}}` (requires `NAME` var)
- [x] 2.5 Add `migrations:sql` task: `go run ./cmd/generate-migrations --sql-only --name={{.NAME}}` (requires `NAME` var)

## 3. Integration Test Tasks

- [x] 3.1 Add `test:integration` task that exports all integration env vars and runs `go test -race ./...` (assumes deps already running)
- [x] 3.2 Create `dev-scripts/test-auto.sh`:
  - Check each backend port (9000/S3, 5432/PG, 3306/MySQL, 6379/Redis) via `nc -z`
  - If any are unreachable, start `nix run .#deps` in the background; capture PID
  - Wait for all four ports with 60s timeout (poll every 2s)
  - Run `eval "$(enable-integration-tests)"` and then `go test -race ./...`
  - On exit (via `trap`), kill the background process if the script started it
  - Propagate the test exit code
- [x] 3.3 Add `test:auto` task: `bash dev-scripts/test-auto.sh`
- [x] 3.4 Make `dev-scripts/test-auto.sh` executable (`chmod +x`)
- [x] 3.5 Verify `task test:auto` starts deps, runs tests, and tears down correctly (manual smoke test)

## 4. CLAUDE.md Cleanup

- [x] 4.1 Rewrite `CLAUDE.md` keeping only: project overview (3 lines), prerequisites, `task` command table, `nix build` / `nix flake check` one-liners, architecture package-structure list, key interfaces summary, database engine summary, and pointers to `.claude/rules/` and `.agent/skills/`
- [x] 4.2 Remove the entire "NarInfo Migration Strategy" section (~200 lines)
- [x] 4.3 Remove the "Development Workflow" section detail (storage backend descriptions, process-compose detail)
- [x] 4.4 Remove the "Code Quality" linting/formatting detail (now in lint skill and rules)
- [x] 4.5 Remove the "Testing" integration test detail (now in tdd skill and rules)
- [x] 4.6 Trim the "Helm Chart Testing" section to a 3-line pointer
- [x] 4.7 Trim the "Kind Integration Tests" section to a 3-line pointer
- [x] 4.8 Verify `wc -l CLAUDE.md` reports ≤ 150 lines (109 lines)
- [x] 4.9 Verify all `task` commands referenced in `CLAUDE.md` exist in `Taskfile.yml`

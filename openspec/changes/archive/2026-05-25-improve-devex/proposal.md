# Proposal: improve-devex

## Why

`ncps` has no `Taskfile.yml`, yet `CLAUDE.md` and the `verify-before-completion` rule both reference `task fmt`, `task lint`, and `task test` — meaning those commands silently fail for every contributor. Alongside this, three agent skills were recently deleted (build, ncps, test) and a comprehensive `tdd` skill plus eight `.claude/rules/` files were added, but the oversized `CLAUDE.md` still duplicates content that now lives in rules and skills.

## What Changes

- **Add `Taskfile.yml`** with standard tasks: `default` (lists tasks), `fmt` (`nix fmt`), `lint` (`golangci-lint run`), `ent:generate`, `ent:check`, `test` (unit), `test:integration` (all backends), `test:auto` (auto-start deps + enable all backends, run full suite, teardown)
- **`test:auto` task**: starts `nix run .#deps` in the background, waits for readiness, exports integration env vars, runs `go test -race ./...`, tears down — zero-friction local integration testing
- **Slim down `CLAUDE.md`**: Remove sections now fully covered by `.claude/rules/` and `.agent/skills/` — keep only: project overview, prerequisites, storage/deps quick-start, architecture summary, and pointers to rules/skills
- **Validate `.claude/rules/`**: Confirm the 8 new rules (tdd-required, verify-before-completion, no-commits-to-main, no-skip-git-hooks, no-panic-outside-main, nolint-with-comments, env-execution, ent-migrations) are correct and sufficient; remove anything now redundant from CLAUDE.md

## Capabilities

### New Capabilities

- `task-workflow`: Standardized developer task runner via `Taskfile.yml` — `fmt`, `lint`, `test`, `test:integration`, `test:auto`, `ent:generate`, `ent:check`

### Modified Capabilities

_(none — no existing spec-level behavior changes)_

## Non-goals

- No changes to Go source code, tests, or production logic
- No changes to CI workflows (GitHub Actions already calls `nix flake check` directly)
- No changes to Nix flake or dev shell tooling
- No new test cases — the `task test:auto` task only orchestrates existing tests
- Not extracting all CLAUDE.md content into skills — only removing what's now duplicated

## Impact

- `Taskfile.yml` — new file at repo root
- `CLAUDE.md` — reduced from ~500 lines to ~150; content not deleted, only moved to already-existing rules/skills
- `.claude/rules/` — no changes beyond confirming correctness
- `.agent/skills/` — no changes (tdd skill and remaining skills stay as-is)
- No runtime, API, or dependency changes

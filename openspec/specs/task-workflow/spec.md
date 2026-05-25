# Spec: task-workflow

## Purpose

Define the standard developer task interface exposed via `Taskfile.yml` at the
repository root. All common operations (formatting, linting, testing, Ent code
generation, and migration generation) must be accessible through `task`
sub-commands so developers and CI have a single, discoverable entry point.

---

## Requirements

### Requirement: Taskfile exists at repo root
A `Taskfile.yml` must exist at the repository root. Running `task` with no arguments must print a list of available tasks.

#### Scenario: Default task lists available tasks
- **WHEN** a developer runs `task` (or `task default`) in the repo root
- **THEN** a formatted list of available tasks and their descriptions is printed

---

### Requirement: `task fmt` formats all project files
Running `task fmt` must format all project files by delegating to `nix fmt`.

#### Scenario: Formatting succeeds
- **WHEN** a developer runs `task fmt`
- **THEN** `nix fmt` runs and exits 0

#### Scenario: Formatting is idempotent
- **WHEN** `task fmt` is run on an already-formatted codebase
- **THEN** no files are modified and the command exits 0

---

### Requirement: `task lint` lints Go code
Running `task lint` must run `golangci-lint run` and exit non-zero if any lint issues are found.

#### Scenario: Clean code passes lint
- **WHEN** a developer runs `task lint` on code with no lint issues
- **THEN** the command exits 0

#### Scenario: Code with issues fails lint
- **WHEN** a developer runs `task lint` on code with lint violations
- **THEN** the command exits non-zero and prints the violations

---

### Requirement: `task lint:fix` auto-fixes lint issues
Running `task lint:fix` must run `golangci-lint run --fix` to automatically apply fixable lint corrections.

#### Scenario: Auto-fixable issues are resolved
- **WHEN** a developer runs `task lint:fix` with auto-fixable lint issues present
- **THEN** fixable issues are corrected in-place and the command exits 0

---

### Requirement: `task test` runs unit tests without backend services
Running `task test` must run `go test -race ./...` without requiring any external services (no S3/PostgreSQL/MySQL/Redis).

#### Scenario: Unit tests pass
- **WHEN** a developer runs `task test` with no backend services running
- **THEN** `go test -race ./...` runs and all tests that do not require backends pass

#### Scenario: Integration tests are skipped (not failed)
- **WHEN** a developer runs `task test` without integration env vars set
- **THEN** integration-gated subtests are skipped (via `t.Skip`), not failed

---

### Requirement: `task test:integration` runs full test suite with deps pre-started
Running `task test:integration` must enable all integration env vars and run the full test suite, assuming backing services are already running.

#### Scenario: Full suite runs when services are available
- **WHEN** a developer runs `task test:integration` with `nix run .#deps` already running
- **THEN** all integration env vars are exported and `go test -race ./...` runs the full suite

#### Scenario: Fails clearly when services are not running
- **WHEN** a developer runs `task test:integration` with no backend services running
- **THEN** the test suite fails with connection errors (not silently skipped)

---

### Requirement: `task test:auto` auto-starts and tears down backend services
Running `task test:auto` must start backend services if they are not already running, run the full integration suite, then shut down any services it started.

#### Scenario: Services not running — auto-start, test, teardown
- **WHEN** a developer runs `task test:auto` and none of the backend ports (9000, 5432, 3306, 6379) are reachable
- **THEN** `nix run .#deps` is started in the background, the script waits for all four ports to be ready, the full test suite runs, and the background process is terminated on exit

#### Scenario: Services already running — skip start, test, no teardown
- **WHEN** a developer runs `task test:auto` and all backend ports are already reachable
- **THEN** the script skips starting deps, runs the full test suite, and does not terminate the already-running services

#### Scenario: Exit code propagated
- **WHEN** the test suite exits with a non-zero code (test failure)
- **THEN** `task test:auto` exits with the same non-zero code

---

### Requirement: `task ent:generate` regenerates the Ent client
Running `task ent:generate` must run `go generate ./ent/...` to regenerate the Ent client from schemas.

#### Scenario: Generate succeeds
- **WHEN** a developer runs `task ent:generate` after editing an Ent schema
- **THEN** `go generate ./ent/...` runs and exits 0

---

### Requirement: `task ent:lint` lints Ent schemas
Running `task ent:lint` must run `go run ./cmd/ent-lint --root .` to enforce the five codegen invariants.

#### Scenario: Schemas with no invariant violations pass
- **WHEN** `task ent:lint` is run on valid schemas
- **THEN** the linter exits 0

---

### Requirement: `task ent:check` validates Ent is up to date
Running `task ent:check` must regenerate the Ent client and then lint it, failing if schemas are out of sync or violate invariants.

#### Scenario: Up-to-date clean schemas pass
- **WHEN** `task ent:check` is run with the generated client in sync with schemas
- **THEN** both `ent:generate` and `ent:lint` exit 0

---

### Requirement: `task migrations:gen` generates Atlas migrations
Running `task migrations:gen NAME=<name>` must run `go run ./cmd/generate-migrations --name=<name>` to emit per-dialect SQL files.

#### Scenario: Migration generation with a valid name
- **WHEN** a developer runs `task migrations:gen NAME=add_foo_column`
- **THEN** per-dialect `.sql` migration files are created under `migrations/<dialect>/`

---

### Requirement: `task migrations:sql` generates an empty SQL migration stub
Running `task migrations:sql NAME=<name>` must run `go run ./cmd/generate-migrations --sql-only --name=<name>`.

#### Scenario: SQL-only stub created
- **WHEN** a developer runs `task migrations:sql NAME=backfill_foo`
- **THEN** empty Goose-formatted `.sql` stubs are created in all dialect directories

---

### Requirement: CLAUDE.md is concise
`CLAUDE.md` must be reduced to at most ~150 lines, containing only: project overview, prerequisites, quick-start commands (via `task`), architecture package-structure summary, and pointers to `.claude/rules/` and `.agent/skills/`.

#### Scenario: CLAUDE.md fits within 150 lines
- **WHEN** the updated `CLAUDE.md` is committed
- **THEN** `wc -l CLAUDE.md` reports ≤ 150 lines

#### Scenario: NarInfo migration runbook is removed from CLAUDE.md
- **WHEN** reviewing the updated `CLAUDE.md`
- **THEN** the file contains no section titled "NarInfo Migration Strategy" and no `ncps migrate-narinfo` CLI reference

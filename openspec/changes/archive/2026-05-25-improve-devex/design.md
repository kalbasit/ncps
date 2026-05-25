# Design: improve-devex

## Context

`ncps` uses `go-task` as the task runner (it is available in the Nix dev shell), but has no `Taskfile.yml`. This creates a silent inconsistency: `.claude/rules/verify-before-completion.md` requires `task fmt`, `task lint`, and `task test` to all pass before any task is reported complete — but none of those commands exist. Every contributor and AI agent is blocked on verification.

Separately, `CLAUDE.md` is ~500 lines. The bottom two-thirds documents integration test env vars, operational NarInfo migration procedures, Helm chart testing, and Kind cluster setup — none of which is context an AI agent needs on every turn. Eight `.claude/rules/` files and an updated `.agent/skills/tdd/` skill now carry the behavioral guidance that was previously buried in `CLAUDE.md`.

Three skills were also deleted (build, ncps, test) and replaced — their content survives in the rules and the tdd skill, but CLAUDE.md still references them as if they exist.

## Goals / Non-Goals

**Goals:**

- Make `task fmt`, `task lint`, and `task test` work — satisfying the `verify-before-completion` rule
- Add `task test:auto` that can start/stop backing services automatically for integration test runs
- Reduce CLAUDE.md to ~150 lines covering only project overview, quick-start, and architecture pointers
- Confirm the 8 new `.claude/rules/` files are correct and non-overlapping

**Non-Goals:**

- No changes to Go source code, tests, or production behaviour
- No changes to CI — `nix flake check` remains the canonical CI entrypoint
- No new test cases — `test:auto` orchestrates existing tests
- Not replacing `nix fmt` or `golangci-lint` with alternatives

## Decisions

### D1 — Flat Taskfile, namespaced with colons

Use a single flat `Taskfile.yml` (no `includes:`) with colon-namespaced task names (e.g., `ent:generate`, `test:auto`). `ncps` is a single Go module with no sub-apps, so includes add indirection without benefit.

_Alternative considered_: `includes:` with per-domain task files (`tasks/ent.yml`, `tasks/test.yml`). Rejected — unnecessary indirection for a project this size.

### D2 — Taskfile tasks

| Task | Command | Description |
|------|---------|-------------|
| `default` | `task --list` | Show available tasks |
| `fmt` | `nix fmt` | Format all files |
| `lint` | `golangci-lint run` | Lint (fail on issues) |
| `lint:fix` | `golangci-lint run --fix` | Lint + auto-fix |
| `test` | `go test -race ./...` | Unit tests (no backends required) |
| `test:integration` | script | Enable all backends (must already be running), run full suite |
| `test:auto` | script | Auto-start backends if not running, run full suite, teardown |
| `ent:generate` | `go generate ./ent/...` | Regenerate Ent client |
| `ent:lint` | `go run ./cmd/ent-lint --root .` | Lint Ent schemas |
| `ent:check` | deps: generate + lint | Full Ent check |
| `migrations:gen` | `go run ./cmd/generate-migrations --name={{.NAME}}` | Generate Atlas migrations |
| `migrations:sql` | `go run ./cmd/generate-migrations --sql-only --name={{.NAME}}` | Generate SQL-only stub |
| `build` | `go build .` | Build binary |
| `dev` | `./dev-scripts/run.sh` | Start dev server (hot-reload) |
| `deps` | `nix run .#deps` | Start backing services |

The three tasks referenced by `verify-before-completion` (`fmt`, `lint`, `test`) are the critical ones. All others make CLAUDE.md's command table accurate and discoverable.

### D3 — `test:auto` via a dev script

`task test:auto` delegates to `dev-scripts/test-auto.sh`. The script:

1. Checks whether each backing service port is already open (`nc -z`): Garage/S3 on 9000, PostgreSQL on 5432, MariaDB on 3306, Redis on 6379
2. If any are missing, starts `nix run .#deps` in the background and waits for all four ports with a timeout (~60 s)
3. Runs `eval "$(enable-integration-tests)"` to export env vars into the current shell
4. Runs `go test -race ./...`
5. If the script started the deps process, sends SIGTERM to the process group on exit (via `trap`)
6. Propagates the test exit code

_Why a script instead of inline task YAML_: The trap/cleanup pattern and conditional process management are awkward in task YAML. Shell scripts are easier to test and read for this kind of orchestration.

_Alternative considered_: A Go-based test harness. Rejected — shell is sufficient and avoids a build step before testing.

### D4 — CLAUDE.md sections to keep

| Keep | Remove |
|------|--------|
| Project Overview (3 lines) | All of "Development Workflow" (covered by dev-scripts + env-execution rule) |
| Prerequisites + dev shell tools | "Dependency Management" detail (covered by env-execution rule) |
| `task` command table (pointing to Taskfile) | "Code Quality" linting detail (covered by lint skill + nolint rule) |
| `nix flake check` / `nix build` | All of "Testing" detail (covered by tdd skill + verify rule) |
| Architecture → Package Structure | Entire "NarInfo Migration Strategy" section (~200 lines, operational docs) |
| Key Interfaces | "Helm Chart Testing" detailed section (keep 3-line pointer only) |
| Database summary (2 sentences) | "Kind Integration Tests" detailed section (keep 3-line pointer only) |
| Pointers to skills and rules | "CI/CD and GitHub Actions" detail (branches: [main] note stays) |

The NarInfo Migration Strategy section is the largest cut (~200 lines). It documents runbook-style operational procedures (`ncps migrate-narinfo` flags, SQL verification queries, rollback steps). This belongs in operator documentation, not AI working context.

### D5 — `.claude/rules/` validation outcome

All 8 rules are correct and non-overlapping. One dependency to note: `verify-before-completion.md` requires `task fmt`, `task lint`, and `task test` — which only become valid after this change adds `Taskfile.yml`. No rule changes needed.

## Risks / Trade-offs

**`test:auto` port detection is heuristic** → Mitigation: if a port is open but the service is not fully healthy, tests will fail with connection errors (same as before). The script reduces friction; it doesn't guarantee service health. The `nix run .#deps` self-validation already handles health checks internally.

**CLAUDE.md loses the NarInfo migration runbook** → Mitigation: the content is preserved in git history. If it needs to resurface, it belongs in `docs/` or the GitHub wiki, not in AI working context. Operators performing migrations should use official documentation, not CLAUDE.md.

**`task test` runs only unit tests** → This is intentional. The `verify-before-completion` rule requires `task test` to be zero-friction (no deps, no env vars). Integration tests require `task test:auto` or `task test:integration`. The rule's intent is "did you break anything obvious" — full integration coverage runs in CI via `nix flake check`.

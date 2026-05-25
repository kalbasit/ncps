# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

ncps (Nix Cache Proxy Server) is a Go application that acts as a local binary cache proxy for Nix. It fetches store paths from upstream caches (like cache.nixos.org) and caches them locally, reducing download times and bandwidth usage.

## Prerequisites

Uses Nix flakes with direnv (`.envrc` with `use_flake`). Tools available in dev shell: go, go-task, golangci-lint, sqlfluff, delve, watchexec.

Bootstrap in any non-interactive shell before running commands (see `.claude/rules/env-execution.md`):

```bash
direnv status | grep -q "Found RC allowed 0" || direnv allow .
unset DIRENV_DIR DIRENV_FILE DIRENV_WATCHES DIRENV_DIFF && eval "$(direnv export bash)"
```

## Common Commands

```bash
# Format, lint, test
task fmt                          # Format all project files (nix fmt)
task lint                         # Lint Go code
task lint:fix                     # Auto-fix lint issues
task test                         # Unit tests — no backends required
task test:integration             # Full suite — deps must already be running
task test:auto                    # Auto-start deps, run full suite, teardown

# Development
task dev                          # Start dev server (hot-reload via run.py)
task deps                         # Start backing services (Garage, PG, MariaDB, Redis)
task build                        # Build binary

# Ent
task ent:generate                 # Regenerate Ent client (go generate ./ent/...)
task ent:lint                     # Lint Ent schemas for invariant violations
task ent:check                    # Generate + lint (CI check)

# Migrations
task migrations:gen NAME=<name>   # Generate per-dialect Atlas migrations
task migrations:sql NAME=<name>   # Generate empty SQL-only stub

# Nix
nix build                         # Build with Nix
nix build .#docker                # Build Docker image
nix flake check                   # Run all CI checks (tests + lint + drift)
nix run .#deps                    # Start all backing services via process-compose
```

## Rules and Skills

Behavioral guidance lives in `.claude/rules/` and `.agent/skills/`. Key rules:

- `tdd-required.md` — All production changes must use TDD (`/tdd`)
- `verify-before-completion.md` — Run `task fmt`, `task lint`, `task test` before reporting done
- `env-execution.md` — Bootstrap direnv before any non-git command
- `ent-migrations.md` — Ent schema + migration workflow and five codegen invariants
- `no-panic-outside-main.md` — Return errors; no `panic` outside `main`
- `nolint-with-comments.md` — Every `//nolint` needs an explanatory comment

Key skills: `tdd/`, `ent-schema/`, `migrate-new/`, `migrate-up/`, `migrate-down/`, `lint/`

## Architecture

### Package Structure

- `cmd/` — CLI commands (serve, global flags, OpenTelemetry bootstrap)
- `cmd/ent-lint/` — AST-based linter enforcing the five Ent codegen invariants
- `cmd/generate-migrations/` — Atlas-driven per-dialect migration generator
- `cmd/atlas-sum-check/` — CI helper verifying `atlas.sum` integrity
- `pkg/cache/` — Core caching logic and upstream cache fetching
- `pkg/storage/` — Storage abstraction: `local/` (filesystem) and `s3/` (S3-compatible)
- `pkg/server/` — HTTP server (Chi router)
- `pkg/database/` — Ent client facade; `migrate/` — migration state detection and apply
- `pkg/nar/` — NAR format handling
- `ent/schema/` — Hand-authored Ent schemas (DDL source of truth)
- `ent/` — Generated Ent client (committed; regenerate with `task ent:generate`)
- `migrations/` — Goose-formatted Atlas migrations per dialect (`sqlite/`, `postgres/`, `mysql/`)

### Key Interfaces (`pkg/storage/store.go`)

- `ConfigStore` — Secret key storage
- `NarInfoStore` — NarInfo metadata storage
- `NarStore` — NAR file storage

Both `local/` and `s3/` backends implement these interfaces.

### Database

Supports SQLite (default), PostgreSQL, and MySQL/MariaDB via [Ent](https://entgo.io/) ORM, [Atlas](https://atlasgo.io/) migration diffing, and [Goose](https://github.com/pressly/goose) runtime runner. Selected via URL scheme in `--cache-database-url` (e.g. `sqlite:/path/to/db`, `postgresql://...`, `mysql://...`).

Use `/migrate-new` for schema changes, `/migrate-up` to apply, `/migrate-down` for the expand-contract policy (migrations are forward-only).

## Configuration

YAML/TOML/JSON config. See `config.example.yaml` for all options. Key areas: cache hostname/data-path/database-url/max-size, upstream caches and public keys, OpenTelemetry, Prometheus, server address and verb control.

## CI/CD

CI runs `nix flake check` on PRs targeting `main` — covers unit tests, integration tests (per-backend cohort derivations), lint, and drift checks. Workflows are restricted to `branches: [main]` to avoid wasted CI on stacked PR intermediates.

Integration tests in CI run via per-backend Nix derivations (`ncps-s3-tests`, `ncps-postgres-tests`, `ncps-mysql-tests`, `ncps-redis-tests`). Locally, start dependencies with `nix run .#deps` then use `task test:auto` for zero-setup integration runs.

## Helm Chart and Kind Tests

- Helm unit tests: `helm unittest charts/ncps` (or `nix flake check` in CI). See `charts/ncps/tests/README.md`.
- Kind integration tests: `k8s-tests all` (12 deployment permutations). See `nix/k8s-tests/README.md`.

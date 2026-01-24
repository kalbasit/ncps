---
name: ncps
description: Developing, running, and testing ncps
---

# NCPS Developer Skill

This skill documents the standard workflows for developing, building, running, and testing the `ncps` project.

## 🔴🟢🔵 Development Protocol: TDD ONLY

> [!IMPORTANT]
> **TEST FIRST. CODE SECOND.**
> You MUST follow Test Driven Development (TDD) for ALL Go code changes, including FEATURES and BUGS.

**The Cycle:**

1. **Red**: Write a failing test case that reproduces the bug or defines the new feature.
2. **Green**: Write the *minimal* amount of code necessary to make the test pass.
3. **Refactor**: Clean up the code while ensuring tests remain green.

**Do not write implementation code without a failing test.**

## Environment Setup

The project uses Nix to manage dependencies. Ensure you have Nix installed.

1. **Start Services**: Before running the application or tests that require backing services (Postgres, MySQL, Redis, MinIO), start them using:

    ```bash
    nix run .#deps
    ```

    This command starts a process manager (process-compose) that runs all necessary services in the foreground. Keep this running in a separate terminal.

2. **Enter Development Shell**: To get all necessary tools (go, dbmate, etc.) in your PATH:

    ```bash
    nix develop
    ```

    Or use `direnv allow` if you have `direnv` installed.

3. **Enable Service Connectivity**: To export the environment variables required to connect to the backing services (especially for running `go test`), you must source the configuration:

    ```bash
    eval "$(enable-integration-tests)"
    ```

    You can also enable specific services individually (e.g., `eval "$(enable-postgres-tests)"`).

## Running the Application

The primary way to run `ncps` locally during development is via `dev-scripts/run.py`.

### Basic Usage

```bash
./dev-scripts/run.py [flags]
```

### Common Flags

- `--mode`: `single` (default) or `ha` (High Availability).
- `--db`: `sqlite` (default), `postgres`, or `mysql`.
- `--storage`: `local` (default) or `s3`.
- `--locker`: `local` (default) or `redis`.
- `--instances`: Number of instances for HA mode (default: 3).

### Examples

**Run single instance with SQLite (default):**

```bash
./dev-scripts/run.py
```

**Run HA cluster with Postgres and S3:**

```bash
./dev-scripts/run.py --mode ha --db postgres --storage s3 --locker redis
```

> [!NOTE]
> The script automatically handles database migrations using `dbmate` before starting the application.

## Testing

### Unit Tests

Run standard Go tests:

```bash
go test ./...
```

### Database Tests

The project has extensive database tests in `pkg/database`. These require the backing services to be running (see "Environment Setup").

### Kubernetes Deployment Tests

To test Kubernetes deployments (requires a cluster):

```bash
./dev-scripts/test-deployments.py --config dev-scripts/test-deployments-config.yaml
```

## Database Management

Migrations are managed via `dbmate`. See the `dbmate` skill for general usage, but note that `dev-scripts/run.py` handles applying migrations automatically for development.

To create a new migration for all supported engines, use the workflow:

```bash
/migrate-new "migration_name"
```

## ADDED Requirements

### Requirement: The runtime image's migration entrypoint is `/bin/ncps`, not `dbmate`

The runtime container image SHALL ship the `ncps` binary as the sole migration entrypoint at `/bin/ncps`. The image SHALL NOT include the `dbmate` binary. Deployment artifacts (the Helm chart, any container `command`/`args` in templates, the migration `Job`, deployment and statefulset init containers) SHALL invoke `/bin/ncps migrate up`. The database URL SHALL be supplied via the `--cache-database-url` flag or the `CACHE_DATABASE_URL` environment variable (the env-var source already declared by the flag in `pkg/ncps/migrate.go`); the legacy `DATABASE_URL` env var (the dbmate convention) SHALL NOT be expected by any first-party deployment artifact.

#### Scenario: Helm chart migration containers invoke `ncps migrate up`

- **WHEN** a Helm chart consumer renders `charts/ncps` (any of the migration-bearing templates: `migration-job.yaml`, `statefulset.yaml`, `deployment.yaml`)
- **THEN** the rendered container `command` SHALL be exactly `["/bin/ncps"]`
- **AND** the rendered `args` SHALL be exactly `["migrate", "up"]`
- **AND** the rendered `env` SHALL include an entry named `CACHE_DATABASE_URL` (either a literal `value` for SQLite or a `valueFrom.secretKeyRef` for Postgres/MySQL)
- **AND** the rendered `env` SHALL NOT include an entry named `DATABASE_URL`

#### Scenario: Developer documentation describes the `ncps migrate up` workflow

- **WHEN** a developer reads `docs/docs/Developer Guide/Contributing.md`, `docs/docs/Developer Guide/Testing.md`, or `docs/docs/User Guide/Configuration/Database/SQLite Configuration.md`
- **THEN** any worked example of running migrations SHALL invoke `ncps migrate up` (with `--cache-database-url=<url>` or `CACHE_DATABASE_URL=<url>` set) — not `dbmate up`
- **AND** any reference to the migration directory SHALL point at `migrations/<dialect>/`, not the historical `db/migrations/<dialect>/`

#### Scenario: Runtime image does not contain dbmate

- **WHEN** the runtime image (`.#docker`) is built
- **THEN** the resulting image's filesystem SHALL NOT contain a `/bin/dbmate` (or any other path serving the dbmate binary)
- **AND** `nix run .#docker | docker run --rm -it … which dbmate` SHALL exit non-zero

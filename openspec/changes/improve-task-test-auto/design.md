# Design: improve-task-test-auto

## Context

`task test:auto` drives `dev-scripts/test-auto.sh`, which starts backing services via
`nix run .#deps` and then runs `go test -race ./...`.  The problem is that `nix run .#deps`
embeds hard-coded ports in the process-compose YAML at Nix eval time.  If another process
holds any of those ports (9000/5432/3306/6379), the test run either silently shares state
with the existing service or waits 60 s and fails.

The existing start scripts — `start-garage.sh`, `start-redis.sh`, and the postgres/mysql
start scripts — already read ports from environment variables (`NCPS_TEST_S3_PORT`, `PGPORT`,
`MYSQL_TCP_PORT`, `REDIS_PORT`).  The process-compose layer is the only thing that hard-codes
the values.  We need to thread dynamic ports from the caller through process-compose down to
those scripts, and update the `enable-*` helper scripts so they emit the correct endpoint
URLs at runtime.

## Goals / Non-Goals

**Goals:**

- `task test:auto` allocates random free ports and always starts its own isolated service
  instances — no shared state with the interactive `task deps` or another `test:auto` run
- Clean, deterministic teardown after every run (success or failure)
- `enable-s3-tests` / `enable-postgres-tests` emit endpoint URLs with the correct dynamic
  port so `go test` connects to the right instance

**Non-Goals:**

- No changes to `nix run .#deps` — the interactive fixed-port dev tool is unchanged
- No changes to Go test code or the integration test helpers (`testhelper/`)
- No changes to CI (`nix flake check`) — CI already has isolated network namespaces per
  derivation and doesn't use `task test:auto`
- Not supporting simultaneous parallel `task test:auto` invocations (sequential only)

## Decisions

### D1 — `process-compose.test-deps` profile with `disable_env_expansion = false`

Add a second process-compose app `test-deps` in `nix/process-compose/flake-module.nix`.
Set `disable_env_expansion = false` so process-compose expands `${PORT_VAR}` placeholders
at launch time from the caller's environment.  In Nix string literals, placeholders are
written as `"\${PORT_VAR}"` (backslash escapes the `$` from Nix interpolation).

The existing `process-compose.deps` attrset is kept unchanged.

_Alternative_: Patch the existing `deps` profile to accept env var overrides.  Rejected —
changes the dev-facing entrypoint, risks breaking interactive use when env vars are
accidentally set in the shell.

### D2 — Python free-port allocation in `test-auto.sh`

```python
python3 -c "
import socket
ss = [socket.socket() for _ in range(7)]
[s.bind(('', 0)) for s in ss]
ports = [str(s.getsockname()[1]) for s in ss]
[s.close() for s in ss]
print(' '.join(ports))
"
```

Bind all sockets simultaneously before closing any, so all 7 ports are guaranteed distinct.
Python is available in every Nix devshell that has Go (and is required by `./dev-scripts/run.py`
anyway).

_Alternative_: Bash `shuf` + `/dev/tcp` re-check loop.  Rejected — inherently racy (a port
can be claimed between the `shuf` pick and the service bind).

### D3 — Detached process-compose with control port (`TEST_PC_PORT`)

Launch as:
```sh
nix run .#test-deps -- up --detached --tui=false -p "$TEST_PC_PORT"
```

Stop as:
```sh
process-compose down -p "$TEST_PC_PORT" 2>/dev/null \
  || nix run .#test-deps -- down -p "$TEST_PC_PORT"
```

The control port is the 7th randomly allocated port.  It uniquely identifies this
process-compose instance and allows clean shutdown independent of PID tracking.
The state is persisted to `${TMPDIR:-/tmp}/ncps-test-deps.env` so `test:deps:stop` can
tear down even from a different shell.

_Alternative_: Background process with `&` + PID tracking (current approach).  Rejected —
`kill $PID` kills only the process-compose parent; child service processes become orphans.
`process-compose down` handles the full process group.

### D4 — Instance-unique ready markers for postgres-init and mariadb-init

The inline init commands in the `test-deps` profile write marker files suffixed with
`${TEST_PC_PORT}` (e.g. `/tmp/ncps-postgres-${TEST_PC_PORT}.ready`) instead of the fixed
names used by `deps` (`/tmp/ncps-postgres-ready`).  This prevents a stale or concurrent
marker from masking a failed init.

Garage already uses a per-UID marker (`/tmp/ncps-garage-$(id -u).ready`); since we're
running sequential single-user instances that's sufficient — no change needed for Garage.

### D5 — Update `enable-*` scripts to use port env vars

`enable-s3-tests` currently hard-codes `http://127.0.0.1:9000`.  The updated version reads
`${NCPS_TEST_S3_PORT:-9000}` at invocation time.  Because `test-auto.sh` exports
`NCPS_TEST_S3_PORT` before calling `eval "$(enable-integration-tests)"`, the script
automatically emits the correct endpoint.

`enable-postgres-tests` is updated similarly with `${PGPORT:-5432}`.  `enable-mysql-tests`
with `${MYSQL_TCP_PORT:-3306}`.  `enable-redis-tests` already emits only a flag with no
URL — no change needed.

Interactive use (shell started with no port env vars set) falls back to the default ports
via `:-default` syntax, preserving the existing behaviour exactly.

### D6 — `test:deps:start` and `test:deps:stop` Taskfile tasks

Add two helper tasks so other workflows (e.g. a future CI smoke-test script) can manage
the lifecycle independently.  `test:auto` remains the canonical single-entry-point that
calls both.

## Risks / Trade-offs

**Port allocation race** → The bind-all-then-close pattern minimises but doesn't eliminate
races (a kernel can reuse ports immediately after close).  Mitigation: the window is
microseconds-wide and the failure mode is a clear process-compose startup error, not silent
test contamination.

**process-compose `--detached` availability** → `--detached` was added in process-compose
v0.43.  The Nix flake pins `process-compose-flake` which bundles a known-good version.  No
action needed, but worth noting if the flake input is updated.

**Marker file collision on repeated fast re-runs** → If `test:auto` is killed hard (SIGKILL)
and immediately re-run, the `${TEST_PC_PORT}` will be different (new random port), so marker
files from the previous run are orphaned in `/tmp`.  They cause no harm but are not cleaned
up.  Mitigation: acceptable for a dev-only tool.

**`enable-postgres-tests` connection URL** — The script must emit the correct `PGPORT` value.
If the script is run interactively (terminal stdout), it prints a warning and exits 0, so
no env var is set.  Mitigation: documented in the script and the existing behaviour is
preserved.

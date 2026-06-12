## Context

`createCache` (`pkg/ncps/serve.go:~1154`) is the shared cache constructor used by
`serve`, the migrate commands, and — under `--repair` — by `fsck`'s
chunked-residue janitor (`fsckCommand` → `repairChunkedResidue` →
`createCache`). It calls:

```go
c.SetTempDir(cmd.String("cache-temp-path"))
  → helper.EnsureDirWritable(d)        // os.CreateTemp(d, "boot_test")
```

`SetTempDir`/`EnsureDirWritable` probe writability of `d`. When `d == ""`,
`os.CreateTemp("", …)` resolves to `os.TempDir()` (`/tmp`).

`serve.go:317`, `migrate_chunks_to_nar.go:57`, and `migrate_nar_to_chunks.go:48`
all register a `cache-temp-path` flag with
`Sources: flagSources("cache.temp-path", "CACHE_TEMP_PATH")`. `fsckCommand`
(`pkg/ncps/fsck.go:230-397`) does not. Consequently, on the fsck subcommand
`cmd.String("cache-temp-path")` returns `""` even though the mounted config sets
`cache.temp-path: /tmp/ncps`, and `createCache` probes the read-only `/tmp`,
failing the whole run before any repair.

The Helm chart is already correct: the fsck CronJob mounts the writable temp
volume at `.Values.config.cache.tempPath` and the ConfigMap emits `temp-path`.
The only missing link is the code that lets fsck read that value.

## Goals / Non-Goals

**Goals:**

- Make `fsck --repair` honor `cache.temp-path` / `CACHE_TEMP_PATH` /
  `--cache-temp-path` by registering the flag on `fsckCommand`.
- Document the flag for fsck.
- Confirm (and record) that the chart needs no change.

**Non-Goals:**

- Wiring fsck's other cache flags (CDC tuning, staging, secret-key-path,
  sign-narinfo). Out of scope; the residue janitor disables lazy chunking and the
  secret key falls back to the DB.
- Any change to `createCache`, detection, repair, summary, or exit codes.
- Chart or deployment security-posture changes.

## Decisions

### Decision 1: Register the flag on fsck, mirroring serve — don't special-case the temp dir

Add a single `cli.StringFlag{Name: "cache-temp-path", Usage: …, Sources: flagSources("cache.temp-path", "CACHE_TEMP_PATH")}`
to `fsckCommand`'s flag list. `createCache` already reads
`cmd.String("cache-temp-path")`, so no other code changes are needed — once the
flag exists with its config/env sources, the value flows through.

- **Why over alternatives:** keeps fsck consistent with every other subcommand
  that builds a cache; the fix is the *absence* being corrected, not new
  behavior.
- **Alternative — default the temp dir to the cache data-path inside `createCache`
  (rejected):** changes shared behavior for all commands and masks the real
  inconsistency; larger blast radius for a one-flag omission.
- **Alternative — deployment-only workaround (emptyDir at `/tmp`) (rejected as
  the fix):** valid as an immediate unblock but leaves the code bug; future
  hardened deployments would hit it again.

### Decision 2: Docs get one flag row; chart verified-no-change

Add `--cache-temp-path` to the fsck flags table in
`Integrity Check (fsck).md`, matching how `serve`/migrate already document it.
Add a tasks step to render/inspect the chart's fsck CronJob and confirm no change
is required, so the "no chart change" conclusion is explicit and verified rather
than assumed.

## Risks / Trade-offs

- **[Registering the flag changes fsck's resolved temp dir from `/tmp` to the
  configured path]** → That is precisely the intended fix; the configured path is
  already writable and mounted. Where no config/env/flag is set, behavior is
  unchanged (still `os.TempDir()`).
- **[A test that asserts fsck flag set / temp-dir behavior could be brittle]** →
  Test at the seam: assert `fsckCommand()` exposes a `cache-temp-path` flag (flag
  presence + sources), which is stable and directly encodes the requirement.
- **[Chart truly needs a change and we miss it]** → Mitigated by an explicit
  verify-the-rendered-cronjob task before concluding.

## Migration Plan

Single PR off `main`, independent of the messaging PR. No DB migration, no config
schema change (config key already exists and is already set in prod). Rollback =
revert the commit. Operators on hardened deployments get working `fsck --repair`
on the next image; no redeploy of config needed. TDD: write a failing test
asserting fsck registers `cache-temp-path` (with the right sources), then add the
flag.

## Open Questions

- None. The fix is mechanical and matches three existing precedents.

## Why

`ncps fsck --repair` aborts before performing any repair on a hardened
(read-only-root-filesystem) deployment. The `fsck` subcommand never registers
the `cache-temp-path` flag, so when `--repair` builds a `Cache` for the
chunked-residue janitor, `createCache` calls `SetTempDir(cmd.String("cache-temp-path"))`
with an empty string. That falls back to `os.TempDir()` (`/tmp`), whose
writability probe fails on a read-only `/tmp`:

```
error creating cache for chunked-residue repair: error setting cache temp dir:
error verifying tmp directory is writable: open /tmp/boot_test...: read-only file system
```

The mounted config already sets `cache.temp-path: /tmp/ncps` (a writable
emptyDir), and `serve`, `migrate-chunks-to-nar`, and `migrate-nar-to-chunks` all
register this flag — only `fsck` is missing it, so it ignores the config and
dies. This blocks production residue cleanup entirely (observed: a `--repair`
run completed phases 1–2 then failed entering phase 3, repairing nothing).

## What Changes

- Register the `cache-temp-path` flag on `fsckCommand`, with
  `Sources: flagSources("cache.temp-path", "CACHE_TEMP_PATH")`, matching
  `serve.go` and the migrate commands. `createCache` already reads this flag, so
  fsck will honor `cache.temp-path` from config / `CACHE_TEMP_PATH` from env.
- Document `--cache-temp-path` in the fsck flags reference doc
  (`Integrity Check (fsck).md`), alongside the existing flags.
- Helm chart: no change required — the fsck CronJob already mounts the writable
  temp volume at `.Values.config.cache.tempPath` and the ConfigMap already emits
  `temp-path`. The tasks include verifying this so the conclusion is explicit.

## Capabilities

### New Capabilities
<!-- none -->

### Modified Capabilities
- `fsck`: add a requirement that the `fsck` subcommand honors the configured
  cache temp path (config `cache.temp-path` / env `CACHE_TEMP_PATH` / flag
  `--cache-temp-path`) when building a cache for `--repair`, instead of always
  falling back to the system temp dir.

## Impact

- Code: `pkg/ncps/fsck.go` (one flag registration). No change to `createCache`,
  detection, or repair logic.
- Docs: `docs/docs/User Guide/Operations/Integrity Check (fsck).md` (one flag
  row).
- Charts: none (verified already-correct).
- No database, migration, API, or dependency changes.
- I/O / latency / memory: negligible. The flag only redirects where the
  `--repair` cache writes its temp files — to the already-mounted
  `cache.temp-path` instead of `/tmp`. No new scans, allocations, or network
  calls.

## Non-goals

- Not changing fsck's detection, summary, repair logic, or exit codes.
- Not wiring fsck's other cache flags (CDC tuning, in-flight staging,
  sign-narinfo, secret-key-path); the residue janitor explicitly disables lazy
  chunking and the secret key already falls back to the database. Only the
  temp-path blocker is in scope.
- Not changing the chart or the deployment's read-only-root-filesystem posture.
- Not addressing fsck's memory/throughput at very large chunk-residue counts
  (separate concern).

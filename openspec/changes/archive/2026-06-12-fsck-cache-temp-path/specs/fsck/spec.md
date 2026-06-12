## ADDED Requirements

### Requirement: Fsck MUST honor the configured cache temp path when building a cache for --repair

The `fsck` subcommand SHALL register the `cache-temp-path` flag with the same
configuration and environment sources as the `serve` and migrate subcommands
(config key `cache.temp-path`, environment variable `CACHE_TEMP_PATH`). When
`fsck --repair` builds a cache (for the chunked-residue reconcile janitor), it
SHALL use the resolved cache temp path for its temporary directory instead of
unconditionally falling back to the operating system's default temp directory.
On a deployment whose default temp directory is not writable, fsck SHALL still
proceed with repair as long as the configured cache temp path is writable.

#### Scenario: Configured temp path is used under --repair

- **WHEN** `cache.temp-path` (or `CACHE_TEMP_PATH`, or `--cache-temp-path`) is set
  to a writable directory and `ncps fsck --repair` runs in CDC mode
- **THEN** the cache created for chunked-residue reconciliation uses that
  configured directory for its temp files
- **AND** fsck does not fail its temp-directory writability check

#### Scenario: Read-only system temp dir no longer blocks repair

- **WHEN** the OS default temp directory (`/tmp`) is read-only but the configured
  cache temp path points at a writable directory
- **THEN** `ncps fsck --repair` proceeds to the repair phase instead of aborting
  with a "tmp directory is writable" / read-only filesystem error

#### Scenario: Flag parity with serve and migrate

- **WHEN** the `fsck` subcommand's flags are enumerated
- **THEN** a `cache-temp-path` flag is present, sourced from `cache.temp-path`
  and `CACHE_TEMP_PATH`, matching the `serve`, `migrate-chunks-to-nar`, and
  `migrate-nar-to-chunks` subcommands

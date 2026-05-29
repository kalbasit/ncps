# Design: speed-up-flake-check

## Context

`nix/checks/flake-module.nix` defines `mkCohort`, which `overrideAttrs` from
`packages._ncps-base` (a `doCheck=false` `buildGoModule`) and, in `checkPhase`,
runs `go test -race -count=1 -covermode=atomic -coverpkg=… <paths>`. `go test`
compiles the instrumented test binaries before running them, and Go's `GOCACHE`
is not shared between Nix sandboxes — so all 5 cohorts compile from scratch.

The 4 backend cohorts (`s3`, `postgres`, `mysql`, `redis`) compile the
**identical** binary set (`./pkg/... ./internal/... ./migrations/...
./testhelper/...`, `-coverpkg` identical) and differ only in which backend is
up. The cmd cohort compiles a separate set (`./cmd/... ./ent/... .`). Measured
cold-cache compile (8-core local, `-run '^$'`): backend set ~60s, cmd set ~62s;
includes cgo `mattn/go-sqlite3` under `-race`. On 4-core CI runners that is
~2 min each, so 3 of the 4 backend compiles (~6 min) are redundant and contend
for cores.

## Goals / Non-Goals

**Goals**
- Compile the race+coverage test binaries once; reuse across all cohorts.
- Preserve cohort isolation, per-cohort `cover.out`, race, and backend gating.

**Non-Goals**
- Merging cohorts; changing coverage scope, backends, or test selection.

## Decisions

### D1 — A shared `_ncps-test-cache` derivation exporting a populated GOCACHE
Add a derivation (overriding `_ncps-base`) whose build runs the **compile-only**
form of every distinct cohort invocation, then copies the resulting `GOCACHE`
into `$out`:

```
export GOCACHE=$TMPDIR/gocache
# backend cohort binary set
go test -race -count=1 -covermode=atomic \
  -coverpkg=./pkg/...,./internal/...,./migrations/...,./testhelper/... \
  -run '^$' ./pkg/... ./internal/... ./migrations/... ./testhelper/...
# cmd cohort binary set
go test -race -count=1 -covermode=atomic -coverpkg=./cmd/...,./ent/...,. \
  -run '^$' ./cmd/... ./ent/... .
cp -r "$GOCACHE" "$out"
```

`-run '^$'` compiles and links the test binaries but runs zero test functions
(so no backend is needed and nothing flakes). Both invocations are included so
the cache satisfies **every** cohort, backend and cmd alike.

### D2 — Cohorts consume the cache before `go test`
`mkCohort`'s `preCheck` seeds a writable `GOCACHE` from the shared output (the
store path is read-only):

```
export GOCACHE="$TMPDIR/gocache"
cp -r --no-preserve=mode,ownership ${testCache}/* "$GOCACHE"/
```

The cohort then runs its existing `go test …` unchanged; compilation is a cache
hit, leaving backend startup + test execution as the dominant cost.

### D3 — Cache-key correctness is the linchpin
Go keys `GOCACHE` entries by exact compile inputs: toolchain version, build
flags (`-race`, `-covermode=atomic`, `-coverpkg`, tags), and package source. The
cache only hits if the shared derivation compiles with **identical** flags and
the **same source/vendor** as the cohorts. Both derive from `_ncps-base` (shared
`src` + `vendorHash`), and the cohort `go test` flags are copied verbatim into
the cache build. Any drift (a cohort changing a flag) silently degrades to a
recompile — correct, just slower — so the flag strings are factored into a
single Nix `let` binding referenced by both the cache build and the cohorts.

*Alternatives considered:*
- **Pre-compiled `.test` binaries (`go test -c`)**: one binary per package,
  then run each with `-test.coverprofile`. More invasive (per-package
  orchestration, manual cover-profile collection) and brittle; rejected in favor
  of the transparent GOCACHE reuse that keeps `go test` as the cohort entrypoint.
- **Collapse cohorts into one derivation**: compiles once trivially but loses
  per-backend failure attribution (a `speed-up-ci` non-goal we keep honoring).

## Risks / Trade-offs

- **Cache miss from flag/source drift** → degrades to today's behavior (extra
  compile), never incorrect. Mitigated by sharing flag strings in one binding.
- **Per-cohort cache copy IO** → bounded (hundreds of MB), far cheaper than a
  cgo+race recompile; happens in parallel with backend startup.
- **`_ncps-base` already builds the non-test package** → the test cache is
  additive; the base build is unchanged and still cached by Nix.

## Migration Plan

1. Add `_ncps-test-cache`; wire `mkCohort` to consume it.
2. `nix flake check -L`; confirm cohorts pass, cohort logs show cache hits
   (no cgo/sqlite recompile), and `.#ncps.coverage` still merges one profile.
3. Record before/after x86 leg wall-clock.
4. **Rollback:** revert `nix/checks/flake-module.nix` — no persisted state.

## Open Questions

- Whether to also seed the cache for `golangci-lint-check` (shares the source
  set) — out of scope unless trivial; flag in tasks as a stretch check.

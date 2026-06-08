## Context

These are dev/test-harness fixes (no ncps product code). The unifying theme is
making the dev/test tooling depend only on what the flake actually provides and
on the actual permission/policy constraints of the dev environment.

## Decisions

### S3 cleanup: `boto3`, empty objects (not `mc`, not delete+recreate)

- **Why `boto3` over `mc`**: `boto3` is already declared in the dev shell
  (`nix/devshells/flake-module.nix`) and used by `nix/k8s-tests`. `mc` is not in
  the flake at all — it only worked off a developer's personal `PATH`, and the
  `dev-s3-backend` spec already forbids MinIO tooling. `aws` (awscli2) is also in
  the flake, but `boto3` keeps the dev scripts dependency-free of an external CLI
  and matches the existing k8s-tests pattern.
- **Why empty objects instead of delete+recreate**: the dev Garage access key is
  scoped to the pre-provisioned `test-bucket` and lacks `createBucket`. Deleting
  the bucket (`mc rb`) and recreating it (`mc mb`) fails on the recreate
  (`Forbidden`) and leaves no bucket. Emptying objects
  (`list_objects_v2` → `delete_objects`) achieves the same "clean bucket" state
  with the permissions the key actually has, and is idempotent across restarts.

### mysql identifier quoting

`key` is a reserved word in MySQL/MariaDB but not (in this context) in
sqlite/postgres, which is why the bug only manifested on mysql. A small
`quote_ident(db, name)` keeps the probe portable: backticks for `mysql`,
double-quotes elsewhere.

### k8s SQLite probe profile

`kubectl debug --profile=restricted` injects `runAsNonRoot: true` into the
ephemeral debug container. The `nouchka/sqlite3` image runs as root, so the
kubelet refuses to create the container. The storage probe already demonstrates
the working pattern — same `kubectl debug` against the same hardened pods, but
without `--profile=restricted`, so the root `busybox` image is permitted. Drop
the profile from the SQLite probe to match. (The ncps pods' own hardening is
unchanged; only the throwaway debug container's policy is relaxed, scoped to the
test harness.)

## Risks / Trade-offs

- Relaxing the debug container off `restricted` lets it run as root — acceptable
  for an ephemeral, test-only introspection container that must read a
  root-owned DB file; it does not weaken the ncps pod's own security context.
- The boto3 cleanup assumes the bucket already exists (provisioned by
  `nix run .#deps`); a best-effort no-op is fine since the bucket is pre-created.

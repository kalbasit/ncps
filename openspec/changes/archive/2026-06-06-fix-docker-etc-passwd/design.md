## Context

`nix/packages/docker.nix` builds the image with `dockerTools.buildLayeredImage`. `/etc/passwd` and `/etc/group` are created as `writeTextFile` derivations and added to a `buildEnv` that is passed in `contents`. `buildEnv` combines paths by **symlinking**, and `buildLayeredImage` preserves those symlinks, so in the image `/etc/passwd` and `/etc/group` are absolute symlinks into the nix store (`/etc/passwd -> /nix/store/…-passwd/etc/passwd`).

Recent containerd (kindest/node v1.35.0, and production k8s of similar vintage) resolves `etc/passwd` with a securejoin during container creation; an absolute symlink target is treated as escaping the rootfs → `openat etc/passwd: path escapes from parent`, and the container never starts. This blocks every ncps pod.

## Goals / Non-Goals

**Goals:**
- `/etc/passwd` and `/etc/group` are regular files in the image, identical content to today.
- Containers start on strict-securejoin runtimes.
- Keep the no-shell/no-tools `disallowedRequisites` guard.
- A regression assertion so this cannot silently regress.

**Non-Goals:**
- No change to securityContext, other `/etc` symlinks, the kind node version, or app code.

## Decisions

**1. Materialize the files via `fakeRootCommands` (not `contents`).**
Write `etc/passwd` and `etc/group` as real files in `dockerTools.buildLayeredImage`'s `fakeRootCommands`, and remove the two `writeTextFile` entries from the `buildEnv`. Real files at the image root are not symlinks, so securejoin passes. `fakeRootCommands` (not `extraCommands`) is required because the customisation-layer working tree already contains a read-only, store-symlinked `/etc` (cacert contributes `/etc/ssl`); `fakeRootCommands` runs under fakeroot in the assembled rootfs, so it can re-materialize `/etc` as a writable real directory and then write the files. (Plain `extraCommands` was tried first and failed with `etc/passwd: Permission denied`.)

- Alternatives considered:
  - `fakeNss` / `shadowSetup`: pulls in extra machinery and changes content/format; heavier than needed.
  - Keep them in `contents` but post-process: fragile (depends on buildEnv symlink layout).
  - Plain `extraCommands`: cannot write into the read-only store-symlinked `/etc` of the customisation layer (`Permission denied`).
  - Chosen `fakeRootCommands` is the minimal mechanism that can both rewrite the read-only `/etc` and create the real files.

**2. Keep `disallowedRequisites`.** The passwd/group text files are inlined into `fakeRootCommands` (here-docs), so they add nothing to the closure. The guard stays on the `buildEnv` of the remaining real contents (cacert, tzdata, ncps).

**3. Regression assertion.**
Add a nix check (or a shell assertion in the existing test surface) that builds the image, extracts the rootfs, and asserts `etc/passwd` and `etc/group` are regular files containing the expected entries. Prefer a `flake check` / `nix build` of a small derivation that inspects `config.packages.docker` so CI catches a regression to symlinks.

## Risks / Trade-offs

- [fakeRootCommands runs in the assembled rootfs where `/etc` is a read-only store symlink] → Re-materialize `/etc` as a writable real directory first (copy through the symlink), then write the files; use only build-sandbox tools that do not enter the image closure so the guard is unaffected.
- [Removing entries from buildEnv changes the contents closure hash] → Expected; image content is equivalent. Verify the image still has cacert + tzdata + ncps and starts.
- [Other store symlinks remain] → Acceptable: `/etc/ssl` etc. are read by the app post-start (normal symlink resolution inside the running container), not by the runtime during creation, so they do not trip securejoin. Out of scope.
- [openspec-guard blocks merge] → archive this change before merge.

## Migration Plan

Pure image-packaging fix. No data/schema/runtime migration. Rollout: build + push the new image; pods that previously failed to create now start. Rollback: revert the file.

## Open Questions

- Should the regression check live as a dedicated `flake check` (`checks.docker-etc-files`) or fold into an existing packaging test? Lean toward a small dedicated check so failure is legible. Resolve in apply.
- Full in-cluster confirmation requires a kind run; the image-level assertion (regular file) plus a single-pod start check is sufficient signal. The full k8s-tests suite may surface unrelated environment issues (e.g. the previously-seen mariadb PV corruption) that are out of scope here.

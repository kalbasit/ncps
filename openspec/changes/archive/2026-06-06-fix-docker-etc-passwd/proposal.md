## Why

The ncps container image ships `/etc/passwd` and `/etc/group` as **absolute symlinks into the nix store** (verified on the built image: `etc/passwd -> /nix/store/…-passwd/etc/passwd`). Recent container runtimes do a securejoin/`openat` on `etc/passwd` while creating the container and **reject an absolute symlink as escaping the rootfs**, so the container never starts:

```text
failed to create containerd container: mount callback failed on …:
openat etc/passwd: path escapes from parent
```

This reproduces on `kindest/node:v1.35.0` and breaks **every** ncps pod — which means it is a latent **production** failure on any k8s/containerd of similar vintage, not just a test problem. It currently blocks the entire `k8s-tests` Kind suite (no ncps pod can start).

## What Changes

- Change `nix/packages/docker.nix` so `/etc/passwd` and `/etc/group` are **regular files** in the image rootfs instead of store symlinks. Move them out of the `buildEnv` `contents` (which symlinks them) and materialize them as real files via `dockerTools.buildLayeredImage`'s `fakeRootCommands` (which can rewrite the read-only store-symlinked `/etc` of the customisation layer and write the files directly). Keep identical content (`root` + `ncps` uid/gid 1000) and keep the `disallowedRequisites` guard.
- Add a packaging regression check asserting `/etc/passwd` and `/etc/group` are regular files (not symlinks) in the built image, with the expected entries.

No application code, DB, or API changes.

## Capabilities

### New Capabilities
- `container-image-etc-files`: The ncps OCI image MUST provide `/etc/passwd` and `/etc/group` as regular files (not store symlinks) so containers start on container runtimes that strictly validate symlinks during creation.

### Modified Capabilities
<!-- none -->

## Impact

- **Code**: `nix/packages/docker.nix` only; plus a packaging assertion (nix check or test).
- **Runtime**: ncps pods start on strict-securejoin containerd (recent kind and production k8s). Unblocks the `k8s-tests` Kind suite (including the `ha-s3-postgres-cdc-lifecycle` permutation).
- **Image**: `/etc/passwd` and `/etc/group` become regular files; OTEL process detection and uid/gid behavior are unchanged. Other store symlinks (e.g. `/etc/ssl`) are untouched — they are read by the app at runtime, not by the runtime during container creation, so they do not trip the check.
- **I/O / network / memory**: none.

## Non-goals

- Not changing the k8s securityContext defaults (`container-defaults-security-context`).
- Not converting other `/etc` store symlinks (`/etc/ssl`, `/etc/mtab`, …) to regular files — only the create-time-critical `passwd`/`group`.
- Not pinning a different `kindest/node` version; the fix is in the image so it works everywhere.

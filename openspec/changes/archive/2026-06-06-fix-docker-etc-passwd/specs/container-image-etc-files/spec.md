## ADDED Requirements

### Requirement: Image MUST ship /etc/passwd and /etc/group as regular files

The ncps OCI image MUST provide `/etc/passwd` and `/etc/group` as regular files in the image rootfs. They MUST NOT be symlinks (in particular, MUST NOT be absolute symlinks into the nix store). This ensures the container starts on container runtimes that perform a strict securejoin/`openat` of `etc/passwd` during container creation and reject absolute symlinks as escaping the rootfs.

The files MUST contain the existing identities: a `root` entry (uid/gid 0) and an `ncps` entry (uid/gid 1000). The image's no-shell / no-coreutils closure guarantees (`disallowedRequisites`) MUST continue to hold.

#### Scenario: /etc/passwd is a regular file in the built image

- **WHEN** the image is built and its rootfs is inspected
- **THEN** `/etc/passwd` is a regular file (not a symlink) containing the `root` and `ncps` (uid 1000) entries

#### Scenario: /etc/group is a regular file in the built image

- **WHEN** the image is built and its rootfs is inspected
- **THEN** `/etc/group` is a regular file (not a symlink) containing the `root` and `ncps` (gid 1000) entries

#### Scenario: Container starts on a strict-securejoin runtime

- **WHEN** the image is run on a container runtime that securejoins `etc/passwd` during container creation (e.g. recent containerd on `kindest/node`)
- **THEN** container creation succeeds (no `openat etc/passwd: path escapes from parent` error) and the process starts

#### Scenario: Closure remains shell-free and tool-free

- **WHEN** the image is built
- **THEN** the `disallowedRequisites` guard still passes (no `bash`, `coreutils`, `busybox`, etc. in the closure)

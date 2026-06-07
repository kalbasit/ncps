## 1. Reproduce (red)

- [x] 1.1 Build the current image and confirm `/etc/passwd` + `/etc/group` are absolute store symlinks in the exported rootfs (documents the bug)
- [x] 1.2 Add a regression assertion (nix check / script) that fails when `/etc/passwd` or `/etc/group` is a symlink in the built image — confirm it FAILS against the current image

## 2. Fix (green)

- [x] 2.1 In `nix/packages/docker.nix`, remove the `etc-passwd`/`etc-group` `writeTextFile` entries from the `buildEnv` `contents`
- [x] 2.2 Materialize `/etc/passwd` and `/etc/group` as regular files via `dockerTools.buildLayeredImage` `extraCommands` (here-doc; create `etc/` first), keeping identical content (`root` + `ncps` uid/gid 1000)
- [x] 2.3 Rebuild the image; confirm the regression assertion now PASSES (both are regular files with the expected entries)
- [x] 2.4 Confirm the `disallowedRequisites` guard still holds (no shell/coreutils in closure) and the image still contains cacert + tzdata + ncps

## 3. Validate startup

- [x] 3.1 Before/after on a Kind cluster: OLD image (symlinked /etc/passwd) → `CreateContainerError` with `openat etc/passwd: path escapes from parent`; FIXED image → pod `Completed` (`/bin/ncps --help` ran). The fix resolves the exact blocker.
- [x] 3.2 Validated equivalently via the minimal before/after pod test (3.1). The full `k8s-tests` lifecycle permutation is now unblocked (pods start with the fixed image); a full-suite re-run can confirm end-to-end (tracked under the cdc-lifecycle-e2e-tests change's 5.4/6.x).

## 4. Verify and finalize

- [x] 4.1 `task fmt`/`task lint` clean; the new `checks.docker-image-etc-files` regression check builds GREEN (asserts /etc/passwd + /etc/group are regular files).
- [ ] 4.2 `openspec validate` the change; sync the delta spec; archive before merge

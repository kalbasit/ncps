## 1. Verifier, test-first (one scenario per cycle)

- [x] 1.1 Create `.github/scripts/release-tag-guard-test.sh` with the fixture-repo helper and the "changelog-only release commit passes" case; verify it fails (guard script missing), then add a minimal `.github/scripts/release-tag-guard.sh` and verify the case passes
- [x] 1.2 Add the "commit touching other files fails" and "code commit without changelog change fails" cases; verify red, implement the changed-files check (printing offending paths), verify green
- [x] 1.3 Add the "heading for a different version fails" and "heading already existed in parent fails" cases; verify red, implement the added-heading check, verify green
- [x] 1.4 Add the "merge commit fails" and "root commit fails" cases; verify red, implement the single-parent check, verify green
- [x] 1.5 Add the "rc tag on code commit passes" and "malformed tag fails" (`v1.2`, `v1.2.3-hotfix`) cases; verify red, implement tag classification, verify green
- [x] 1.6 Mutation sanity check: temporarily make the changed-files check always pass, confirm the suite fails, then restore and confirm green

## 2. Flake check

- [x] 2.1 Add `release-tag-guard-check` to `nix/checks/flake-module.nix` (narrow `src`, `bash`/`git`/`shellcheck`, `HOME=$TMPDIR`, shellcheck + suite) with a comment naming the asserted property; verify `nix build .#checks.x86_64-linux.release-tag-guard-check` succeeds and that it appears in `nix eval .#checks.x86_64-linux --apply builtins.attrNames --json`

## 3. Workflow wiring

- [x] 3.1 Add the `guard` job to `.github/workflows/releases.yml` (pinned checkout with `fetch-depth: 2`, `permissions: contents: read`, runs the script with `$GITHUB_REF_NAME`) and add `needs: guard` to `build`; verify with `actionlint` (if available in the dev shell) or a YAML parse, and confirm `helm`/`release` still transitively depend on `guard`

## 4. Real-world validation and gates

- [x] 4.1 Run the verifier against real history: `v0.10.0` at `f043b0c9` (expect pass) and `v0.10.1` at `dd084152` (expect fail, naming the non-CHANGELOG files); record both outputs
- [x] 4.2 Run `task fmt`, `task lint`, and `task test`; verify each exits 0

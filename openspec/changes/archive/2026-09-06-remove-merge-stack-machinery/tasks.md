## 1. Pre-flight verification

- [x] 1.1 Re-confirm both workflows are inert: `gh pr list --state merged --limit 30 --json number,labels --jq '[.[] | select([.labels[].name] | index("merge-stack"))] | length'` returns `0`.
- [x] 1.2 Re-confirm no recent run did real work: `gh run list --workflow=merge-stack-start.yml --limit 5` and the same for `merge-stack-continue.yml` show only `skipped`.
- [x] 1.3 Confirm the blast radius across **active** files is exactly two: `grep -rIn --exclude-dir=.git --exclude-dir=archive -e 'merge-stack' -e 'stackmerge' .` matches only `.github/workflows/merge-stack-start.yml` and `.github/workflows/merge-stack-continue.yml`. The archive is excluded deliberately: archived records quote these strings as history and are not live references.

## 2. Removal

- [x] 2.1 Delete `.github/workflows/merge-stack-start.yml`.
- [x] 2.2 Delete `.github/workflows/merge-stack-continue.yml`.
- [x] 2.3 Re-run the archive-excluded grep from 1.3 and confirm zero matches remain among active files.

## 3. Guard the required-check graph

- [x] 3.1 Confirm neither deleted workflow appears in any surviving workflow's `needs:` or `uses:` — `grep -rIn -e 'merge-stack' .github/` returns nothing.
- [x] 3.2 Confirm `ci.yml`'s `ci` gate job still lists exactly `shared`, `filter`, `generate-database`, `deploy-docs-pages` in `needs:` (unchanged by this work).
- [x] 3.3 Confirm `secrets.GHA_PAT_TOKEN` is still consumed by at least one surviving workflow, so the secret is not orphaned.

## 4. Verification

- [x] 4.1 Run `task fmt` and confirm exit status 0.
- [x] 4.2 Run `task lint` and confirm exit status 0.
- [x] 4.3 Run `task test` and confirm exit status 0.
- [x] 4.4 On the pull request, confirm the `ci` required check reports and is green, and that no `Merge Stack` workflow is dispatched. Verified on PR #1496: every check reported `SUCCESS`/`SKIPPED`, and the workflows dispatched on the branch were exactly `["CI", "DevSkim", "Lint PR"]`.

## 5. Close out

- [x] 5.1 Archive this change (`/opsx:archive`) so `openspec-guard` passes and the pull request can merge.
- [x] 5.2 Report the `monitor-stack-merge` follow-up to the operator: the skill still instructed adding the `merge-stack` label and polling `merge-stack-continue.yml`. Its source is `modules/agent/skills/monitor-stack-merge.nix` in kalbasit/soxincfg (surfaced at `~/.claude/skills/monitor-stack-merge/SKILL.md` via home-manager). Handed off in kalbasit/soxincfg#44, which retargets it at `gh stack merge`. Out of scope here — it lives outside this repository.

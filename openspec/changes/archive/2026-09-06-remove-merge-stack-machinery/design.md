## Context

See `proposal.md` — Why. Two workflows are being deleted:

| File | Trigger | Guard | Action |
|---|---|---|---|
| `merge-stack-start.yml` | `pull_request: types: [labeled]` | `label.name == 'merge-stack'`, `sender.type == 'User'`, non-fork | `kalbasit/stackmerge-action/start@main` |
| `merge-stack-continue.yml` | `pull_request: types: [closed]` | `merged == true`, has `merge-stack` label, non-fork | `kalbasit/stackmerge-action/continue@main` |

Both use `secrets.GHA_PAT_TOKEN`, which several other workflows also use, so no secret becomes orphaned.

Constraint that shapes the approach: `openspec-guard` aggregates into the `ci` gate, so this change must be archived before the pull request can merge. Constraint the removal must respect: neither workflow is referenced by `ci.yml`'s `needs:` list, so deleting them cannot break the required-check graph.

## Goals / Non-Goals

**Goals:**

- Delete both workflow files in a single commit that is trivially revertible.
- Leave every remaining workflow's trigger surface and the `ci` required-check set byte-identical.
- Record the native-stacks replacement so a future reader does not reintroduce the automation.

**Non-Goals:**

- Verifying that native stacked PRs keep working. That is already established by stack 1494 and is not re-litigated here.
- Any edit to `ci.yml`, `devskim.yml`, or `semantic-pull-request.yml`.
- Touching `~/.claude/skills/monitor-stack-merge/SKILL.md` (see Risks).

## Decisions

**Delete outright rather than disable.** Alternatives considered: (a) add `if: false`, (b) narrow the trigger to a branch that never exists, (c) delete. Options (a) and (b) leave a dispatched-but-skipped workflow run on every `labeled` and `closed` event and preserve a live reference to a third-party action pinned at `@main` — a supply-chain surface with no upside. Deletion is one `git revert` away from restoration, so the reversibility argument for keeping a disabled stub does not hold.

**No deprecation window.** The workflows are already inert in practice (0 of the last 30 merged PRs used the label; all recent runs `skipped`), so a window would only delay the removal without de-risking it.

**Leave the `merge-stack` label in place.** Deleting it is a repository-settings mutation, not a repository-content change, and it cannot be reverted by reverting this commit. An inert label is harmless. Deleting the label is left as an explicit operator decision.

**No test changes.** The project mandates TDD for production code changes. This change deletes two CI workflow files and adds no code path, so there is no unit under test; the verification is the CI run on the pull request itself plus the greps in `tasks.md`. This is a deliberate, scoped exemption, not an oversight.

## Risks / Trade-offs

**The `monitor-stack-merge` skill breaks silently** → It is built entirely around these workflows: it adds the `merge-stack` label to the top PR, polls `merge-stack-continue.yml` via `gh run list --workflow=merge-stack-continue.yml`, and instructs the operator not to run `gs ss` because the workflow submits the next PR. After this change those instructions describe workflows that no longer exist, and the polling command returns nothing rather than failing loudly. The skill lives in the user's global Claude configuration, outside this repository, so it cannot be fixed in this pull request. Mitigation: flag it to the operator on merge and rewrite it against native stacks as an immediate follow-up.

**Native stacked PRs are still in public preview** → If GitHub withdraws or degrades the feature, the repository loses automated stack merging and falls back to manual bottom-up merges. Mitigation: `git revert` restores both files verbatim; the `merge-stack` label is deliberately retained so a revert is immediately functional.

**Loss of the audit trail for why the automation existed** → Mitigation: the archived change under `openspec/changes/archive/` preserves the rationale and the evidence.

## Migration Plan

1. Delete both files, commit, open the pull request.
2. Confirm CI is green and that the `ci` required check still reports.
3. Merge. No deploy step: workflow files take effect on the default branch immediately.

Rollback: `git revert <sha>`. The `merge-stack` label still exists, so the automation resumes on the next labeled PR with no further action.

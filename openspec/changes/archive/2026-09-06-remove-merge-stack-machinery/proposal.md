## Why

GitHub's native stacked pull requests replaced the repository's bespoke `merge-stack` automation. Stack 1494's six pull requests each ran the full CI matrix against non-`main` bases and then merged atomically within three seconds, with no involvement from the `merge-stack` path at all. The two workflows that drive that path are now dead code that still listens on every `pull_request` event.

## What Changes

- Delete `.github/workflows/merge-stack-start.yml` — the `labeled` trigger that called `kalbasit/stackmerge-action/start`.
- Delete `.github/workflows/merge-stack-continue.yml` — the `closed` trigger that called `kalbasit/stackmerge-action/continue`.
- Drop the repository's dependency on `kalbasit/stackmerge-action` (no other file references it).
- The `merge-stack` GitHub label becomes inert. Deleting the label itself is a repository-settings action outside this change.

Not **BREAKING** for any consumer of ncps: neither workflow ships in a release artifact, affects the binary, or is referenced by another workflow.

Evidence these are dead:

- Zero of the last 30 merged pull requests carried the `merge-stack` label.
- Every recent run of both workflows concluded `skipped`.
- `grep -rIn 'merge-stack\|stackmerge'` matches only the two files being deleted.

## Capabilities

### New Capabilities

None. This is a CI tooling removal with no runtime behavior change, so `.openspec.yaml` sets `skip_specs: true`.

### Modified Capabilities

None. No requirement in `openspec/specs/` describes the `merge-stack` automation.

## Non-goals

- Rewriting `~/.claude/skills/monitor-stack-merge/SKILL.md`. That skill is built entirely around these workflows and the `merge-stack` label, but it lives in the user's global Claude configuration, outside this repository. It is a tracked follow-up, not in-scope work.
- Deleting the `merge-stack` label from repository settings.
- Any CI cost tuning for stacked pull requests (for example gating expensive cohorts on `github.event.pull_request.stack.position == github.event.pull_request.stack.size`). That is a separate change.
- Changing `ci.yml`, `devskim.yml`, or `semantic-pull-request.yml`. Their `branches: [main]` filters are correct as written — GitHub resolves them against the stack base.

## Impact

- **Affected code**: two files under `.github/workflows/`. No Go source, no Nix derivation, no chart.
- **I/O, network latency, memory**: none. No runtime code path changes; the ncps binary is byte-identical.
- **CI**: two fewer workflow runs dispatched per labeled/closed pull request event. No required check is removed, because neither workflow feeds the `ci` gate.
- **Dependencies**: removes the last use of the third-party `kalbasit/stackmerge-action`.
- **Operational risk**: if native stacked pull requests were ever disabled for this repository, stack merges would fall back to manual bottom-up merging. Restoring the workflows means reverting this commit.

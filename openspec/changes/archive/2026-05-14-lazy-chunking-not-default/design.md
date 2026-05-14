## Context

PR #1099 set `cache-cdc-lazy-chunking-enabled` default to `true` and mirrored that in the Helm chart. This silently activates lazy chunking for all CDC-enabled deployments on upgrade, starting background workers and a cleanup cron job without user consent.

Current state: two files have diverging defaults (`Value: false` already partially reverted in the working tree), one usage string is stale, and the Helm chart test asserts the wrong default.

## Goals / Non-Goals

**Goals:**
- `--cache-cdc-lazy-chunking-enabled` defaults to `false` (opt-in)
- Helm `values.yaml` defaults `lazyChunkingEnabled: false`
- Usage string and Helm test reflect the correct default
- Docs/chart-reference corrected

**Non-Goals:**
- No functional change to lazy chunking behavior when enabled
- No removal of the feature or related flags

## Decisions

**Revert the default, keep the feature.** Lazy chunking is a valid performance optimization, but it changes operational behavior (extra workers, delayed file deletion, extra cron job). Defaults should not change silently across upgrades; users must explicitly opt in.

**No deprecation path.** Users who upgraded after #1099 and relied on the implicit `true` default should re-check their config. This is unavoidable given the short window between #1099 and this revert.

## Risks / Trade-offs

- [Risk] Users who upgraded between #1099 and this revert and never set the flag explicitly will lose lazy chunking on next upgrade → Mitigation: release notes should call this out explicitly

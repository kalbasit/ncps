## Context

The chart renders the server config file from `config.*` values. Sibling timeouts such as
`config.cdc.chunkWaitTimeout` are nullable and omitted when null.

## Goals / Non-Goals

**Goals:** expose the flag through the chart with the same null-means-default semantics.

**Non-Goals:** changing the default or the server.

## Decisions

- Guard on null with `kindIs "invalid"` rather than truthiness, so an explicit `"0"` (disable the
  bound) is rendered rather than dropped. This is the same rule the chart's job helper follows.
- Render the value as a quoted string (`toString | quote`), so it reaches the server's duration parser
  as `"15s"`/`"0"`. The schema accepts a string, `null`, or exactly the number `0` (so an unquoted
  `statTimeout: 0` works). Any other bare number is rejected, because it would render as a unitless
  duration such as `"5"`, which the server cannot parse.
- Place it under `config.storage`, mirroring the config key `cache.storage.stat-timeout`.

## Risks / Trade-offs

- None beyond the usual chart surface growth; defaults render exactly as before.

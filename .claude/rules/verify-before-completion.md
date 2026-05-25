# Verify Before Reporting Completion

Before reporting that any task is complete, you MUST run both of the following commands and confirm each exits with zero status:

1. `task fmt` — all code must be correctly formatted
1. `task lint` — all code must be correctly linted
1. `task test` — all tests must pass

## Rules

- Never report a task as complete until both commands have been run and succeeded.
- If `task fmt` fails or produces changes, apply the formatting and re-run to confirm it exits cleanly.
- If `task lint` fails or produces changes, apply the linting and re-run to confirm it exits cleanly.
- If `task test` fails, fix the failing tests before reporting completion.
- All commands must exit with status `0`. Any non-zero exit is a blocker.

## Applies To

All tasks that involve production code changes: new features, bug fixes, refactors, and configuration changes.

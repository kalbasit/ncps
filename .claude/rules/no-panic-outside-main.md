# No Panics Outside the Main Package

Production code in non-`main` packages MUST NOT call `panic()`. Return an `error` and let the caller decide how to handle it.

## Rules

- Never call `panic()` in any package other than `main`.
- Never use `log.Fatal*` or `os.Exit` outside `main` either — they bypass error handling just like `panic`.
- If an error is genuinely unrecoverable, return it up the stack. The `main` package is the only place allowed to terminate the process.
- If a function signature cannot return an error (e.g. an `ent` `DefaultFunc` that must return a single value), restructure the code so the failure case cannot occur (e.g. build the value directly from inputs that cannot fail). Do not reach for `panic` as an escape hatch.
- If a legitimate invariant check is needed (e.g. guarding against a programmer bug), prefer returning a sentinel error. Only if there is truly no other option, use `panic` with a `//nolint:forbidigo // reason` comment explaining why no alternative exists.

## Exceptions

- `_test.go` files may panic (e.g. in mock stubs for unused interface methods).
- Generated code (e.g. files under `apps/uar/ent/<db>/` that are produced by `ent generate`) is exempt — do not hand-edit generated files to remove panics.
- Hand-written schema files under `ent/<db>/schema/` are NOT generated code and MUST follow this rule.

## Applies To

All hand-written production Go code in non-`main` packages.

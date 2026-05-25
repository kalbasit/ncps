# Nolint Directives Require Comments

All `//nolint:{rule}` directives used to silence legitimate linter false positives must be accompanied by an explanatory comment.

## Format

```go
//nolint:{rule} // comment explaining why this false positive is legitimate
```

## Rules

- Every `//nolint` directive must have a trailing comment explaining the reason.
- The comment should be concise but clear enough for future readers to understand why the linter rule was suppressed.
- Comments must appear on the same line as the `//nolint` directive.
- Do not use `//nolint` to silence genuine issues — fix the underlying problem instead.

## Examples

```go
//nolint:errcheck // error is logged by defer handler, safe to ignore
result := someFunc() // nolint:errcheck would be wrong here

//nolint:gosec // only validating example input, not processing user data
testData := []byte("example")
```

## Applies To

All production code: Go source files, configuration files, and any other code where linter suppressions are used.

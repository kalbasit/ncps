---
description: Address unresolved comments in a PR
---

# Address Unresolved PR Comments

This workflow guides you through fetching unresolved comments from a GitHub Pull Request and addressing them systematically.

## Pre-requisites

- `gh` CLI installed and authenticated.
- `jq` installed for JSON processing.
- Access to the helper script `dev-scripts/get-unresolved-comments.sh`.

## Workflow Steps

### 1. Fetch Unresolved Comments

Use the helper script to fetch the unresolved comments. If no PR number is provided, it will attempt to find the PR for the current branch.

```bash
# Fetch comments for the current PR
./dev-scripts/get-unresolved-comments.sh

# Or specify a PR number
./dev-scripts/get-unresolved-comments.sh <PR_NUMBER>
```

The script outputs a JSON array of comments. Each comment includes the `body` (feedback), `path` (file), `line`, and `threadId` (required for resolution).

### 2. Address Each Comment

Iterate through the unresolved comments and perform the following for each:

1. **Locate the issue**: Use `view_file` to examine the file and specific line mentioned in the comment.
2. **Analyze and fix**: Understand the feedback and implement the necessary changes.
3. **Verify**: Run relevant tests or linters (e.g., using `/test` or `/lint` workflows) to ensure the fix is correct and doesn't introduce regressions.
4. **Commit**: Once the fix is verified, commit the change using a descriptive message.
5. **Resolve on GitHub**: Use the `threadId` provided in the fetch step to resolve the thread on GitHub.

```bash
git commit -am "fix: address PR comment regarding <context>"
./dev-scripts/resolve-pr-comment.sh <THREAD_ID>
```

### 3. Final Review

After addressing and resolving all comments, perform a final check of the changes and ensure the project still builds and tests pass.

## Internal details

The script `dev-scripts/get-unresolved-comments.sh` uses `gh api graphql` to fetch `reviewThreads` where `isResolved` is false.
The script `dev-scripts/resolve-pr-comment.sh` uses the `resolveReviewThread` GraphQL mutation to mark a thread as resolved.
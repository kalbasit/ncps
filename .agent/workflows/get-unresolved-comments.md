---
description: Get unresolved comments in a PR
---

# Fetch Unresolved PR Comments

This workflow provides a way to fetch only the unresolved comments from a GitHub Pull Request using the `gh` CLI and a helper script.

## Pre-requisites

- `gh` CLI installed and authenticated.
- `jq` installed for JSON processing.

## Running the script

Use the helper script `dev-scripts/get-unresolved-comments.sh` to fetch unresolved comments:

```bash
# Example: Fetch unresolved comments for PR 517
./dev-scripts/get-unresolved-comments.sh 517
```

The script will output a JSON array of unresolved comments.

## Internal details

The script uses `gh api graphql` to fetch `reviewThreads` where `isResolved` is false. It uses `mktemp -d` and `trap` internally to handle temporary files securely outside the project directory.

## Example: Processing the output

If you need to save the output to a temporary file for further processing while following security best practices:

```bash
# Create a secure temporary directory
TMP_DIR=$(mktemp -d)
# Ensure it is removed on exit
trap 'rm -rf "$TMP_DIR"' EXIT

# Fetch comments to a temporary file
./dev-scripts/get-unresolved-comments.sh 517 > "$TMP_DIR/unresolved.json"

# Process the file
jq '.' "$TMP_DIR/unresolved.json"
```

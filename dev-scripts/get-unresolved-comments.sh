#!/bin/bash
set -eo pipefail

# get-unresolved-comments.sh: Fetches unresolved PR comments using GitHub GraphQL API.
# Usage: ./get-unresolved-comments.sh <pr-number>

PR_NUMBER=$1
if [ -z "$PR_NUMBER" ]; then
  echo "Usage: $0 <pr-number>" >&2
  exit 1
fi

# Infer repo owner and name
REPO_INFO=$(gh repo view --json owner,name)
OWNER=$(echo "$REPO_INFO" | jq -r .owner.login)
NAME=$(echo "$REPO_INFO" | jq -r .name)

# Create a secure temporary directory
TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT

# GraphQL query to fetch unresolved threads
QUERY='
query($owner: String!, $name: String!, $pr: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $pr) {
      reviewThreads(first: 100) {
        nodes {
          isResolved
          comments(first: 100) {
            nodes {
              body
              path
              line
              author {
                login
              }
            }
          }
        }
      }
    }
  }
}'

# Save result to a temp file in the secure location
RESULT_FILE="$TMP_DIR/result.json"

gh api graphql -F owner="$OWNER" -F name="$NAME" -F pr="$PR_NUMBER" -f query="$QUERY" > "$RESULT_FILE"

# Filter and output only unresolved comments
jq '.data.repository.pullRequest.reviewThreads.nodes[] | select(.isResolved == false) | .comments.nodes[]' "$RESULT_FILE"

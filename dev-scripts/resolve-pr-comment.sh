#!/bin/bash
set -eo pipefail

# resolve-pr-comment.sh: Resolves a GitHub PR review thread.
# Usage: ./resolve-pr-comment.sh <thread-id>

THREAD_ID=$1
if [ -z "$THREAD_ID" ]; then
  echo "Usage: $0 <thread-id>" >&2
  exit 1
fi

QUERY='
mutation($threadId: ID!) {
  resolveReviewThread(input: {threadId: $threadId}) {
    thread {
      isResolved
    }
  }
}'

gh api graphql -f query="$QUERY" -F threadId="$THREAD_ID"

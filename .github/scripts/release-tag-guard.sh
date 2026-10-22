#!/usr/bin/env bash
# Verifies that a release tag points at a changelog-only release commit before
# the release workflow publishes anything. See the release-tag-guard spec.
#
# Usage: release-tag-guard.sh <tag> [<commit>]   (<commit> defaults to HEAD)

set -euo pipefail

tag=${1:?usage: $0 <tag> [<commit>]}
commit=${2:-HEAD}

fail() {
  echo "release-tag-guard: $tag: $*" >&2
  echo "release-tag-guard: tag the squash-merged 'docs(changelog): release $tag' commit instead" >&2
  exit 1
}

# Pre-releases are exempt, using the same alpha/beta/rc classification the
# release workflow applies. Anything that is neither a stable vX.Y.Z tag nor
# such a pre-release is rejected rather than silently exempted.
if [[ $tag =~ ^v[0-9]+\.[0-9]+\.[0-9]+-(alpha|beta|rc) ]]; then
  echo "release-tag-guard: $tag is a pre-release; skipping the changelog check"
  exit 0
fi

if ! [[ $tag =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  fail "not a recognised release tag (expected vX.Y.Z, or vX.Y.Z-alpha*/-beta*/-rc*)"
fi

# A squash-merged release PR has exactly one parent; diffing a merge commit
# against one parent would hide what the other side brought in.
read -r -a revs <<<"$(git rev-list --parents -n 1 "$commit")"
if [ "${#revs[@]}" -ne 2 ]; then
  fail "a single-parent commit is required, but $commit has $((${#revs[@]} - 1)) parent(s)"
fi

# The release commit cuts the version in CHANGELOG.md and changes nothing else.
changed=$(git diff --name-only "$commit^" "$commit")
if [ "$changed" != "CHANGELOG.md" ]; then
  fail "a release commit must change only CHANGELOG.md, but $commit changes:" \
    "$(tr '\n' ' ' <<<"${changed:-<nothing>}")"
fi

# The commit must ADD the version heading, not merely carry it: that pins the
# tag to the one commit that cut the release. The diff is captured first:
# piping it into `grep -q` would let grep's early exit SIGPIPE git, which
# pipefail reports as a failure on large diffs.
version=${tag#v}
diff=$(git diff -U0 "$commit^" "$commit" -- CHANGELOG.md)
if ! grep -qE "^\+## \[${version//./\\.}\]( |\$)" <<<"$diff"; then
  fail "$commit does not add a '## [$version]' heading to CHANGELOG.md"
fi

echo "release-tag-guard: $tag at $(git rev-parse --short "$commit") is a changelog-only release commit"

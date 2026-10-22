#!/usr/bin/env bash
# Hermetic test suite for release-tag-guard.sh. Each case builds a throwaway
# git repository, runs the guard against it and compares the verdict (and, for
# failures, a distinctive message) with the expected one.
#
# Usage: release-tag-guard-test.sh <path-to-release-tag-guard.sh>

set -euo pipefail

guard=$(realpath "${1:?usage: $0 <path-to-release-tag-guard.sh>}")

# Commit identity and config come from the environment so the suite never
# reads or writes the caller's git configuration.
export GIT_AUTHOR_NAME=test GIT_AUTHOR_EMAIL=test@example.com
export GIT_COMMITTER_NAME=test GIT_COMMITTER_EMAIL=test@example.com
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1

workdir=$(mktemp -d)
trap 'rm -rf "$workdir"' EXIT

failures=0

# new_repo creates a repository whose single commit holds a CHANGELOG with an
# [Unreleased] section and a previous release, plus a source file, and prints
# its path.
new_repo() {
  local dir
  dir=$(mktemp -d "$workdir/repo.XXXXXX")
  git -C "$dir" init -q -b main
  printf '# Changelog\n\n## [Unreleased]\n\n### Fixed\n\n- something\n\n## [1.2.2] - 2026-01-01\n' >"$dir/CHANGELOG.md"
  echo 'package main' >"$dir/main.go"
  commit "$dir" base
  echo "$dir"
}

# add_heading inserts a "## [<version>] - <date>" heading under [Unreleased],
# the way a release commit cuts a version.
add_heading() {
  sed -i "s/^## \[Unreleased\]\$/## [Unreleased]\n\n## [$2] - 2026-02-02/" "$1/CHANGELOG.md"
}

commit() {
  git -C "$1" add -A
  git -C "$1" commit -q -m "$2"
}

# expect <name> <pass|fail> <needle> <repo> <guard args...>
# A failing case must also print <needle>, so it cannot pass by failing for an
# unrelated reason. An empty needle skips the message check.
expect() {
  local name=$1 want=$2 needle=$3 dir=$4
  shift 4

  local out status=0 got=pass
  out=$(cd "$dir" && bash "$guard" "$@" 2>&1) || status=$?
  [ "$status" -eq 0 ] || got=fail

  if [ "$got" = "$want" ] && { [ -z "$needle" ] || grep -qF -- "$needle" <<<"$out"; }; then
    echo "ok   $name"
  else
    echo "FAIL $name (want $want, got $got${needle:+, expected output to contain: $needle})"
    while IFS= read -r line; do echo "     | $line"; done <<<"$out"
    failures=$((failures + 1))
  fi
}

# --- Stable tags -------------------------------------------------------------

r=$(new_repo)
add_heading "$r" 1.2.3
commit "$r" "docs(changelog): release v1.2.3"
expect "changelog-only release commit passes" pass "" "$r" v1.2.3

# The heading is found early in a diff far larger than a pipe buffer, so a
# reader that stops at the first match must not turn the writer's SIGPIPE into
# a failure.
r=$(new_repo)
add_heading "$r" 1.2.3
for i in $(seq 1 20000); do echo "- entry $i"; done >>"$r/CHANGELOG.md"
commit "$r" "docs(changelog): release v1.2.3 with a large reorganisation"
expect "release commit with a large changelog diff passes" pass "" "$r" v1.2.3

r=$(new_repo)
add_heading "$r" 1.2.3
echo 'package main // changed' >"$r/main.go"
commit "$r" "release plus a code change"
expect "commit touching other files fails" fail "main.go" "$r" v1.2.3

r=$(new_repo)
echo 'package main // fix' >"$r/main.go"
commit "$r" "fix: a code commit"
expect "code commit without a changelog change fails" fail "only CHANGELOG.md" "$r" v1.2.3

r=$(new_repo)
add_heading "$r" 1.2.4
commit "$r" "docs(changelog): release v1.2.4"
expect "heading for a different version fails" fail "## [1.2.3]" "$r" v1.2.3

r=$(new_repo)
add_heading "$r" 1.2.30
commit "$r" "docs(changelog): release v1.2.30"
expect "heading for a version sharing the tag's prefix fails" fail "## [1.2.3]" "$r" v1.2.3

r=$(new_repo)
add_heading "$r" 1.2.3
commit "$r" "docs(changelog): release v1.2.3"
echo '- a late entry' >>"$r/CHANGELOG.md"
commit "$r" "docs(changelog): touch up after the release"
expect "heading already existed in the parent fails" fail "## [1.2.3]" "$r" v1.2.3

# A --no-ff merge of a changelog-only branch: diffed against its first parent
# it looks like a release commit, so only the parent count catches it.
r=$(new_repo)
git -C "$r" checkout -q -b release
add_heading "$r" 1.2.3
commit "$r" "docs(changelog): release v1.2.3"
git -C "$r" checkout -q main
git -C "$r" merge -q --no-ff -m "Merge branch 'release'" release
expect "merge commit fails" fail "single-parent" "$r" v1.2.3

r=$(mktemp -d "$workdir/repo.XXXXXX")
git -C "$r" init -q -b main
printf '# Changelog\n\n## [Unreleased]\n\n## [1.2.3] - 2026-02-02\n' >"$r/CHANGELOG.md"
commit "$r" "initial commit"
expect "root commit fails" fail "single-parent" "$r" v1.2.3

# --- Tag classification ------------------------------------------------------

r=$(new_repo)
echo 'package main // feature' >"$r/main.go"
commit "$r" "feat: a code commit"
expect "rc tag on a code commit passes" pass "pre-release" "$r" v1.2.3-rc1
expect "beta tag on a code commit passes" pass "pre-release" "$r" v1.2.3-beta.2
expect "alpha tag on a code commit passes" pass "pre-release" "$r" v1.2.3-alpha
expect "two-component tag fails" fail "not a recognised release tag" "$r" v1.2
expect "unknown suffix tag fails" fail "not a recognised release tag" "$r" v1.2.3-hotfix

# --- Summary -----------------------------------------------------------------

if [ "$failures" -ne 0 ]; then
  echo "$failures case(s) failed"
  exit 1
fi

echo "all cases passed"

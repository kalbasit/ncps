#!/usr/bin/env bash

error() {
  echo -e "\033[0;31m$@\033[0m"
}

fatal() {
  error "$@"
  exit 1
}

info() {
  echo -e "\033[0;33m$@\033[0m"
}

if [[ -d .git ]]; then
  error "This must only run from within a new Git worktree"
  exit 0
fi

readonly main_worktree_path="$(git worktree list --porcelain | head -n 1 | cut -d ' ' -f 2-)"

info Allow direnv to load this path
direnv allow .

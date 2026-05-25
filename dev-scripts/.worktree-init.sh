#!/usr/bin/env bash

set -euo pipefail

error() {
  printf "\033[0;31m%s\033[0m\n" "$*"
}

fatal() {
  error "$@"
  exit 1
}

info() {
  printf "\033[0;33m%s\033[0m\n" "$*"
}

if [[ -d .git ]]; then
  fatal "This must only run from within a new Git worktree"
fi

info "Allow direnv to load this path"
direnv allow .

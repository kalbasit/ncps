#!/usr/bin/env bash

set -euo pipefail

# $PWD is the new worktree (set by swm). The script is checked out in every
# worktree, so call it directly without cding away from the worktree.
exec ./dev-scripts/worktree-init.sh

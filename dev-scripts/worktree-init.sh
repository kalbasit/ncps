#!/usr/bin/env bash

cd -- "$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"

nix develop --command ./dev-scripts/.worktree-init.sh

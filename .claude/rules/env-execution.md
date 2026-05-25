# Environment Execution (Nix & direnv)

This repository uses Nix and `direnv` for ALL tooling. In non-interactive shells (daemon, ACP, CI), direnv hooks do NOT run automatically — you cannot assume the environment is already loaded.

## Mandatory bootstrap — no exceptions

Before running ANY command that is not a plain `git` operation, you MUST run these two lines in order, in the same shell session:

```sh
direnv status | grep -q "Found RC allowed 0" || direnv allow .
unset DIRENV_DIR DIRENV_FILE DIRENV_WATCHES DIRENV_DIFF && eval "$(direnv export bash)"
```

- Line 1: only runs `direnv allow .` when the RC is not already allowed (`Found RC allowed 0` means allowed). Calling `direnv allow .` unconditionally touches the allow file's mtime, which triggers unnecessary environment reloads in interactive shells.
- Line 2: **always** unsets stale `DIRENV_*` state variables first, then re-exports the full environment. This is necessary because Claude Code's tool execution shells inherit partial `DIRENV_*` vars from the parent process, causing `direnv export bash` to believe the environment is current and output nothing — leaving Nix devshell tools (e.g. `task`, `bun`, `go`) absent from `PATH`.

Do NOT skip either step. Do NOT assume direnv is already allowed or already loaded. Do NOT run them in a different order.

## What counts as "any command"

This includes, but is not limited to: `task`, `bun`, `go`, `nix`, `docker`, test runners, linters, formatters, and any project script. When in doubt, run the bootstrap first.

## Prohibited

- Do not install missing tools globally (e.g. `npm install -g bun`, `brew install go`). All dependencies come from the Nix flake.
- Do not use `direnv exec . <command>` as a substitute for the bootstrap — it does not export env vars into the current shell and will silently break chained commands.

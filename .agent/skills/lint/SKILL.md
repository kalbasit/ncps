---
description: Lint and format code
---

// turbo-all

1. Run standard linter (with fix enabled by default as preferred in CLAUDE.md):

```bash
nix develop --command golangci-lint run --fix
```

2. (Optional) Run linter on specific files:

```bash
nix develop --command golangci-lint run --fix $FILE
```

3. Format all files using Nix:

```bash
nix fmt
```

4. (If SQL files modified) Lint SQL files:

```bash
nix develop --command sqlfluff lint db/query.*.sql db/migrations/
```

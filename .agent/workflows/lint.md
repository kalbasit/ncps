---
description: Lint and format code
---
1. Run standard linter (with fix enabled by default as preferred in CLAUDE.md):
```bash
golangci-lint run --fix
```

2. Format all files using Nix:
```bash
nix fmt
```

3. (If SQL files modified) Lint SQL files:
```bash
sqlfluff lint db/query.*.sql db/migrations/
```

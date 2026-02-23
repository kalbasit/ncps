---
description: Run project tests
---

1. Run standard tests with race detection:

```bash
nix develop --command go test -race ./...
```

2. (Optional) Read `CLAUDE.md` section on "Integration Tests" if you need to run S3, Postgres, MySQL, or Redis tests specifically. You may need to run `nix run .#deps` and export specific environment variables.

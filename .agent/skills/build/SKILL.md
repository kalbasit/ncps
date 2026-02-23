---
description: Build the project
---

1. Build with Go:

```bash
nix develop --command go build .
```

2. Build with Nix:

```bash
nix build
```

3. (Optional) Build Docker image with Nix:

```bash
nix build .#docker
```

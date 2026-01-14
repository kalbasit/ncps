---
description: Submit the current Graphite stack in non-interactive mode
---

1. Submit the stack. If called with `-m` (e.g., `/gt-ss -m`), include the `--merge-when-ready` flag.

```bash
gt ss --no-interactive --publish $([[ "$*" == *"-m"* ]] && echo "--merge-when-ready")
```
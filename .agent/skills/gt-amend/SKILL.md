---
description: Amend the current commit with a semantic commit message
---

1. Amend the current commit. You must provide a semantic commit message (feat, fix, docs, style, refactor, test, chore) following the format `type: title`.

2. The commit message MUST include a description that explains the **why** and **how** of the change.

```bash
git commit --amend -m "<type>: <title>

<detailed description of why and how>"
```

Example:
```bash
git commit --amend -m "feat: improve performance of storage backend

By using a bunked read approach, we reduce the number of I/O operations.
This implementation caches the most frequently accessed chunks in memory...
"
```

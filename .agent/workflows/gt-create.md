---
description: Create a new Graphite stack (semantic commit)
---

1. Create a new stack using `gt create`. You must provide a semantic commit message (feat, fix, docs, style, refactor, test, chore) following the format `type: title`.

2. The commit message MUST include a description that explains the **why** and **how** of the change.

```bash
gt create -am "<type>: <title>

<detailed description of why and how>"
```

Example:
```bash
gt create -am "feat: add support for new storage backend

This adds support for S3-compatible storage backends. It was needed to
enable deployments in cloud environments without persistent local volumes.
The implementation utilizes the AWS SDK for Go and supports...
"
```

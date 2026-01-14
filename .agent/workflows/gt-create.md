---
description: Create a new Graphite stack (semantic commit)
---

1. You MUST first thing first run the `/lint` workflow.
   - If there are any linting issues, you must fix them before proceeding.
   - Only when all issues are fixed, proceed to the next step.

2. Create a new stack using `gt create`. You must provide a semantic commit message (feat, fix, docs, style, refactor, test, chore) following the format `type: title`.

3. The commit message MUST include a description that explains the **why** and **how** of the change.

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

> [!CAUTION]
> The AGENT MUST NEVER run `gt ss`. Only the USER should ever decide to run `gt ss` or the `/gt-ss` workflow.

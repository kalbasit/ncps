---
description: Restack the current Graphite stack and resolve conflicts
---

1. Start the restacking process:

```bash
gt restack
```

2. If conflicts are found:
    - Review the output to identify which commit is being rebased and the files involved.
    - Examine the conflict markers in the affected files.
    - **Generated files**: If the conflict is in `pkg/database/generated_*`, do NOT resolve manually:
        1. Restore the file from `HEAD`: `git checkout HEAD -- <file>`
        2. Run the `/generate-db-wrappers` workflow to regenerate them correctly.
    - **Other files**: Resolve the conflicts by choosing the correct changes based on the context of the PR stack.
    - Stage the resolved files:

    ```bash
    git add <resolved-files>
    ```

    - **CRITICAL**: Do NOT run `git rebase --continue`. Instead, use Graphite's continue command:

    ```bash
    gt continue
    ```

3. Repeat step 2 as necessary until the restack is complete.
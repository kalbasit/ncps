---
name: graphite
description: Navigation and restacking with Graphite (gt)
---

# Graphite (gt) Skill

This skill provides instructions for navigating branches and restacking stacks using Graphite (`gt`).

## Core Principles

1. **Strictly Prohibited**: NEVER run `gt ss`, `gt submit`, or `gt squash`. These commands are forbidden.
2. **Navigation**: Use `gt` commands to move between branches in a stack.
3. **Maintenance**: Use `gt restack` for local stack maintenance and `gt get` or `gt sync` for syncing from remote.

## Branch Navigation

Use these commands to navigate through a stack of branches:

| Command | Action |
| :--- | :--- |
| `gt up` | Move to the branch immediately above the current one in the stack. |
| `gt down` | Move to the parent of the current branch. |
| `gt prev` | Move to the previous branch in the stack, as ordered by `gt log`. |
| `gt next` | Move to the next branch in the stack, as ordered by `gt log`. |
| `gt top` | Move to the tip branch of the current stack. |
| `gt bottom` | Move to the branch closest to trunk in the current stack. |
| `gt ls` | Display all tracked branches and their dependency relationships in a short format. |

## Stack Management

Maintain your stack with these commands:

| Command | Action |
| :--- | :--- |
| `gt restack` | Ensure each branch in the current stack has its parent in its Git commit history, rebasing if necessary. This maintains the local stack and does NOT reach out to upstream. |
| `gt get` | Sync a specific branch or stack from remote (trunk to branch). If no branch is provided, it syncs the current stack. |
| `gt sync` | Sync all branches (including trunk) from remote and prompt to delete merged branches. |

## Branch Management

Use these commands to manage branches within your stack.

| Command | Action |
| :--- | :--- |
| `gt create <branch-name>` | Create a new branch with the given name and stack it on top of the current one. **Always follow the `/gt-create` workflow.** |
| `gt move` | Rebase the current branch onto a target branch and restack descendants. Use `--onto <target-branch>` to avoid interactive mode. |
| `gt split` | Split the current branch into multiple branches. This command is highly interactive. |

## Commit Management

Use `gt modify` to manage commits within your stack. It automatically restacks descendants.

| Command | Action |
| :--- | :--- |
| `gt modify --amend` | Amend the head commit of the current branch. |
| `gt modify -c` | Create a new commit on the current branch. **Preferred** for adding new changes while keeping history clean. |

## Dealing with Interactivity

Many Graphite commands (`gt modify`, `gt move`, `gt split`, `gt restack`) are interactive by default, opening selectors, editors, or specialized menus.

- **For the Agent**: Avoid using these commands in an interactive way. If a command requires user input that cannot be provided via flags (e.g., resolving complex rebase conflicts), **stop and notify the user**.
- **Rebase Conflicts**: If `gt restack` or `gt move` hits a conflict, Graphite will pause. Follow these steps to resolve:
    1. **Locate Conflicts**: Use `git status` or look at the error output to identify files with merge conflicts.
    2. **Edit Files**: Open the conflicting files and resolve the merge markers (`<<<<<<<`, `=======`, `>>>>>>>`). Ensure the code is functionally correct and includes necessary changes from both sides.
    3. **Stage Changes**: Run `gt add -A` (or `git add <file>`) to mark the conflicts as resolved.
    4. **Continue**: Run `gt continue` to resume the restack process. **IMPORTANT**: NEVER use `git rebase --continue` as Graphite tracks its own state.
    5. **Verify**: Once the restack completes, run tests to ensure the merged state is stable.
- **Interactive Selectors**: Prefer providing explicit branch names or flags (like `--onto` for `gt move`) to avoid interactive selectors.

## Common Fixes & Lessons

### Using Graphite over Git

**The Problem**: Using standard `git commit` or `git commit --amend` fails to update Graphite's internal stack metadata and does not trigger automatic restacking of descendants. This can lead to a desynchronized stack where Graphite is unaware of the new commits or changes.

**The Solution**: Always prefer `gt` commands over their standard `git` counterparts for commit and branch management.

- **Creating a new branch**: Use `gt create <branch-name>` instead of `git checkout -b <branch-name>`.
- **Creating a new commit**: Use `gt modify -c -m "message"` instead of `git commit -m "message"`.
- **Amending a commit**: Use `gt modify --amend` instead of `git commit --amend`.
- **Staging and committing everything**: Use `gt modify -cam "message"` instead of `git commit -am "message"`.

**Benefits**:
1.  **Metadata Integrity**: Graphite keeps its internal view of the stack consistent.
2.  **Automatic Restacking**: Descendant branches are automatically updated if you change a parent branch.
3.  **Visual Clarity**: Graphite's CLI output stays accurate and helpful.

### Misplaced Commits

If you accidentally commit a change to the wrong branch in a stack:

1. **Stash uncommitted changes**: If you have uncommitted work, `git stash` to keep it safe.
1. **Move the change to a new branch** (if the commit belongs in its own new branch):
   1. `gt create <new-branch>` to safely move the head.
   1. `gt down` to move back to the parent.
   1. `git reset --hard HEAD~1` (Safe now because the commit is preserved in the new branch).
1. **Move to an existing branch**:
   1. `git reset HEAD~1` (Soft reset to keep changes in the working tree).
   1. `gt down`/`gt up` to the target branch.
   1. `gt modify -c` or `gt modify --amend` to incorporate the changes.
1. **Restore stashed changes**: `git stash pop` to resume your work.
1. **Repair the stack**: Move back to the tip and run `gt restack` to ensure all descendant branches are updated with the parent's new state.

## Prohibited Commands

> [!CAUTION]
> **DO NOT** use the following commands under any circumstances:
>
> - `gt ss`
> - `gt submit`
> - `gt squash`

These commands are strictly forbidden to ensure that PR submission, updates, and branch squashing are handled manually or through specified workflows.

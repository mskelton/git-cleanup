# Git Cleanup

Cleanup your git repos

## Features

- Checks out and pulls the default branch
- Prunes stale remote-tracking refs
- Deletes local branches whose upstream is gone
- Resets worktrees that were on deleted branches back to a pool branch
- Rebases worktree-pool checkouts onto the default branch

Worktree pool directories are expected to be named `<repo>-<branch>` next to
the main repo (for example `git-cleanup-feature`). When a branch is gone, that
worktree is reset to a local branch matching the directory name. Active pool
worktrees are rebased onto the default branch.

## Installation

You can install Git Cleanup by running the install script which will download
the [latest release](https://github.com/mskelton/git-cleanup/releases/latest).

```bash
curl -LSfs https://go.mskelton.dev/git-cleanup/install | sh
```

Or you can build from source.

```bash
git clone git@github.com:mskelton/git-cleanup.git
cd git-cleanup
go install .
```

## Usage

```bash
git-cleanup
```

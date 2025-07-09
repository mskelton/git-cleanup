# Git Cleanup

Cleanup your git repos

## Features

- Pulls latest changes from default branch
- Prunes local branches
- Deletes local branches that have been removed on remote
- Removes worktrees for deleted branches
- Reliable default branch detection (works with any git setup)

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

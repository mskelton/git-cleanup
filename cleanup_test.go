package main

import (
	"strings"
	"testing"
	"time"
)

func TestStaleRemoteTracking(t *testing.T) {
	local := []string{
		"refs/remotes/origin/HEAD",
		"refs/remotes/origin/main",
		"refs/remotes/origin/old",
		"refs/remotes/origin/feature/x",
	}
	remoteHeads := map[string]struct{}{
		"refs/remotes/origin/main":      {},
		"refs/remotes/origin/feature/x": {},
	}

	got := staleRemoteTracking(local, remoteHeads, "origin")
	if strings.Join(got, ",") != "refs/remotes/origin/old" {
		t.Fatalf("got %v, want [refs/remotes/origin/old]", got)
	}
}

func TestShortenRemoteURL(t *testing.T) {
	cases := map[string]string{
		"git@github.com:org/repo.git":       "github.com:org/repo",
		"https://github.com/org/repo.git":   "github.com/org/repo",
		"ssh://git@github.com/org/repo.git": "github.com/org/repo",
	}
	for in, want := range cases {
		if got := shortenRemoteURL(in); got != want {
			t.Fatalf("%s: got %q, want %q", in, got, want)
		}
	}
}

func TestParseRootDir(t *testing.T) {
	t.Run("main repo", func(t *testing.T) {
		got, err := parseRootDir("/repo/.git\n.git\n/repo/.git\n")
		if err != nil {
			t.Fatal(err)
		}
		if got != "/repo" {
			t.Fatalf("got %q, want /repo", got)
		}
	})

	t.Run("worktree", func(t *testing.T) {
		got, err := parseRootDir("/repo/.git\n/repo/.git/worktrees/feature\n/repo/.git/worktrees/feature\n")
		if err != nil {
			t.Fatal(err)
		}
		if got != "/repo" {
			t.Fatalf("got %q, want /repo", got)
		}
	})

	t.Run("incomplete output", func(t *testing.T) {
		if _, err := parseRootDir(".git\n"); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestGetRootDirNotARepo(t *testing.T) {
	prev := cwd
	cwd = t.TempDir()
	t.Cleanup(func() { cwd = prev })

	_, err := getRootDir()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("got %v", err)
	}
}

func TestGetRootDirMissingCwd(t *testing.T) {
	prev := cwd
	cwd = "/this/path/does/not/exist"
	t.Cleanup(func() { cwd = prev })

	_, err := getRootDir()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("got %v", err)
	}
}

func TestParseForEachRef(t *testing.T) {
	got := parseForEachRef("" +
		"main\trefs/remotes/origin/main\t\n" +
		"old\trefs/remotes/origin/old\t[gone]\n" +
		"feature/x\trefs/remotes/origin/feature/x\t[ahead 1]\n" +
		"local\t\t\n")

	if len(got) != 4 {
		t.Fatalf("got %d branches, want 4", len(got))
	}
	if got[0].Name != "main" || got[0].Gone {
		t.Fatalf("main: %+v", got[0])
	}
	if got[1].Name != "old" || !got[1].Gone {
		t.Fatalf("old: %+v", got[1])
	}
	if got[2].Name != "feature/x" || got[2].Gone {
		t.Fatalf("feature/x: %+v", got[2])
	}
	if got[3].Name != "local" || got[3].Upstream != "" || got[3].Gone {
		t.Fatalf("local: %+v", got[3])
	}
}

func TestParseWorktreeList(t *testing.T) {
	got := parseWorktreeList(`worktree /repo
HEAD abc
branch refs/heads/main

worktree /repo-foo
HEAD def
branch refs/heads/foo

worktree /repo-foobar
HEAD ghi
branch refs/heads/foobar

worktree /repo-detached
HEAD jkl
detached
`)

	if got["foo"] != "/repo-foo" {
		t.Fatalf("foo: %q", got["foo"])
	}
	if got["foobar"] != "/repo-foobar" {
		t.Fatalf("foobar: %q", got["foobar"])
	}
	if _, ok := got["detached"]; ok {
		t.Fatal("detached HEAD should not be mapped")
	}
	if _, err := worktreePathFor(got, "foo"); err != nil {
		t.Fatal(err)
	}
	if _, err := worktreePathFor(got, "fo"); err == nil {
		t.Fatal("prefix match should not find fo")
	}
}

func TestClassifyBranches(t *testing.T) {
	root := "/repo"
	worktrees := map[string]string{
		"main":    "/repo",
		"old":     "/repo",
		"gone-wt": "/repo-gone-wt",
		"feature": "/repo-feature",
		"scratch": "/repo-scratch",
	}
	branches := []localBranch{
		{Name: "main", Gone: true},
		{Name: "old", Gone: true},
		{Name: "gone-wt", Gone: true},
		{Name: "feature", Gone: false},
		{Name: "scratch", Gone: false},
		{Name: "local-only", Gone: false},
	}

	got := classifyBranches(branches, worktrees, root)

	if strings.Join(got.DeletedBranches, ",") != "main,old,gone-wt" {
		t.Fatalf("deleted: %v", got.DeletedBranches)
	}
	if strings.Join(got.WorktreeBranches, ",") != "gone-wt" {
		t.Fatalf("worktree: %v", got.WorktreeBranches)
	}
	if strings.Join(got.WorktreePoolBranches, ",") != "feature,scratch" {
		t.Fatalf("pool: %v", got.WorktreePoolBranches)
	}
}

func TestFormatElapsed(t *testing.T) {
	if got := formatElapsed(4 * time.Second); got != "4s" {
		t.Fatalf("got %q", got)
	}
	if got := formatElapsed(75 * time.Second); got != "1:15" {
		t.Fatalf("got %q", got)
	}
}

package main

import (
	"errors"
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

func TestSyncDefaultBranchSkipsPullWhenCheckoutFails(t *testing.T) {
	pulled := false
	err := syncDefaultBranch("feature", "main", func() error {
		return errors.New("checkout failed")
	}, func() error {
		pulled = true
		return nil
	})
	if err == nil || err.Error() != "checkout failed" {
		t.Fatalf("got %v", err)
	}
	if pulled {
		t.Fatal("pull ran after checkout failed")
	}
}

func TestSyncDefaultBranchPullsWhenAlreadyOnDefault(t *testing.T) {
	checkedOut := false
	pulled := false
	if err := syncDefaultBranch("main", "main", func() error {
		checkedOut = true
		return nil
	}, func() error {
		pulled = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if checkedOut {
		t.Fatal("checkout ran while already on the default branch")
	}
	if !pulled {
		t.Fatal("expected pull")
	}
}

func TestSyncDefaultBranchChecksOutThenPulls(t *testing.T) {
	var steps []string
	if err := syncDefaultBranch("feature", "main", func() error {
		steps = append(steps, "checkout")
		return nil
	}, func() error {
		steps = append(steps, "pull")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(steps, ",") != "checkout,pull" {
		t.Fatalf("got %v", steps)
	}
}

func TestWorktreeResetStepsRebasesExistingBranch(t *testing.T) {
	got := worktreeResetSteps("/repo", "/repo-feature", "main", true)
	want := [][]string{
		{"-C", "/repo-feature", "rebase", "main", "feature"},
		{"-C", "/repo-feature", "checkout", "feature"},
	}
	if !sameSteps(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestWorktreeResetStepsCreatesMissingBranch(t *testing.T) {
	got := worktreeResetSteps("/repo", "/repo-feature", "main", false)
	want := [][]string{
		{"-C", "/repo-feature", "checkout", "-b", "feature", "main"},
	}
	if !sameSteps(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func sameSteps(got, want [][]string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if strings.Join(got[i], " ") != strings.Join(want[i], " ") {
			return false
		}
	}
	return true
}

func TestFormatElapsed(t *testing.T) {
	if got := formatElapsed(4 * time.Second); got != "4s" {
		t.Fatalf("got %q", got)
	}
	if got := formatElapsed(75 * time.Second); got != "1:15" {
		t.Fatalf("got %q", got)
	}
}

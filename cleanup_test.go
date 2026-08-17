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

func TestFormatElapsed(t *testing.T) {
	if got := formatElapsed(4 * time.Second); got != "4s" {
		t.Fatalf("got %q", got)
	}
	if got := formatElapsed(75 * time.Second); got != "1:15" {
		t.Fatalf("got %q", got)
	}
}

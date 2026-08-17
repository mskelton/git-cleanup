package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
	"github.com/mskelton/git-cleanup/pkg/streamer"
)

var rootDir string

// displayedError is an operation failure already shown by the streamer UI.
type displayedError struct{ error }

func (e displayedError) Unwrap() error { return e.error }

func isDisplayedError(err error) bool {
	var displayed displayedError
	return errors.As(err, &displayed)
}

func runStep(title string, operation func(chan<- string) error) error {
	if err := streamer.Run(title, operation); err != nil {
		return displayedError{err}
	}
	return nil
}

func git(args ...string) *exec.Cmd {
	if !slices.Contains(args, "-C") {
		args = append([]string{"-C", rootDir}, args...)
	}

	return exec.Command("git", args...)
}

func cleanup() error {
	green := color.New(color.FgGreen)
	red := color.New(color.FgRed)

	var err error
	rootDir, err = getRootDir()
	if err != nil {
		return err
	}

	// Get default branch
	defaultBranch, err := getDefaultBranch()
	if err != nil {
		return fmt.Errorf("failed to get default branch: %w", err)
	}

	// Check if we need to checkout default branch
	currentBranch, err := getCurrentBranch()
	if err != nil {
		return fmt.Errorf("failed to get current branch: %w", err)
	}

	if currentBranch != defaultBranch {
		if err := runStep("Checking out default branch", func(outputChan chan<- string) error {
			return checkoutBranch(defaultBranch, outputChan)
		}); err != nil {
			return err
		}
	}

	// Pull latest changes
	if err := runStep("Pulling latest changes", func(outputChan chan<- string) error {
		return pullBranch(defaultBranch, outputChan)
	}); err != nil {
		return err
	}

	// Drop stale remote-tracking refs so gone locals show up later
	if err := runStep("Pruning stale remotes", func(outputChan chan<- string) error {
		return pruneRemote(outputChan)
	}); err != nil {
		return err
	}

	// Get deleted branches
	branches, err := getBranches()
	if err != nil {
		return fmt.Errorf("error getting deleted branches: %w", err)
	}

	// Reset worktrees
	for _, branch := range branches.WorktreeBranches {
		worktreePath, err := worktreePathFor(branches.WorktreePaths, branch)
		if err != nil {
			red.Printf("Error finding worktree for branch %s: %v\n", branch, err)
			continue
		}

		// Convert path to relative format
		homeDir, _ := os.UserHomeDir()
		relativePath := strings.Replace(worktreePath, homeDir, "~", 1)

		if err := runStep(fmt.Sprintf("Resetting worktree: %s", relativePath), func(outputChan chan<- string) error {
			return resetWorktree(defaultBranch, worktreePath, outputChan)
		}); err != nil {
			return err
		}
	}

	// Delete branches
	for _, branch := range branches.DeletedBranches {
		if err := runStep(fmt.Sprintf("Deleting branch: %s", branch), func(outputChan chan<- string) error {
			return deleteBranch(branch, outputChan)
		}); err != nil {
			return err
		}
	}

	// Rebase worktree pool
	if len(branches.WorktreePoolBranches) > 0 {
		if err := runStep("Rebasing worktree pool", func(outputChan chan<- string) error {
			for _, branch := range branches.WorktreePoolBranches {
				worktreePath, err := worktreePathFor(branches.WorktreePaths, branch)
				if err != nil {
					return err
				}

				err = rebaseWorktreePoolBranch(worktreePath, branch, defaultBranch, outputChan)
				if err != nil {
					return err
				}
			}

			return nil
		}); err != nil {
			return err
		}
	}

	green.Println("✔ Git cleanup completed")
	return nil
}

func getRootDir() (string, error) {
	args := []string{"rev-parse", "--git-common-dir", "--git-dir", "--absolute-git-dir"}
	if cwd != "" {
		args = append([]string{"-C", cwd}, args...)
	}

	// Call git directly so we don't prepend -C with an unset rootDir.
	output, err := exec.Command("git", args...).Output()
	if err != nil {
		return "", fmt.Errorf("failed to find git root: %w", err)
	}

	root, err := parseRootDir(string(output))
	if err != nil {
		return "", err
	}
	return root, nil
}

func parseRootDir(output string) (string, error) {
	dirs := strings.Split(output, "\n")
	if len(dirs) < 3 || dirs[0] == "" || dirs[2] == "" {
		return "", fmt.Errorf("failed to find git root: unexpected rev-parse output")
	}

	// If the common dir and the git dir are the same, we are in the main repo
	if dirs[0] == dirs[1] {
		return filepath.Dir(dirs[2]), nil
	}

	// If the common dir and the git dir are different, we are in a worktree, use
	// the common dir
	return filepath.Dir(dirs[0]), nil
}

func getDefaultBranch() (string, error) {
	methods := [][]string{
		{"symbolic-ref", "refs/remotes/origin/HEAD"},
		{"rev-parse", "--abbrev-ref", "origin/HEAD"},
		{"config", "--get", "init.defaultBranch"},
	}

	for _, method := range methods {
		cmd := git(method...)
		output, err := cmd.Output()
		if err == nil {
			result := strings.TrimSpace(string(output))

			result = strings.TrimPrefix(result, "refs/heads/")
			result = strings.TrimPrefix(result, "refs/remotes/")
			result = strings.TrimPrefix(result, "origin/")

			if result != "" {
				return result, nil
			}
		}
	}

	return "", fmt.Errorf("failed to get default branch")
}

func getCurrentBranch() (string, error) {
	cmd := git("branch", "--show-current")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get current branch: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

func checkoutBranch(branch string, outputChan chan<- string) error {
	cmd := git("checkout", branch)
	return streamer.RunCommand(cmd, outputChan)
}

func pullBranch(branch string, outputChan chan<- string) error {
	cmd := git("pull", "--progress", "origin", branch)
	return streamer.RunCommand(cmd, outputChan)
}

func pruneRemote(outputChan chan<- string) error {
	const remote = "origin"

	local, err := listRemoteTracking(remote)
	if err != nil {
		return err
	}
	outputChan <- fmt.Sprintf("Found %d local remotes", len(local))

	target := remoteDisplayName(remote)
	outputChan <- fmt.Sprintf("Waiting for %s…", target)

	remoteHeads, err := listRemoteHeadsWithProgress(remote, target, outputChan)
	if err != nil {
		return err
	}
	outputChan <- fmt.Sprintf("Found %d branches on %s", len(remoteHeads), remote)

	stale := staleRemoteTracking(local, remoteHeads, remote)
	if len(stale) == 0 {
		outputChan <- "No stale remotes to prune"
		return nil
	}

	outputChan <- fmt.Sprintf("Removing %d stale remotes", len(stale))
	for _, ref := range stale {
		outputChan <- "Pruning " + strings.TrimPrefix(ref, "refs/remotes/")
		cmd := git("update-ref", "-d", ref)
		if output, err := cmd.CombinedOutput(); err != nil {
			msg := strings.TrimSpace(string(output))
			if msg == "" {
				return fmt.Errorf("failed to prune %s: %w", ref, err)
			}
			return fmt.Errorf("%s", msg)
		}
	}

	return nil
}

type remoteHeadsResult struct {
	heads map[string]struct{}
	err   error
}

func listRemoteHeadsWithProgress(remote, target string, outputChan chan<- string) (map[string]struct{}, error) {
	var mu sync.Mutex
	received := 0
	done := make(chan remoteHeadsResult, 1)

	go func() {
		heads, err := listRemoteHeads(remote, func(count int) {
			mu.Lock()
			received = count
			mu.Unlock()
		})
		done <- remoteHeadsResult{heads, err}
	}()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	started := time.Now()

	for {
		select {
		case result := <-done:
			return result.heads, result.err
		case <-ticker.C:
			mu.Lock()
			count := received
			mu.Unlock()
			elapsed := formatElapsed(time.Since(started))
			if count > 0 {
				outputChan <- fmt.Sprintf("Received %d branches from %s… %s", count, target, elapsed)
			} else {
				outputChan <- fmt.Sprintf("Waiting for %s… %s", target, elapsed)
			}
		}
	}
}

func listRemoteHeads(remote string, onCount func(int)) (map[string]struct{}, error) {
	cmd := git("ls-remote", "--heads", remote)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	heads := make(map[string]struct{})
	prefix := "refs/remotes/" + remote + "/"
	count := 0
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) < 2 || !strings.HasPrefix(parts[1], "refs/heads/") {
			continue
		}
		heads[prefix+strings.TrimPrefix(parts[1], "refs/heads/")] = struct{}{}
		count++
		if onCount != nil {
			onCount(count)
		}
	}

	waitErr := cmd.Wait()
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if waitErr != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%s", msg)
		}
		return nil, fmt.Errorf("failed to list remote branches: %w", waitErr)
	}

	return heads, nil
}

func remoteDisplayName(remote string) string {
	output, err := git("remote", "get-url", remote).Output()
	if err != nil {
		return remote
	}
	return shortenRemoteURL(strings.TrimSpace(string(output)))
}

func shortenRemoteURL(raw string) string {
	raw = strings.TrimSuffix(raw, ".git")
	for _, prefix := range []string{"https://", "http://", "ssh://"} {
		raw = strings.TrimPrefix(raw, prefix)
	}
	return strings.TrimPrefix(raw, "git@")
}

func formatElapsed(d time.Duration) string {
	secs := int(d.Seconds())
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	return fmt.Sprintf("%d:%02d", secs/60, secs%60)
}

func listRemoteTracking(remote string) ([]string, error) {
	cmd := git("for-each-ref", "--format=%(refname)", "refs/remotes/"+remote)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list remote-tracking branches: %w", err)
	}

	var refs []string
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		if ref := strings.TrimSpace(scanner.Text()); ref != "" {
			refs = append(refs, ref)
		}
	}

	return refs, scanner.Err()
}

func staleRemoteTracking(local []string, remoteHeads map[string]struct{}, remote string) []string {
	headRef := "refs/remotes/" + remote + "/HEAD"
	var stale []string
	for _, ref := range local {
		if ref == headRef {
			continue
		}
		if _, ok := remoteHeads[ref]; !ok {
			stale = append(stale, ref)
		}
	}
	sort.Strings(stale)
	return stale
}

type Branches struct {
	DeletedBranches      []string
	WorktreeBranches     []string
	WorktreePoolBranches []string
	WorktreePaths        map[string]string
}

type localBranch struct {
	Name     string
	Upstream string
	Gone     bool
}

func getBranches() (Branches, error) {
	cmd := git("for-each-ref", "--format=%(refname:short)%09%(upstream)%09%(upstream:track)", "refs/heads")
	output, err := cmd.Output()
	if err != nil {
		return Branches{}, fmt.Errorf("failed to get branch info: %w", err)
	}

	wtCmd := git("worktree", "list", "--porcelain")
	wtOutput, err := wtCmd.Output()
	if err != nil {
		return Branches{}, fmt.Errorf("failed to get worktree list: %w", err)
	}

	worktrees := parseWorktreeList(string(wtOutput))
	return classifyBranches(parseForEachRef(string(output)), worktrees, rootDir), nil
}

func parseForEachRef(output string) []localBranch {
	var branches []localBranch
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		parts := strings.Split(line, "\t")
		for len(parts) < 3 {
			parts = append(parts, "")
		}
		if parts[0] == "" {
			continue
		}

		branches = append(branches, localBranch{
			Name:     parts[0],
			Upstream: parts[1],
			Gone:     strings.Contains(parts[2], "gone"),
		})
	}
	return branches
}

func parseWorktreeList(output string) map[string]string {
	paths := make(map[string]string)
	var worktreePath string

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			worktreePath = ""
			continue
		}
		if rest, ok := strings.CutPrefix(line, "worktree "); ok {
			worktreePath = rest
			continue
		}
		if rest, ok := strings.CutPrefix(line, "branch "); ok {
			if branch, ok := strings.CutPrefix(rest, "refs/heads/"); ok && worktreePath != "" {
				paths[branch] = worktreePath
			}
		}
	}

	return paths
}

func classifyBranches(branches []localBranch, worktrees map[string]string, root string) Branches {
	result := Branches{WorktreePaths: worktrees}

	for _, branch := range branches {
		wtPath, checkedOut := worktrees[branch.Name]
		linked := checkedOut && filepath.Clean(wtPath) != filepath.Clean(root)

		if branch.Gone {
			result.DeletedBranches = append(result.DeletedBranches, branch.Name)
			if linked {
				result.WorktreeBranches = append(result.WorktreeBranches, branch.Name)
			}
			continue
		}

		if linked && worktreeBranchAt(root, wtPath) == branch.Name {
			result.WorktreePoolBranches = append(result.WorktreePoolBranches, branch.Name)
		}
	}

	return result
}

func worktreePathFor(paths map[string]string, branch string) (string, error) {
	path, ok := paths[branch]
	if !ok || path == "" {
		return "", fmt.Errorf("worktree not found for branch %s", branch)
	}
	return path, nil
}

func deleteBranch(branch string, outputChan chan<- string) error {
	cmd := git("branch", "-D", branch)
	return streamer.RunCommand(cmd, outputChan)
}

// worktreeBranch derives the branch name for a worktree by stripping the main
// repo directory name prefix (worktree dirs are named "<repo>-<branch>").
func worktreeBranch(worktreePath string) string {
	return worktreeBranchAt(rootDir, worktreePath)
}

func worktreeBranchAt(root, worktreePath string) string {
	prefix := filepath.Base(root) + "-"
	return strings.TrimPrefix(filepath.Base(worktreePath), prefix)
}

func resetWorktree(defaultBranch, worktreePath string, outputChan chan<- string) error {
	worktreeBranch := worktreeBranch(worktreePath)

	cmd := git("show-ref", "--verify", "--quiet", "refs/heads/"+worktreeBranch)
	if err := streamer.RunCommand(cmd, outputChan); err == nil {
		// Rebase the branch onto the default branch
		if err := rebaseWorktree(worktreePath, worktreeBranch, defaultBranch, outputChan); err != nil {
			return err
		}

		// Checkout the branch in the worktree
		cmd = git("-C", worktreePath, "checkout", worktreeBranch)
		return streamer.RunCommand(cmd, outputChan)
	}

	// Branch doesn't exist, create and checkout in the worktree
	cmd = git("-C", worktreePath, "checkout", "-b", worktreeBranch, defaultBranch)
	return streamer.RunCommand(cmd, outputChan)
}

func rebaseWorktree(worktreePath, branch, defaultBranch string, outputChan chan<- string) error {
	cmd := git("-C", worktreePath, "rebase", defaultBranch, branch)
	return streamer.RunCommand(cmd, outputChan)
}

func rebaseWorktreePoolBranch(worktreePath, branch, defaultBranch string, outputChan chan<- string) error {
	// Check if worktree is dirty
	cmd := git("-C", worktreePath, "status", "--porcelain")
	output, err := cmd.Output()
	if err != nil {
		return err
	}

	isDirty := len(strings.TrimSpace(string(output))) > 0

	if isDirty {
		outputChan <- "Worktree is dirty, stashing changes..."
		stashCmd := git("-C", worktreePath, "stash", "push", "-m", fmt.Sprintf("Auto-stash before rebase %s onto %s", branch, defaultBranch))
		if err := streamer.RunCommand(stashCmd, outputChan); err != nil {
			return err
		}

		outputChan <- "Stashed changes"
	}

	// Perform rebase
	outputChan <- fmt.Sprintf("Rebasing %s onto %s...", branch, defaultBranch)
	rebaseCmd := git("-C", worktreePath, "rebase", defaultBranch, branch)
	if err := streamer.RunCommand(rebaseCmd, outputChan); err != nil {
		// If rebase fails and we stashed changes, try to restore them
		if isDirty {
			outputChan <- "Rebase failed, restoring stashed changes..."
			unstashCmd := git("-C", worktreePath, "stash", "pop")
			if unstashErr := streamer.RunCommand(unstashCmd, outputChan); unstashErr != nil {
				outputChan <- fmt.Sprintf("Warning: failed to restore stashed changes: %v", unstashErr)
			}
		}

		return err
	}

	// If rebase succeeded and we stashed changes, restore them
	if isDirty {
		outputChan <- "Rebase successful, restoring stashed changes..."
		unstashCmd := git("-C", worktreePath, "stash", "pop")
		if err := streamer.RunCommand(unstashCmd, outputChan); err != nil {
			outputChan <- fmt.Sprintf("Warning: failed to restore stashed changes: %v", err)
		}
	}

	return nil
}

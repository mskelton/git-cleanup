package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/fatih/color"
	"github.com/mskelton/git-cleanup/pkg/streamer"
)

var gitDir string

func git(args ...string) *exec.Cmd {
	if !slices.Contains(args, "-C") {
		args = append([]string{"-C", cwd}, args...)
	}

	return exec.Command("git", args...)
}

func cleanup() error {
	green := color.New(color.FgGreen)
	red := color.New(color.FgRed)

	gitDir = getGitDir()

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
		streamer.Run("Checking out default branch", func(outputChan chan<- string) error {
			return checkoutBranch(defaultBranch, outputChan)
		})
	}

	// Pull latest changes
	streamer.Run("Pulling latest changes", func(outputChan chan<- string) error {
		return pullBranch(defaultBranch, outputChan)
	})

	// Prune branches
	streamer.Run("Pruning local branches", func(outputChan chan<- string) error {
		return fetchPrune(outputChan)
	})

	// Get deleted branches
	branches, err := getBranches()
	if err != nil {
		return fmt.Errorf("error getting deleted branches: %w", err)
	}

	// Reset worktrees
	for _, branch := range branches.WorktreeBranches {
		worktreePath, err := getWorktreePath(branch)
		if err != nil {
			red.Printf("Error finding worktree for branch %s: %v\n", branch, err)
			continue
		}

		// Convert path to relative format
		homeDir, _ := os.UserHomeDir()
		relativePath := strings.Replace(worktreePath, homeDir, "~", 1)

		streamer.Run(fmt.Sprintf("Resetting worktree: %s", relativePath), func(outputChan chan<- string) error {
			return resetWorktree(defaultBranch, worktreePath, outputChan)
		})
	}

	// Delete branches
	for _, branch := range branches.DeletedBranches {
		streamer.Run(fmt.Sprintf("Deleting branch: %s", branch), func(outputChan chan<- string) error {
			return deleteBranch(branch, outputChan)
		})
	}

	// Rebase worktree pool
	if len(branches.WorktreePoolBranches) > 0 {
		streamer.Run("Rebasing worktree pool", func(outputChan chan<- string) error {
			for _, branch := range branches.WorktreePoolBranches {
				worktreePath, err := getWorktreePath(branch)
				if err != nil {
					return err
				}

				err = rebaseWorktreePoolBranch(worktreePath, branch, defaultBranch, outputChan)
				if err != nil {
					return err
				}
			}

			return nil
		})
	}

	green.Println("✔ Git cleanup completed")
	return nil
}

func getGitDir() string {
	output, err := git("rev-parse", "--git-common-dir", "--git-dir", "--absolute-git-dir").Output()
	if err != nil {
		return ""
	}

	dirs := strings.Split(string(output), "\n")
	if len(dirs) < 3 {
		return ""
	}

	// If the common dir and the git dir are the same, we are in the main repo
	if dirs[0] == dirs[1] {
		return dirs[2]
	}

	// If the common dir and the git dir are different, we are in a worktree, use
	// the common dir
	return dirs[0]
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
	cmd := git("--git-dir", gitDir, "branch", "--show-current")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get current branch: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

func checkoutBranch(branch string, outputChan chan<- string) error {
	cmd := git("--git-dir", gitDir, "checkout", branch)
	return streamer.RunCommand(cmd, outputChan)
}

func pullBranch(branch string, outputChan chan<- string) error {
	cmd := git("--git-dir", gitDir, "pull", "origin", branch)
	return streamer.RunCommand(cmd, outputChan)
}

func fetchPrune(outputChan chan<- string) error {
	cmd := git("--git-dir", gitDir, "fetch", "-p")
	return streamer.RunCommand(cmd, outputChan)
}

func getBranches() (struct {
	DeletedBranches      []string
	WorktreeBranches     []string
	WorktreePoolBranches []string
}, error) {
	var result struct {
		DeletedBranches      []string
		WorktreeBranches     []string
		WorktreePoolBranches []string
	}

	cmd := git("branch", "-vv")
	output, err := cmd.Output()
	if err != nil {
		return result, fmt.Errorf("failed to get branch info: %w", err)
	}

	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()

		if regexp.MustCompile(`origin/.*: gone\]`).MatchString(line) {
			parts := strings.Fields(line)

			if strings.HasPrefix(line, "+") && len(parts) >= 2 {
				result.WorktreeBranches = append(result.WorktreeBranches, parts[1])
				result.DeletedBranches = append(result.DeletedBranches, parts[1])
			} else if len(parts) > 0 {
				result.DeletedBranches = append(result.DeletedBranches, parts[0])
			}
		} else if strings.HasPrefix(line, "+") {
			parts := strings.Fields(line)
			branch := parts[1]
			path := parts[3][1 : len(parts[3])-1]

			if strings.TrimPrefix(filepath.Base(path), "web-") == branch {
				result.WorktreePoolBranches = append(result.WorktreePoolBranches, branch)
			}
		}
	}

	return result, nil
}

func deleteBranch(branch string, outputChan chan<- string) error {
	cmd := git("branch", "-D", branch)
	return streamer.RunCommand(cmd, outputChan)
}

func getWorktreePath(branch string) (string, error) {
	cmd := git("worktree", "list", "--porcelain")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get worktree list: %w", err)
	}

	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	var worktreePath string
	var foundBranch bool

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "worktree ") {
			worktreePath = strings.TrimPrefix(line, "worktree ")
		} else if strings.HasPrefix(line, "branch ") {
			if strings.Contains(line, "refs/heads/"+branch) {
				foundBranch = true
				break
			}
		}
	}

	if !foundBranch {
		return "", fmt.Errorf("worktree not found for branch %s", branch)
	}

	return worktreePath, nil
}

func resetWorktree(defaultBranch, worktreePath string, outputChan chan<- string) error {
	worktreeBranch := strings.TrimPrefix(filepath.Base(worktreePath), "web-")

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
	cmd = git("-C", worktreePath, "checkout", "-b", worktreeBranch)
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

		// Stash changes
		stashCmd := git("-C", worktreePath, "stash", "push", "-m", fmt.Sprintf("Auto-stash before rebase %s onto %s", branch, defaultBranch))
		err := streamer.RunCommand(stashCmd, outputChan)
		if err != nil {
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

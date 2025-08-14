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

func git(args ...string) *exec.Cmd {
	if cwd != "" && !slices.Contains(args, "-C") {
		args = append([]string{"-C", cwd}, args...)
	}

	return exec.Command("git", args...)
}

func runCommand(cmd *exec.Cmd, outputChan chan<- string) error {
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(output)))
	}
	return nil
}

func cleanup() error {
	green := color.New(color.FgGreen)
	red := color.New(color.FgRed)

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
	deletedBranches, deletedWorktreeBranches, err := getDeletedBranches()
	if err != nil {
		return fmt.Errorf("error getting deleted branches: %w", err)
	}

	// Reset worktrees
	for _, branch := range deletedWorktreeBranches {
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
	for _, branch := range deletedBranches {
		streamer.Run(fmt.Sprintf("Deleting branch: %s", branch), func(outputChan chan<- string) error {
			return deleteBranch(branch, outputChan)
		})
	}

	green.Println("✔ Git cleanup completed")
	return nil
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
	return runCommand(cmd, outputChan)
}

func pullBranch(branch string, outputChan chan<- string) error {
	cmd := git("pull", "origin", branch)
	return runCommand(cmd, outputChan)
}

func fetchPrune(outputChan chan<- string) error {
	cmd := git("fetch", "-p")
	return runCommand(cmd, outputChan)
}

func getDeletedBranches() ([]string, []string, error) {
	cmd := git("branch", "-vv")
	output, err := cmd.Output()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get branch info: %w", err)
	}

	var branches []string
	var worktreeBranches []string

	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()

		if regexp.MustCompile(`origin/.*: gone\]`).MatchString(line) {
			parts := strings.Fields(line)

			if strings.HasPrefix(line, "+") && len(parts) >= 2 {
				worktreeBranches = append(worktreeBranches, parts[1])
				branches = append(branches, parts[1])
			} else if len(parts) > 0 {
				branches = append(branches, parts[0])
			}
		}
	}

	return branches, worktreeBranches, nil
}

func deleteBranch(branch string, outputChan chan<- string) error {
	cmd := git("branch", "-D", branch)
	return runCommand(cmd, outputChan)
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

func resetWorktree(defaultBranch, path string, outputChan chan<- string) error {
	worktreeBranch := strings.TrimPrefix(filepath.Base(path), "web-")

	cmd := git("show-ref", "--verify", "--quiet", "refs/heads/"+worktreeBranch)
	if err := runCommand(cmd, outputChan); err == nil {
		// Rebase the branch onto the default branch
		cmd = git("-C", path, "rebase", "origin/"+defaultBranch, worktreeBranch)
		if err := runCommand(cmd, outputChan); err != nil {
			return err
		}

		// Checkout the branch in the worktree
		cmd = git("-C", path, "checkout", worktreeBranch)
		return runCommand(cmd, outputChan)
	}

	// Branch doesn't exist, create and checkout in the worktree
	cmd = git("-C", path, "checkout", "-b", worktreeBranch)
	return runCommand(cmd, outputChan)
}

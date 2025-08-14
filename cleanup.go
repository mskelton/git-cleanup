package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/fatih/color"
)

// git creates a git command with automatic cwd handling
func git(args ...string) *exec.Cmd {
	if cwd != "" {
		args = append([]string{"-C", cwd}, args...)
	}
	return exec.Command("git", args...)
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
		if err := runWithSpinner("Checking out default branch", func(outputChan chan<- string) error {
			return checkoutBranch(defaultBranch, outputChan)
		}); err != nil {
			return fmt.Errorf("error checking out default branch: %w", err)
		}

		fmt.Printf("✔ Checked out default branch: %s\n", defaultBranch)
	}

	// Pull latest changes
	if err := runWithSpinner("Pulling latest changes", func(outputChan chan<- string) error {
		return pullBranch(defaultBranch, outputChan)
	}); err != nil {
		return fmt.Errorf("error pulling latest changes: %w", err)
	}

	fmt.Printf("✔ Pulled latest changes from %s\n", defaultBranch)

	// Prune branches
	if err := runWithSpinner("Pruning local branches", func(outputChan chan<- string) error {
		return fetchPrune(outputChan)
	}); err != nil {
		return fmt.Errorf("error pruning branches: %w", err)
	}

	fmt.Println("✔ Pruned local branches")

	// Get deleted branches
	deletedBranches, deletedWorktreeBranches, err := getDeletedBranches()
	if err != nil {
		return fmt.Errorf("error getting deleted branches: %w", err)
	}

	// Delete regular branches
	for _, branch := range deletedBranches {
		if err := runWithSpinner(fmt.Sprintf("Deleting branch: %s", branch), func(outputChan chan<- string) error {
			return deleteBranch(branch, outputChan)
		}); err != nil {
			red.Printf("Error deleting branch %s: %v\n", branch, err)
			continue
		}

		fmt.Printf("✔ Deleted branch: %s\n", branch)
	}

	// Delete worktree branches
	for _, branch := range deletedWorktreeBranches {
		worktreePath, err := getWorktreePath(branch)
		if err != nil {
			red.Printf("Error finding worktree for branch %s: %v\n", branch, err)
			continue
		}

		// Convert path to relative format
		homeDir, _ := os.UserHomeDir()
		relativePath := strings.Replace(worktreePath, homeDir, "~", 1)

		if err := runWithSpinner(fmt.Sprintf("Deleting worktree: %s", relativePath), func(outputChan chan<- string) error {
			return removeWorktree(worktreePath, outputChan)
		}); err != nil {
			red.Printf("Error removing worktree %s: %v\n", relativePath, err)
			continue
		}

		if err := deleteBranch(branch, nil); err != nil {
			red.Printf("Error deleting branch %s: %v\n", branch, err)
			continue
		}

		fmt.Printf("✔ Deleted worktree: %s\n", relativePath)
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
			if strings.HasPrefix(line, "+") {
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					worktreeBranches = append(worktreeBranches, parts[1])
				}
			} else {
				parts := strings.Fields(line)
				if len(parts) > 0 {
					branches = append(branches, parts[0])
				}
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

func removeWorktree(path string, outputChan chan<- string) error {
	cmd := git("worktree", "remove", path)
	return runCommand(cmd, outputChan)
}

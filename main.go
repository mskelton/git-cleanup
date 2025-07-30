package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/briandowns/spinner"
	"github.com/fatih/color"
)

const charSet = 14

func runWithSpinner(suffix string, operation func() error) error {
	s := spinner.New(spinner.CharSets[charSet], 100*time.Millisecond)
	s.Suffix = " " + suffix
	s.Start()

	err := operation()

	s.Stop()
	return err
}

func getDefaultBranch() (string, error) {
	methods := [][]string{
		{"symbolic-ref", "refs/remotes/origin/HEAD"},
		{"rev-parse", "--abbrev-ref", "origin/HEAD"},
		{"config", "--get", "init.defaultBranch"},
	}

	for _, method := range methods {
		output, err := git(exec.Command("git", method...))
		if err == nil {
			result := strings.TrimSpace(string(output))

			// Clean up the result
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
	output, err := git(exec.Command("git", "branch", "--show-current"))
	if err != nil {
		return "", fmt.Errorf("failed to get current branch: %w", err)
	}

	return strings.TrimSpace(string(output)), nil
}

func checkoutBranch(branch string) error {
	_, err := git(exec.Command("git", "checkout", branch))
	return err
}

func pullBranch(branch string) error {
	_, err := git(exec.Command("git", "pull", "origin", branch))
	return err
}

func fetchPrune() error {
	_, err := git(exec.Command("git", "fetch", "-p"))
	return err
}

func getDeletedBranches() ([]string, []string, error) {
	output, err := git(exec.Command("git", "branch", "-vv"))
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
				// Worktree branch
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					worktreeBranches = append(worktreeBranches, parts[1])
				}
			} else {
				// Regular branch
				parts := strings.Fields(line)
				if len(parts) > 0 {
					branches = append(branches, parts[0])
				}
			}
		}
	}

	return branches, worktreeBranches, nil
}

func deleteBranch(branch string) error {
	_, err := git(exec.Command("git", "branch", "-D", branch))
	return err
}

func getWorktreePath(branch string) (string, error) {
	output, err := git(exec.Command("git", "worktree", "list", "--porcelain"))
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

func removeWorktree(path string) error {
	_, err := git(exec.Command("git", "worktree", "remove", path))
	return err
}

func main() {
	green := color.New(color.FgGreen)
	red := color.New(color.FgRed)

	// Get default branch
	defaultBranch, err := getDefaultBranch()
	if err != nil {
		red.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	// Check if we need to checkout default branch
	currentBranch, err := getCurrentBranch()
	if err != nil {
		red.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	if currentBranch != defaultBranch {
		if err := runWithSpinner("Checking out default branch", func() error {
			return checkoutBranch(defaultBranch)
		}); err != nil {
			red.Printf("Error checking out default branch: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("✔ Checked out default branch: %s\n", defaultBranch)
	}

	// Pull latest changes
	if err := runWithSpinner("Pulling latest changes", func() error {
		return pullBranch(defaultBranch)
	}); err != nil {
		red.Printf("Error pulling latest changes: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✔ Pulled latest changes from %s\n", defaultBranch)

	// Prune branches
	if err := runWithSpinner("Pruning local branches", func() error {
		return fetchPrune()
	}); err != nil {
		red.Printf("Error pruning branches: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("✔ Pruned local branches")

	// Get deleted branches
	deletedBranches, deletedWorktreeBranches, err := getDeletedBranches()
	if err != nil {
		red.Printf("Error getting deleted branches: %v\n", err)
		os.Exit(1)
	}

	// Delete regular branches
	for _, branch := range deletedBranches {
		if err := runWithSpinner(fmt.Sprintf("Deleting branch: %s", branch), func() error {
			return deleteBranch(branch)
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

		if err := runWithSpinner(fmt.Sprintf("Deleting worktree: %s", relativePath), func() error {
			return removeWorktree(worktreePath)
		}); err != nil {
			red.Printf("Error removing worktree %s: %v\n", relativePath, err)
			continue
		}

		if err := deleteBranch(branch); err != nil {
			red.Printf("Error deleting branch %s: %v\n", branch, err)
			continue
		}

		fmt.Printf("✔ Deleted worktree: %s\n", relativePath)
	}

	green.Println("\n✨ Git cleanup completed successfully!")
}

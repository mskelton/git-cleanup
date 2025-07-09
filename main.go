package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/briandowns/spinner"
	"github.com/fatih/color"
)

type Status struct {
	lines []string
}

func (s *Status) Add(line string) {
	s.lines = append(s.lines, line)
	if len(s.lines) > 3 {
		s.lines = s.lines[1:]
	}
}

func (s *Status) Print() {
	// Clear previous lines
	for i := 0; i < 3; i++ {
		fmt.Print("\033[2K\033[A")
	}

	// Print current status
	for i, line := range s.lines {
		fmt.Printf("\033[2K%s\n", line)
		if i < len(s.lines)-1 {
			fmt.Print("\033[2K\n")
		}
	}
}

func runCommand(cmd *exec.Cmd) error {
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("command failed: %s\nOutput: %s", err, string(output))
	}
	return nil
}

func getDefaultBranch() (string, error) {
	methods := [][]string{
		{"symbolic-ref", "refs/remotes/origin/HEAD"},
		{"rev-parse", "--abbrev-ref", "origin/HEAD"},
		{"config", "--get", "init.defaultBranch"},
	}

	for _, method := range methods {
		cmd := exec.Command("git", method...)
		output, err := cmd.Output()
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
	cmd := exec.Command("git", "branch", "--show-current")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get current branch: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

func checkoutBranch(branch string) error {
	cmd := exec.Command("git", "checkout", branch)
	return runCommand(cmd)
}

func pullBranch(branch string) error {
	cmd := exec.Command("git", "pull", "origin", branch)
	return runCommand(cmd)
}

func fetchPrune() error {
	cmd := exec.Command("git", "fetch", "-p")
	return runCommand(cmd)
}

func getDeletedBranches() ([]string, []string, error) {
	cmd := exec.Command("git", "branch", "-vv")
	output, err := cmd.Output()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get branch info: %w", err)
	}

	var branches []string
	var worktreeBranches []string

	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "origin/.*: gone]") {
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
	cmd := exec.Command("git", "branch", "-D", branch)
	return runCommand(cmd)
}

func getWorktreePath(branch string) (string, error) {
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
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

func removeWorktree(path string) error {
	cmd := exec.Command("git", "worktree", "remove", path)
	return runCommand(cmd)
}

func main() {
	status := &Status{}
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
		s := spinner.New(spinner.CharSets[9], 100*time.Millisecond)
		s.Suffix = " Checking out default branch"
		s.Start()

		if err := checkoutBranch(defaultBranch); err != nil {
			s.Stop()
			red.Printf("Error checking out default branch: %v\n", err)
			os.Exit(1)
		}

		s.Stop()
		status.Add(green.Sprintf("✔ Checked out default branch: %s", defaultBranch))
	}

	// Pull latest changes
	s := spinner.New(spinner.CharSets[9], 100*time.Millisecond)
	s.Suffix = " Pulling latest changes"
	s.Start()

	if err := pullBranch(defaultBranch); err != nil {
		s.Stop()
		red.Printf("Error pulling latest changes: %v\n", err)
		os.Exit(1)
	}

	s.Stop()
	status.Add(green.Sprintf("✔ Pulled latest changes from %s", defaultBranch))

	// Prune branches
	s = spinner.New(spinner.CharSets[9], 100*time.Millisecond)
	s.Suffix = " Pruning local branches"
	s.Start()

	if err := fetchPrune(); err != nil {
		s.Stop()
		red.Printf("Error pruning branches: %v\n", err)
		os.Exit(1)
	}

	s.Stop()
	status.Add(green.Sprintf("✔ Pruned local branches"))

	// Get deleted branches
	deletedBranches, deletedWorktreeBranches, err := getDeletedBranches()
	if err != nil {
		red.Printf("Error getting deleted branches: %v\n", err)
		os.Exit(1)
	}

	// Delete regular branches
	for _, branch := range deletedBranches {
		s = spinner.New(spinner.CharSets[9], 100*time.Millisecond)
		s.Suffix = fmt.Sprintf(" Deleting branch: %s", branch)
		s.Start()

		if err := deleteBranch(branch); err != nil {
			s.Stop()
			red.Printf("Error deleting branch %s: %v\n", branch, err)
			continue
		}

		s.Stop()
		status.Add(green.Sprintf("✔ Deleted branch: %s", branch))
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

		s = spinner.New(spinner.CharSets[9], 100*time.Millisecond)
		s.Suffix = fmt.Sprintf(" Deleting worktree: %s", relativePath)
		s.Start()

		if err := removeWorktree(worktreePath); err != nil {
			s.Stop()
			red.Printf("Error removing worktree %s: %v\n", relativePath, err)
			continue
		}

		if err := deleteBranch(branch); err != nil {
			s.Stop()
			red.Printf("Error deleting branch %s: %v\n", branch, err)
			continue
		}

		s.Stop()
		status.Add(green.Sprintf("✔ Deleted worktree: %s", relativePath))
	}

	// Print final status
	status.Print()
	green.Println("\n✨ Git cleanup completed successfully!")
}

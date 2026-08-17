package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	cwd string
)

func main() {
	var rootCmd = &cobra.Command{
		Use:   "git-cleanup",
		Short: "Clean up your git repositories",
		Long: `Git Cleanup is a tool that helps maintain clean git repositories by:
- Checking out and pulling the default branch
- Pruning stale remote-tracking refs
- Deleting local branches whose upstream is gone
- Resetting worktrees for deleted branches back to a pool branch
- Rebasing worktree-pool branches (<repo>-<branch> directories) onto the default branch`,
		Version:       "1.0.0",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cleanup()
		},
	}

	rootCmd.Flags().StringVar(&cwd, "cwd", "", "Run commands in this directory")

	if err := rootCmd.Execute(); err != nil {
		if !isDisplayedError(err) {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		os.Exit(1)
	}
}

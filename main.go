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
- Pulling latest changes from the default branch
- Pruning local branches that have been removed on remote
- Deleting local branches that no longer exist on remote
- Removing worktrees for deleted branches
- Auto-retrying git operations that fail due to ref locking issues`,
		Version: "1.0.0",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cleanup()
		},
	}

	rootCmd.Flags().StringVar(&cwd, "cwd", "", "Run commands in this directory")

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

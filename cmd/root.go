package cmd

import (
	"github.com/spf13/cobra"
)

// NewRootCommand creates and returns the root cobra command with all global
// persistent flags registered. Subcommands are attached here.
func NewRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "mint",
		Short: "Provision and manage EC2-based development environments",
		Long:  "Provision and manage EC2-based development environments for running Claude Code.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Global flags matching CLI UX conventions (ADR-0012)
	rootCmd.PersistentFlags().Bool("verbose", false, "Show progress steps")
	rootCmd.PersistentFlags().Bool("debug", false, "Show AWS SDK details")
	rootCmd.PersistentFlags().Bool("json", false, "Machine-readable JSON output")
	rootCmd.PersistentFlags().Bool("yes", false, "Skip confirmation on destructive operations")
	rootCmd.PersistentFlags().String("vm", "default", "Target VM name")

	// Register subcommands
	rootCmd.AddCommand(newVersionCommand())
	rootCmd.AddCommand(newConfigCommand())

	return rootCmd
}

// Execute creates the root command and runs it. Called from main.
func Execute() error {
	return NewRootCommand().Execute()
}

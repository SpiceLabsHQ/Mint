package cmd

import (
	"context"

	"github.com/nicholasgasior/mint/internal/cli"
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
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			cliCtx := cli.NewCLIContext(cmd)
			cmd.SetContext(cli.WithContext(context.Background(), cliCtx))
			return nil
		},
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
	rootCmd.AddCommand(newDownCommand())
	rootCmd.AddCommand(newDestroyCommand())
	rootCmd.AddCommand(newInitCommand())
	rootCmd.AddCommand(newUpCommand())
	rootCmd.AddCommand(newSSHConfigCommand())
	rootCmd.AddCommand(newListCommand())
	rootCmd.AddCommand(newStatusCommand())
	rootCmd.AddCommand(newSSHCommand())
	rootCmd.AddCommand(newCodeCommand())

	return rootCmd
}

// Execute creates the root command and runs it. Called from main.
func Execute() error {
	return NewRootCommand().Execute()
}

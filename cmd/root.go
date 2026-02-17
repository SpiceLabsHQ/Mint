package cmd

import (
	"context"
	"fmt"

	"github.com/nicholasgasior/mint/internal/cli"
	"github.com/spf13/cobra"
)

// NewRootCommand creates and returns the root cobra command with all global
// persistent flags registered. Subcommands are attached here.
func NewRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:           "mint",
		Short:         "Provision and manage EC2-based development environments",
		Long:          "Provision and manage EC2-based development environments for running Claude Code.",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			cliCtx := cli.NewCLIContext(cmd)
			ctx := cli.WithContext(context.Background(), cliCtx)

			// Initialize AWS clients for commands that need them.
			// Local-only commands (version, config, ssh-config, help)
			// skip AWS initialization entirely.
			if commandNeedsAWS(cmd.Name()) {
				clients, err := initAWSClients(ctx)
				if err != nil {
					return fmt.Errorf("initialize AWS: %w", err)
				}
				ctx = contextWithAWSClients(ctx, clients)
			}

			cmd.SetContext(ctx)
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

	// Phase 2: Connectivity & session commands
	rootCmd.AddCommand(newMoshCommand())
	rootCmd.AddCommand(newConnectCommand())
	rootCmd.AddCommand(newSessionsCommand())
	rootCmd.AddCommand(newKeyCommand())
	rootCmd.AddCommand(newProjectCommand())
	rootCmd.AddCommand(newExtendCommand())

	// Phase 3: Lifecycle & health commands
	rootCmd.AddCommand(newResizeCommand())
	rootCmd.AddCommand(newRecreateCommand())

	return rootCmd
}

// embeddedBootstrapScript holds the bootstrap script bytes passed in from
// main.go's go:embed directive. This package-level variable is set by
// SetBootstrapScript before building the command tree.
var embeddedBootstrapScript []byte

// SetBootstrapScript stores the embedded bootstrap script for use by
// subcommands (e.g., up) that need it for EC2 provisioning.
func SetBootstrapScript(script []byte) {
	embeddedBootstrapScript = script
}

// GetBootstrapScript returns the embedded bootstrap script previously
// stored via SetBootstrapScript. Returns nil if no script has been set.
func GetBootstrapScript() []byte {
	return embeddedBootstrapScript
}

// Execute creates the root command and runs it. Called from main.
// Deprecated: Use ExecuteWithBootstrapScript to pass the embedded bootstrap script.
func Execute() error {
	return NewRootCommand().Execute()
}

// ExecuteWithBootstrapScript stores the embedded bootstrap script and
// executes the root command. The script is made available to subcommands
// (e.g., up) that need it for EC2 provisioning.
func ExecuteWithBootstrapScript(script []byte) error {
	SetBootstrapScript(script)
	return NewRootCommand().Execute()
}

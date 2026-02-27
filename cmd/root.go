package cmd

import (
	"context"
	"fmt"

	"github.com/SpiceLabsHQ/Mint/internal/cli"
	"github.com/SpiceLabsHQ/Mint/internal/config"
	"github.com/spf13/cobra"
)

// Ensure silentExitError satisfies the error interface (compile-time check).
var _ error = silentExitError{}

// NewRootCommand creates and returns the root cobra command with all global
// persistent flags registered. Subcommands are attached here.
func NewRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:           "mint",
		Short:         "Provision and manage EC2-based development environments",
		Long:          "Provision and manage EC2-based development environments for running Claude Code.",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			cliCtx := cli.NewCLIContext(cmd)
			ctx := cli.WithContext(context.Background(), cliCtx)

			// Initialize AWS clients for commands that need them.
			// Local-only commands (version, config, ssh-config, completion,
			// help) skip AWS initialization entirely.
			if commandNeedsAWS(cmd) {
				clients, err := initAWSClients(ctx)
				if err != nil {
					friendlyMsg := fmt.Sprintf("initialize AWS: %v", err)
					if isCredentialError(err) || isSSOReAuthError(err) {
						// Derive the effective profile so credentialErrMessage can
						// produce a targeted "aws sso login --profile <name>" hint
						// when the error is an SSO token expiry.
						// Precedence: --profile flag > config aws_profile.
						profile := cliCtx.Profile
						if profile == "" {
							if mintCfg, cfgErr := config.Load(config.DefaultConfigDir()); cfgErr == nil {
								profile = mintCfg.AWSProfile
							}
						}
						friendlyMsg = fmt.Sprintf("AWS credentials: %s", credentialErrMessage(err, profile))
					}
					// In JSON mode, write structured error to stdout so machine
					// consumers get valid JSON instead of plaintext on stderr
					// (Bug #67). Use silentExitError so main.go doesn't
					// double-print.
					if cliCtx.JSON {
						cmd.SetContext(ctx)
						fmt.Fprintf(cmd.OutOrStdout(), "{\"error\":%q}\n", friendlyMsg)
						return silentExitError{}
					}
					return fmt.Errorf("%s", friendlyMsg)
				}
				ctx = contextWithAWSClients(ctx, clients)
			}

			cmd.SetContext(ctx)
			return nil
		},
	}

	// Set a consistent version template so `mint --version` prints
	// "mint version dev" rather than cobra's default "mint version dev\n".
	rootCmd.SetVersionTemplate("mint version {{.Version}}\n")

	// Global flags matching CLI UX conventions (ADR-0012)
	rootCmd.PersistentFlags().Bool("verbose", false, "Show progress steps")
	rootCmd.PersistentFlags().Bool("debug", false, "Show AWS SDK details")
	rootCmd.PersistentFlags().Bool("json", false, "Machine-readable JSON output")
	rootCmd.PersistentFlags().Bool("yes", false, "Skip confirmation on destructive operations")
	rootCmd.PersistentFlags().String("vm", "default", "Target VM name")
	rootCmd.PersistentFlags().String("profile", "", "AWS profile name (overrides AWS_PROFILE)")

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
	rootCmd.AddCommand(newDoctorCommand())
	rootCmd.AddCommand(newUpdateCommand())

	// Admin commands for infrastructure setup
	rootCmd.AddCommand(newAdminCommand())

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

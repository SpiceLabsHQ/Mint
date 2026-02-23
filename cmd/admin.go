package cmd

import (
	"github.com/spf13/cobra"
)

// newAdminCommand creates the parent "admin" command group with subcommands.
func newAdminCommand() *cobra.Command {
	return newAdminCommandWithDeployDeps(nil)
}

// newAdminCommandWithDeployDeps creates the admin command tree with explicit
// deploy dependencies for testing.
func newAdminCommandWithDeployDeps(deployDeps *adminDeployDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Admin tools for setting up Mint infrastructure",
		Long:  "Admin tools for setting up Mint infrastructure. These commands are intended for privileged operators.",
	}

	cmd.AddCommand(newAdminDeployCommandWithDeps(deployDeps))
	cmd.AddCommand(newAdminAttachPolicyCommand())
	cmd.AddCommand(newAdminSetupCommand())

	return cmd
}

// newAdminCommandWithAttachPolicyDeps creates the admin command tree with explicit
// attach-policy dependencies for testing.
func newAdminCommandWithAttachPolicyDeps(attachPolicyDeps *adminAttachPolicyDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Admin tools for setting up Mint infrastructure",
		Long:  "Admin tools for setting up Mint infrastructure. These commands are intended for privileged operators.",
	}

	cmd.AddCommand(newAdminDeployCommand())
	cmd.AddCommand(newAdminAttachPolicyCommandWithDeps(attachPolicyDeps))
	cmd.AddCommand(newAdminSetupCommand())

	return cmd
}

// newAdminCommandWithSetupDeps creates the admin command tree with explicit
// setup dependencies for testing.
func newAdminCommandWithSetupDeps(setupDeps *adminSetupDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Admin tools for setting up Mint infrastructure",
		Long:  "Admin tools for setting up Mint infrastructure. These commands are intended for privileged operators.",
	}

	cmd.AddCommand(newAdminDeployCommand())
	cmd.AddCommand(newAdminAttachPolicyCommand())
	cmd.AddCommand(newAdminSetupCommandWithDeps(setupDeps))

	return cmd
}

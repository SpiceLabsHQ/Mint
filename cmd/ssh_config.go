package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/nicholasgasior/mint/internal/cli"
	"github.com/nicholasgasior/mint/internal/config"
	"github.com/nicholasgasior/mint/internal/sshconfig"
	"github.com/spf13/cobra"
)

// defaultSSHPort is the non-standard SSH port per ADR-0016.
const defaultSSHPort = 41122

// defaultSSHUser is the default user for Ubuntu 24.04 VMs.
const defaultSSHUser = "ubuntu"

func newSSHConfigCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ssh-config",
		Short: "Manage SSH config entries for mint VMs",
		Long: "Generate and manage SSH config Host blocks for mint VMs. " +
			"Managed blocks are marked with # mint:begin/end markers and " +
			"include a SHA256 checksum for hand-edit detection (ADR-0008).",
		Args: cobra.NoArgs,
		RunE: runSSHConfig,
	}

	cmd.Flags().String("hostname", "", "Public IP or hostname of the VM")
	cmd.Flags().String("instance-id", "", "EC2 instance ID for ProxyCommand (required)")
	cmd.Flags().String("az", "", "Availability zone for EC2 Instance Connect (required)")
	cmd.Flags().String("ssh-config-path", "", "Path to SSH config file (default: ~/.ssh/config)")
	cmd.Flags().Bool("remove", false, "Remove the managed block for the VM")

	return cmd
}

// defaultSSHConfigPath returns ~/.ssh/config.
func defaultSSHConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".ssh", "config")
	}
	return filepath.Join(home, ".ssh", "config")
}

func runSSHConfig(cmd *cobra.Command, args []string) error {
	cliCtx := cli.FromCommand(cmd)
	w := cmd.OutOrStdout()

	vmName := "default"
	yes := false
	if cliCtx != nil {
		vmName = cliCtx.VM
		yes = cliCtx.Yes
	}

	sshConfigPath, _ := cmd.Flags().GetString("ssh-config-path")
	if sshConfigPath == "" {
		sshConfigPath = defaultSSHConfigPath()
	}

	remove, _ := cmd.Flags().GetBool("remove")
	if remove {
		return runSSHConfigRemove(cmd, sshConfigPath, vmName)
	}

	hostname, _ := cmd.Flags().GetString("hostname")
	if hostname == "" {
		return fmt.Errorf("--hostname is required (public IP or hostname of the VM)")
	}

	instanceID, _ := cmd.Flags().GetString("instance-id")
	if instanceID == "" {
		return fmt.Errorf("--instance-id is required (EC2 instance ID for ProxyCommand)")
	}

	az, _ := cmd.Flags().GetString("az")
	if az == "" {
		return fmt.Errorf("--az is required (availability zone for EC2 Instance Connect)")
	}

	// ADR-0015: Check permission before writing to ~/.ssh/config.
	configDir := config.DefaultConfigDir()
	cfg, err := config.Load(configDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if !cfg.SSHConfigApproved {
		if !yes {
			return fmt.Errorf(
				"mint needs permission to write to %s (ADR-0015). "+
					"Run with --yes to approve, or set ssh_config_approved=true in mint config",
				sshConfigPath,
			)
		}

		// Store approval so we never prompt again.
		cfg.SSHConfigApproved = true
		if err := config.Save(cfg, configDir); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
		fmt.Fprintf(w, "SSH config write approval stored.\n")
	}

	// Check for hand edits on existing block.
	if data, err := os.ReadFile(sshConfigPath); err == nil {
		if sshconfig.HasHandEdits(string(data), vmName) {
			fmt.Fprintf(w, "Warning: hand-edits detected in managed block for %q. Overwriting.\n", vmName)
		}
	}

	// Generate and write the managed block.
	block := sshconfig.GenerateBlock(vmName, hostname, defaultSSHUser, defaultSSHPort, instanceID, az)
	if err := sshconfig.WriteManagedBlock(sshConfigPath, vmName, block); err != nil {
		return fmt.Errorf("write ssh config: %w", err)
	}

	fmt.Fprintf(w, "SSH config updated for VM %q (Host mint-%s).\n", vmName, vmName)
	return nil
}

func runSSHConfigRemove(cmd *cobra.Command, sshConfigPath, vmName string) error {
	if err := sshconfig.RemoveManagedBlock(sshConfigPath, vmName); err != nil {
		return fmt.Errorf("remove ssh config block: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "SSH config block removed for VM %q.\n", vmName)
	return nil
}

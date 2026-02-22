package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/nicholasgasior/mint/internal/cli"
	"github.com/nicholasgasior/mint/internal/config"
	mintaws "github.com/nicholasgasior/mint/internal/aws"
	"github.com/nicholasgasior/mint/internal/sshconfig"
	"github.com/nicholasgasior/mint/internal/vm"
	"github.com/spf13/cobra"
)

// defaultSSHPort is the non-standard SSH port per ADR-0016.
const defaultSSHPort = 41122

// defaultSSHUser is the default user for Ubuntu 24.04 VMs.
const defaultSSHUser = "ubuntu"

// sshConfigDeps holds the injectable dependencies for the ssh-config command.
// Used by newSSHConfigCommandWithDeps for testing. When nil, the production
// path self-initializes AWS clients only in auto-discover mode.
type sshConfigDeps struct {
	describe mintaws.DescribeInstancesAPI
	owner    string
}

// newSSHConfigCommand creates the production ssh-config command.
func newSSHConfigCommand() *cobra.Command {
	return newSSHConfigCommandWithDeps(nil)
}

// newSSHConfigCommandWithDeps creates the ssh-config command with explicit
// dependencies for testing.
func newSSHConfigCommandWithDeps(deps *sshConfigDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ssh-config",
		Short: "Manage SSH config entries for mint VMs",
		Long: "Generate and manage SSH config Host blocks for mint VMs. " +
			"Managed blocks are marked with # mint:begin/end markers and " +
			"include a SHA256 checksum for hand-edit detection (ADR-0008).\n\n" +
			"Auto-discover from running VM:\n" +
			"  mint ssh-config\n\n" +
			"Explicit values (as called by mint up):\n" +
			"  mint ssh-config --hostname <ip> --instance-id <id> --az <az>",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSSHConfig(cmd, deps)
		},
	}

	cmd.Flags().String("hostname", "", "Public IP or hostname of the VM")
	cmd.Flags().String("instance-id", "", "EC2 instance ID for ProxyCommand")
	cmd.Flags().String("az", "", "Availability zone for EC2 Instance Connect")
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

func runSSHConfig(cmd *cobra.Command, deps *sshConfigDeps) error {
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
	instanceID, _ := cmd.Flags().GetString("instance-id")
	az, _ := cmd.Flags().GetString("az")

	// Determine whether the user is in explicit mode (at least one flag set)
	// or auto-discover mode (all three flags absent).
	explicitMode := hostname != "" || instanceID != "" || az != ""

	if explicitMode {
		// Validate all three are provided when any is given.
		if hostname == "" {
			return fmt.Errorf("--hostname is required when --instance-id or --az are provided\n\nTip: mint ssh-config is called automatically by mint up.\nTo add manually: mint ssh-config --hostname <ip> --instance-id <id> --az <az>")
		}
		if instanceID == "" {
			return fmt.Errorf("--instance-id is required (EC2 instance ID for ProxyCommand)")
		}
		if az == "" {
			return fmt.Errorf("--az is required (availability zone for EC2 Instance Connect)")
		}
	} else {
		// Auto-discover mode: query AWS for the running VM.
		// ssh-config bypasses PersistentPreRunE AWS init (commandNeedsAWS
		// returns false) so we self-initialize clients here, following the
		// same pattern as the doctor command.
		var describe mintaws.DescribeInstancesAPI
		var owner string

		if deps != nil {
			// Test path: use injected dependencies.
			describe = deps.describe
			owner = deps.owner
		} else {
			// Production path: self-initialize AWS clients.
			ctx := cmd.Context()
			clients, err := initAWSClients(ctx)
			if err != nil {
				return fmt.Errorf("initialize AWS for auto-discovery: %w", err)
			}
			describe = clients.ec2Client
			owner = clients.owner
		}

		ctx := cmd.Context()
		found, err := vm.FindVM(ctx, describe, owner, vmName)
		if err != nil {
			return fmt.Errorf("discovering VM: %w", err)
		}
		if found == nil {
			return fmt.Errorf(
				"No running VM found. Provide --hostname, --instance-id, and --az, "+
					"or run 'mint up' first.",
			)
		}
		hostname = found.PublicIP
		instanceID = found.ID
		az = found.AvailabilityZone
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
				"mint needs permission to write to %s (ADR-0015) — "+
					"run with --yes to approve, or set ssh_config_approved=true in mint config",
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
	found, err := sshconfig.RemoveManagedBlock(sshConfigPath, vmName)
	if err != nil {
		return fmt.Errorf("remove ssh config block: %w", err)
	}

	if found {
		fmt.Fprintf(cmd.OutOrStdout(), "SSH config block removed for VM %q.\n", vmName)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "No SSH config block found for VM %q.\n", vmName)
	}
	return nil
}

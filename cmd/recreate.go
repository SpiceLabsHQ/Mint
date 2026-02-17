package cmd

import (
	"bufio"
	"context"
	"fmt"
	"strings"

	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/spf13/cobra"

	mintaws "github.com/nicholasgasior/mint/internal/aws"
	"github.com/nicholasgasior/mint/internal/cli"
	"github.com/nicholasgasior/mint/internal/vm"
)

// recreateDeps holds the injectable dependencies for the recreate command.
type recreateDeps struct {
	describe  mintaws.DescribeInstancesAPI
	sendKey   mintaws.SendSSHPublicKeyAPI
	remoteRun RemoteCommandRunner
	owner     string
}

// newRecreateCommand creates the production recreate command.
func newRecreateCommand() *cobra.Command {
	return newRecreateCommandWithDeps(nil)
}

// newRecreateCommandWithDeps creates the recreate command with explicit
// dependencies for testing. When deps is nil, the command wires real AWS clients.
func newRecreateCommandWithDeps(deps *recreateDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "recreate",
		Short: "Destroy and re-provision the VM with the same configuration",
		Long: "Destroy the current VM and create a fresh one with the same instance type, " +
			"storage, and project configuration. Active sessions are detected and the " +
			"operation is blocked unless --force is used.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps != nil {
				return runRecreate(cmd, deps)
			}
			clients := awsClientsFromContext(cmd.Context())
			if clients == nil {
				return fmt.Errorf("AWS clients not configured")
			}
			return runRecreate(cmd, &recreateDeps{
				describe:  clients.ec2Client,
				sendKey:   clients.icClient,
				remoteRun: defaultRemoteRunner,
				owner:     clients.owner,
			})
		},
	}

	cmd.Flags().Bool("force", false, "Bypass active session guard")

	return cmd
}

// runRecreate executes the recreate command logic: discover VM, check for
// active sessions, confirm with user, then signal readiness for the lifecycle
// sequence (implemented in a separate unit).
func runRecreate(cmd *cobra.Command, deps *recreateDeps) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	cliCtx := cli.FromCommand(cmd)
	vmName := "default"
	verbose := false
	yes := false
	if cliCtx != nil {
		vmName = cliCtx.VM
		verbose = cliCtx.Verbose
		yes = cliCtx.Yes
	}

	force, _ := cmd.Flags().GetBool("force")
	w := cmd.OutOrStdout()

	// Discover VM.
	if verbose {
		fmt.Fprintf(w, "Discovering VM %q for owner %q...\n", vmName, deps.owner)
	}

	found, err := vm.FindVM(ctx, deps.describe, deps.owner, vmName)
	if err != nil {
		return fmt.Errorf("discovering VM: %w", err)
	}
	if found == nil {
		return fmt.Errorf("no VM %q found — run mint up first to create one", vmName)
	}

	// Verify VM is running (session detection requires SSH access).
	state := ec2types.InstanceStateName(found.State)
	if state != ec2types.InstanceStateNameRunning {
		return fmt.Errorf("VM %q is %s — must be running to recreate (need SSH access for session detection)", vmName, found.State)
	}

	// Active session detection: check for tmux clients and SSH/mosh sessions.
	if verbose {
		fmt.Fprintf(w, "Checking for active sessions on VM %q...\n", vmName)
	}

	activeSessions, err := detectActiveSessions(ctx, deps, found)
	if err != nil {
		// Non-fatal: if we can't detect sessions, warn but continue with
		// confirmation. This avoids blocking recreate when SSH is flaky.
		if verbose {
			fmt.Fprintf(w, "Warning: could not detect active sessions: %v\n", err)
		}
	}

	if activeSessions != "" && !force {
		return fmt.Errorf("active sessions detected on VM %q:\n\n%s\n\nUse --force to proceed anyway", vmName, activeSessions)
	}

	if activeSessions != "" && force {
		fmt.Fprintf(w, "Warning: proceeding despite active sessions on VM %q:\n%s\n\n", vmName, activeSessions)
	}

	// Show what will happen.
	fmt.Fprintf(w, "This will destroy and re-provision VM %q (%s).\n", vmName, found.ID)
	fmt.Fprintf(w, "  - Instance %s will be terminated\n", found.ID)
	fmt.Fprintf(w, "  - A new VM will be provisioned with the same configuration\n")
	fmt.Fprintf(w, "  - Project EBS volumes will be preserved if possible\n")

	// Confirmation: require user to type VM name unless --yes is set.
	if !yes {
		fmt.Fprintf(w, "\nType the VM name %q to confirm: ", vmName)
		scanner := bufio.NewScanner(cmd.InOrStdin())
		if scanner.Scan() {
			input := strings.TrimSpace(scanner.Text())
			if input != vmName {
				return fmt.Errorf("confirmation %q does not match VM name %q — recreate aborted", input, vmName)
			}
		} else {
			return fmt.Errorf("no confirmation input received — recreate aborted")
		}
	}

	// Guards passed — the actual lifecycle sequence is implemented in Unit #16.
	fmt.Fprintf(w, "Proceeding with recreate of VM %q (%s)...\n", vmName, found.ID)
	return nil
}

// detectActiveSessions SSHs into the VM and checks for active tmux clients
// and SSH/mosh connections. Returns a human-readable summary of active
// sessions, or empty string if no active sessions are found.
func detectActiveSessions(ctx context.Context, deps *recreateDeps, found *vm.VM) (string, error) {
	// Check tmux clients (attached sessions indicate active users).
	tmuxCmd := []string{
		"tmux", "list-clients", "-F", "#{client_name} #{session_name}",
	}

	var parts []string

	tmuxOutput, err := deps.remoteRun(
		ctx,
		deps.sendKey,
		found.ID,
		found.AvailabilityZone,
		found.PublicIP,
		defaultSSHPort,
		defaultSSHUser,
		tmuxCmd,
	)
	if err != nil {
		// tmux not running or no clients is not an error condition.
		if !isTmuxNoSessionsError(err) {
			return "", fmt.Errorf("checking tmux clients: %w", err)
		}
	} else {
		clients := strings.TrimSpace(string(tmuxOutput))
		if clients != "" {
			parts = append(parts, "  Tmux clients:\n    "+strings.ReplaceAll(clients, "\n", "\n    "))
		}
	}

	// Check SSH/mosh connections (who command shows logged-in users).
	whoCmd := []string{"who"}

	whoOutput, err := deps.remoteRun(
		ctx,
		deps.sendKey,
		found.ID,
		found.AvailabilityZone,
		found.PublicIP,
		defaultSSHPort,
		defaultSSHUser,
		whoCmd,
	)
	if err != nil {
		return "", fmt.Errorf("checking active connections: %w", err)
	}

	connections := strings.TrimSpace(string(whoOutput))
	if connections != "" {
		parts = append(parts, "  Active connections:\n    "+strings.ReplaceAll(connections, "\n", "\n    "))
	}

	return strings.Join(parts, "\n"), nil
}

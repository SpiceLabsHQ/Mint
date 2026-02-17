package cmd

import (
	"context"
	"fmt"

	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/spf13/cobra"

	mintaws "github.com/nicholasgasior/mint/internal/aws"
	"github.com/nicholasgasior/mint/internal/cli"
	"github.com/nicholasgasior/mint/internal/sshconfig"
	"github.com/nicholasgasior/mint/internal/vm"
)

// codeDeps holds the injectable dependencies for the code command.
type codeDeps struct {
	describe      mintaws.DescribeInstancesAPI
	owner         string
	runner        CommandRunner
	sshConfigPath string
}

// newCodeCommand creates the production code command.
func newCodeCommand() *cobra.Command {
	return newCodeCommandWithDeps(nil)
}

// newCodeCommandWithDeps creates the code command with explicit dependencies
// for testing.
func newCodeCommandWithDeps(deps *codeDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "code",
		Short: "Open VS Code connected to the VM",
		Long: "Open VS Code with Remote-SSH connected to the VM. " +
			"Ensures the SSH config entry exists before launching.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps == nil {
				return fmt.Errorf("AWS clients not configured (not yet wired for production use)")
			}
			return runCode(cmd, deps)
		},
	}

	cmd.Flags().String("path", "/home/ubuntu", "Remote directory to open in VS Code")

	return cmd
}

// runCode executes the code command logic: discover VM, verify running,
// ensure SSH config, exec VS Code with --remote.
func runCode(cmd *cobra.Command, deps *codeDeps) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	cliCtx := cli.FromCommand(cmd)
	vmName := "default"
	if cliCtx != nil {
		vmName = cliCtx.VM
	}

	remotePath, _ := cmd.Flags().GetString("path")
	if remotePath == "" {
		remotePath = "/home/ubuntu"
	}

	// Discover VM by owner + VM name.
	found, err := vm.FindVM(ctx, deps.describe, deps.owner, vmName)
	if err != nil {
		return fmt.Errorf("discovering VM: %w", err)
	}
	if found == nil {
		return fmt.Errorf("no VM %q found — run mint up first to create one", vmName)
	}

	// Verify VM is running.
	if found.State != string(ec2types.InstanceStateNameRunning) {
		return fmt.Errorf("VM %q (%s) is not running (state: %s) — run mint up to start it",
			vmName, found.ID, found.State)
	}

	// Ensure SSH config entry exists.
	sshConfigPath := deps.sshConfigPath
	if sshConfigPath == "" {
		sshConfigPath = defaultSSHConfigPath()
	}

	block := sshconfig.GenerateBlock(vmName, found.PublicIP, defaultSSHUser, defaultSSHPort)
	if err := sshconfig.WriteManagedBlock(sshConfigPath, vmName, block); err != nil {
		return fmt.Errorf("write ssh config: %w", err)
	}

	// Build VS Code command: code --remote ssh-remote+mint-<vmName> <path>
	remoteName := fmt.Sprintf("ssh-remote+mint-%s", vmName)

	runner := deps.runner
	if runner == nil {
		runner = defaultRunner
	}

	return runner("code", "--remote", remoteName, remotePath)
}

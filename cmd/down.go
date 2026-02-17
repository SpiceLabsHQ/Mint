package cmd

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/spf13/cobra"

	mintaws "github.com/nicholasgasior/mint/internal/aws"
	"github.com/nicholasgasior/mint/internal/cli"
	"github.com/nicholasgasior/mint/internal/vm"
)

// downDeps holds the injectable dependencies for the down command.
type downDeps struct {
	describe mintaws.DescribeInstancesAPI
	stop     mintaws.StopInstancesAPI
	owner    string
}

// newDownCommand creates the production down command. It will be wired with
// real AWS clients when the full provisioning flow is integrated.
func newDownCommand() *cobra.Command {
	return newDownCommandWithDeps(nil)
}

// newDownCommandWithDeps creates the down command with explicit dependencies
// for testing. When deps is nil, the command will need real AWS clients
// injected before execution (placeholder for future integration).
func newDownCommandWithDeps(deps *downDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Stop the VM instance",
		Long:  "Stop the VM instance. All volumes and Elastic IP persist for next mint up.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps != nil {
				return runDown(cmd, deps)
			}
			clients := awsClientsFromContext(cmd.Context())
			if clients == nil {
				return fmt.Errorf("AWS clients not configured")
			}
			return runDown(cmd, &downDeps{
				describe: clients.ec2Client,
				stop:     clients.ec2Client,
				owner:    clients.owner,
			})
		},
	}
}

// runDown executes the down command logic: discover VM, check state, stop.
func runDown(cmd *cobra.Command, deps *downDeps) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	cliCtx := cli.FromCommand(cmd)
	vmName := "default"
	verbose := false
	if cliCtx != nil {
		vmName = cliCtx.VM
		verbose = cliCtx.Verbose
	}

	w := cmd.OutOrStdout()

	// Discover VM
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

	// Handle already-stopped VM
	if found.State == string(ec2types.InstanceStateNameStopped) ||
		found.State == string(ec2types.InstanceStateNameStopping) {
		fmt.Fprintf(w, "VM %q (%s) is already stopped.\n", vmName, found.ID)
		return nil
	}

	// Stop the instance
	if verbose {
		fmt.Fprintf(w, "Stopping instance %s...\n", found.ID)
	}

	_, err = deps.stop.StopInstances(ctx, &ec2.StopInstancesInput{
		InstanceIds: []string{found.ID},
	})
	if err != nil {
		return fmt.Errorf("stopping instance %s: %w", found.ID, err)
	}

	if verbose {
		fmt.Fprintf(w, "Instance stopped.\n")
	}

	fmt.Fprintf(w, "VM %q (%s) stopped. Volumes and Elastic IP persist.\n", vmName, found.ID)
	return nil
}

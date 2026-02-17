package cmd

import (
	"bufio"
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	mintaws "github.com/nicholasgasior/mint/internal/aws"
	"github.com/nicholasgasior/mint/internal/cli"
	"github.com/nicholasgasior/mint/internal/provision"
	"github.com/nicholasgasior/mint/internal/vm"
)

// destroyDeps holds the injectable dependencies for the destroy command.
type destroyDeps struct {
	describe        mintaws.DescribeInstancesAPI
	terminate       mintaws.TerminateInstancesAPI
	describeVolumes mintaws.DescribeVolumesAPI
	detachVolume    mintaws.DetachVolumeAPI
	deleteVolume    mintaws.DeleteVolumeAPI
	describeAddrs   mintaws.DescribeAddressesAPI
	releaseAddr     mintaws.ReleaseAddressAPI
	owner           string
}

// newDestroyCommand creates the production destroy command. It will be wired
// with real AWS clients when the full provisioning flow is integrated.
func newDestroyCommand() *cobra.Command {
	return newDestroyCommandWithDeps(nil)
}

// newDestroyCommandWithDeps creates the destroy command with explicit
// dependencies for testing.
func newDestroyCommandWithDeps(deps *destroyDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "destroy",
		Short: "Terminate the VM and clean up all associated resources",
		Long: "Terminate the VM instance, delete project EBS volumes, and release " +
			"the Elastic IP. Root EBS is auto-destroyed by EC2. User EFS access " +
			"point is preserved (user-scoped, persistent across VMs).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps != nil {
				return runDestroy(cmd, deps)
			}
			clients := awsClientsFromContext(cmd.Context())
			if clients == nil {
				return fmt.Errorf("AWS clients not configured")
			}
			return runDestroy(cmd, &destroyDeps{
				describe:        clients.ec2Client,
				terminate:       clients.ec2Client,
				describeVolumes: clients.ec2Client,
				detachVolume:    clients.ec2Client,
				deleteVolume:    clients.ec2Client,
				describeAddrs:   clients.ec2Client,
				releaseAddr:     clients.ec2Client,
				owner:           clients.owner,
			})
		},
	}
}

// runDestroy executes the destroy command logic: discover VM, confirm, destroy.
func runDestroy(cmd *cobra.Command, deps *destroyDeps) error {
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

	w := cmd.OutOrStdout()

	// Discover VM to show what will be destroyed.
	if verbose {
		fmt.Fprintf(w, "Discovering VM %q for owner %q...\n", vmName, deps.owner)
	}

	found, err := vm.FindVM(ctx, deps.describe, deps.owner, vmName)
	if err != nil {
		return fmt.Errorf("discovering VM: %w", err)
	}
	if found == nil {
		return fmt.Errorf("no VM %q found — nothing to destroy", vmName)
	}

	// Show what will be destroyed.
	fmt.Fprintf(w, "This will permanently destroy VM %q (%s).\n", vmName, found.ID)
	fmt.Fprintf(w, "  - Instance %s will be terminated (root EBS auto-destroyed)\n", found.ID)
	fmt.Fprintf(w, "  - Project EBS volumes will be deleted\n")
	fmt.Fprintf(w, "  - Elastic IP will be released\n")
	fmt.Fprintf(w, "  - User EFS access point is preserved\n")

	// Confirmation: require user to type VM name unless --yes is set.
	confirmed := yes
	if !yes {
		fmt.Fprintf(w, "\nType the VM name %q to confirm: ", vmName)
		scanner := bufio.NewScanner(cmd.InOrStdin())
		if scanner.Scan() {
			input := strings.TrimSpace(scanner.Text())
			if input != vmName {
				return fmt.Errorf("confirmation %q does not match VM name %q — destroy aborted", input, vmName)
			}
			confirmed = true
		} else {
			return fmt.Errorf("no confirmation input received — destroy aborted")
		}
	}

	// Build Destroyer and run.
	destroyer := provision.NewDestroyer(
		deps.describe,
		deps.terminate,
		deps.describeVolumes,
		deps.detachVolume,
		deps.deleteVolume,
		deps.describeAddrs,
		deps.releaseAddr,
	)

	if verbose {
		fmt.Fprintf(w, "Terminating instance %s...\n", found.ID)
	}

	result, err := destroyer.RunWithResult(ctx, deps.owner, vmName, confirmed)
	if err != nil {
		return err
	}

	// Report results.
	if verbose {
		fmt.Fprintf(w, "Instance terminated.\n")
		if result.VolumesDeleted > 0 {
			fmt.Fprintf(w, "%d project volume(s) deleted.\n", result.VolumesDeleted)
		}
		if result.EIPReleased {
			fmt.Fprintf(w, "Elastic IP released.\n")
		}
	}

	for _, warn := range result.Warnings {
		fmt.Fprintf(w, "Warning: %s\n", warn)
	}

	fmt.Fprintf(w, "VM %q (%s) destroyed.\n", vmName, result.InstanceID)
	return nil
}

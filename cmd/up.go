package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nicholasgasior/mint/internal/cli"
	"github.com/nicholasgasior/mint/internal/provision"
	"github.com/spf13/cobra"
)

// upDeps holds the injectable dependencies for the up command.
type upDeps struct {
	provisioner     *provision.Provisioner
	owner           string
	ownerARN        string
	bootstrapScript []byte
	instanceType    string
	volumeSize      int32
}

// newUpCommand creates the production up command.
func newUpCommand() *cobra.Command {
	return newUpCommandWithDeps(nil)
}

// newUpCommandWithDeps creates the up command with explicit dependencies for testing.
func newUpCommandWithDeps(deps *upDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "up",
		Short: "Provision or start the VM",
		Long: "Provision a new VM or start a stopped one. Creates EC2 instance, " +
			"project EBS volume, and Elastic IP. If a VM already exists and is " +
			"stopped, it will be started.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps == nil {
				return fmt.Errorf("AWS clients not configured (not yet wired for production use)")
			}
			return runUp(cmd, deps)
		},
	}
}

// runUp executes the up command logic.
func runUp(cmd *cobra.Command, deps *upDeps) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	cliCtx := cli.FromCommand(cmd)
	vmName := "default"
	verbose := false
	jsonOutput := false
	if cliCtx != nil {
		vmName = cliCtx.VM
		verbose = cliCtx.Verbose
		jsonOutput = cliCtx.JSON
	}

	w := cmd.OutOrStdout()

	if verbose {
		fmt.Fprintf(w, "Provisioning VM %q for owner %q...\n", vmName, deps.owner)
	}

	cfg := provision.ProvisionConfig{
		InstanceType:    deps.instanceType,
		VolumeSize:      deps.volumeSize,
		BootstrapScript: deps.bootstrapScript,
	}

	result, err := deps.provisioner.Run(ctx, deps.owner, deps.ownerARN, vmName, cfg)
	if err != nil {
		return err
	}

	return printUpResult(cmd, cliCtx, result, jsonOutput, verbose)
}

func printUpResult(cmd *cobra.Command, cliCtx *cli.CLIContext, result *provision.ProvisionResult, jsonOutput, verbose bool) error {
	if jsonOutput {
		return printUpJSON(cmd, result)
	}
	return printUpHuman(cmd, result, verbose)
}

func printUpJSON(cmd *cobra.Command, result *provision.ProvisionResult) error {
	data := map[string]any{
		"instance_id":   result.InstanceID,
		"public_ip":     result.PublicIP,
		"volume_id":     result.VolumeID,
		"allocation_id": result.AllocationID,
		"restarted":     result.Restarted,
	}

	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(data)
}

func printUpHuman(cmd *cobra.Command, result *provision.ProvisionResult, verbose bool) error {
	w := cmd.OutOrStdout()

	if result.Restarted {
		fmt.Fprintf(w, "VM %s restarted.\n", result.InstanceID)
		if result.PublicIP != "" {
			fmt.Fprintf(w, "IP            %s\n", result.PublicIP)
		}
		return nil
	}

	// Fresh provision.
	fmt.Fprintf(w, "Instance      %s\n", result.InstanceID)
	if result.PublicIP != "" {
		fmt.Fprintf(w, "IP            %s\n", result.PublicIP)
	}
	if result.VolumeID != "" {
		fmt.Fprintf(w, "Volume        %s\n", result.VolumeID)
	}
	if result.AllocationID != "" {
		fmt.Fprintf(w, "EIP           %s\n", result.AllocationID)
	}

	fmt.Fprintln(w, "\nVM is provisioning. Bootstrap may take a few minutes.")
	return nil
}

// upWithProvisioner runs up with a pre-built Provisioner (for testing).
func upWithProvisioner(ctx context.Context, cmd *cobra.Command, cliCtx *cli.CLIContext, deps *upDeps, vmName string) error {
	cfg := provision.ProvisionConfig{
		InstanceType:    deps.instanceType,
		VolumeSize:      deps.volumeSize,
		BootstrapScript: deps.bootstrapScript,
	}

	verbose := false
	jsonOutput := false
	if cliCtx != nil {
		verbose = cliCtx.Verbose
		jsonOutput = cliCtx.JSON
	}

	result, err := deps.provisioner.Run(ctx, deps.owner, deps.ownerARN, vmName, cfg)
	if err != nil {
		return err
	}

	return printUpResult(cmd, cliCtx, result, jsonOutput, verbose)
}

package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/spf13/cobra"

	mintaws "github.com/nicholasgasior/mint/internal/aws"
	"github.com/nicholasgasior/mint/internal/cli"
	"github.com/nicholasgasior/mint/internal/progress"
	"github.com/nicholasgasior/mint/internal/vm"
)

// resizeDeps holds the injectable dependencies for the resize command.
type resizeDeps struct {
	describe      mintaws.DescribeInstancesAPI
	describeTypes mintaws.DescribeInstanceTypesAPI
	stop          mintaws.StopInstancesAPI
	waitStopped   mintaws.WaitInstanceStoppedAPI
	modify        mintaws.ModifyInstanceAttributeAPI
	start         mintaws.StartInstancesAPI
	owner         string
	region        string
}

// WithWaitStopped sets the waiter used to poll until the instance reaches the
// stopped state. Call this before runResize to override the default (no-op) waiter.
func (d *resizeDeps) WithWaitStopped(w mintaws.WaitInstanceStoppedAPI) *resizeDeps {
	d.waitStopped = w
	return d
}

// newResizeCommand creates the production resize command.
func newResizeCommand() *cobra.Command {
	return newResizeCommandWithDeps(nil)
}

// newResizeCommandWithDeps creates the resize command with explicit dependencies
// for testing. When deps is nil, the command wires real AWS clients.
func newResizeCommandWithDeps(deps *resizeDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "resize <instance-type>",
		Short: "Change the VM instance type",
		Long: "Stop the VM, change its instance type, and restart it. " +
			"If the VM is already stopped, only the instance type is changed " +
			"(the VM remains stopped).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps != nil {
				return runResize(cmd, deps, args[0])
			}
			clients := awsClientsFromContext(cmd.Context())
			if clients == nil {
				return fmt.Errorf("AWS clients not configured")
			}
			return runResize(cmd, &resizeDeps{
				describe:      clients.ec2Client,
				describeTypes: clients.ec2Client,
				stop:          clients.ec2Client,
				waitStopped:   ec2.NewInstanceStoppedWaiter(clients.ec2Client),
				modify:        clients.ec2Client,
				start:         clients.ec2Client,
				owner:         clients.owner,
				region:        clients.mintConfig.Region,
			}, args[0])
		},
	}
}

// runResize executes the resize command logic: discover VM, validate type,
// stop (if running), modify instance attribute, start (if was running).
func runResize(cmd *cobra.Command, deps *resizeDeps, newType string) error {
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
	sp := progress.NewCommandSpinner(w, verbose)

	// Discover VM.
	sp.Start(fmt.Sprintf("Discovering VM %q for owner %q...", vmName, deps.owner))

	found, err := vm.FindVM(ctx, deps.describe, deps.owner, vmName)
	if err != nil {
		sp.Fail(err.Error())
		return fmt.Errorf("discovering VM: %w", err)
	}
	if found == nil {
		return fmt.Errorf("no VM %q found — run mint up first to create one", vmName)
	}

	// Validate VM state: must be running or stopped.
	state := ec2types.InstanceStateName(found.State)
	if state != ec2types.InstanceStateNameRunning && state != ec2types.InstanceStateNameStopped {
		return fmt.Errorf("VM %q is %s — must be running or stopped to resize", vmName, found.State)
	}

	// Reject no-op resize.
	if found.InstanceType == newType {
		return fmt.Errorf("VM %q is already running instance type %s", vmName, newType)
	}

	// Validate instance type against AWS API.
	sp.Update(fmt.Sprintf("Validating instance type %q...", newType))

	validator := mintaws.NewInstanceTypeValidator(deps.describeTypes)
	if err := validator.Validate(ctx, newType, deps.region); err != nil {
		sp.Fail(err.Error())
		return fmt.Errorf("invalid instance type: %w", err)
	}

	wasRunning := state == ec2types.InstanceStateNameRunning

	// Stop instance if running.
	if wasRunning {
		sp.Update(fmt.Sprintf("Stopping instance %s...", found.ID))
		_, err := deps.stop.StopInstances(ctx, &ec2.StopInstancesInput{
			InstanceIds: []string{found.ID},
		})
		if err != nil {
			sp.Fail(err.Error())
			return fmt.Errorf("stopping instance %s: %w", found.ID, err)
		}

		// Wait for the instance to reach stopped state before modifying its
		// type. EC2 returns IncorrectInstanceState if ModifyInstanceAttribute
		// is called while the instance is still stopping.
		if deps.waitStopped != nil {
			sp.Update(fmt.Sprintf("Waiting for instance %s to stop...", found.ID))
			if err := deps.waitStopped.Wait(ctx, &ec2.DescribeInstancesInput{
				InstanceIds: []string{found.ID},
			}, 10*time.Minute); err != nil {
				sp.Fail(err.Error())
				return fmt.Errorf("waiting for instance %s to stop: %w", found.ID, err)
			}
		}
	}

	// Modify instance type.
	sp.Update(fmt.Sprintf("Modifying instance type to %s...", newType))

	_, err = deps.modify.ModifyInstanceAttribute(ctx, &ec2.ModifyInstanceAttributeInput{
		InstanceId: aws.String(found.ID),
		InstanceType: &ec2types.AttributeValue{
			Value: aws.String(newType),
		},
	})
	if err != nil {
		sp.Fail(err.Error())
		return fmt.Errorf("modifying instance type: %w", err)
	}

	// Restart instance if it was running before.
	if wasRunning {
		sp.Update(fmt.Sprintf("Starting instance %s...", found.ID))
		_, err := deps.start.StartInstances(ctx, &ec2.StartInstancesInput{
			InstanceIds: []string{found.ID},
		})
		if err != nil {
			sp.Fail(err.Error())
			return fmt.Errorf("starting instance %s: %w", found.ID, err)
		}
	}

	// Print the final success message to the command output unconditionally.
	// sp.Stop clears the spinner line in interactive mode before we print.
	sp.Stop("")
	fmt.Fprintf(w, "VM %q (%s) resized to %s.\n", vmName, found.ID, newType)
	return nil
}

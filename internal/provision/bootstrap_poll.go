// Package provision implements the core provisioning logic for Mint.
// This file contains the BootstrapPoller, which polls for bootstrap completion
// after an EC2 instance is launched via "mint up" (ADR-0009).
package provision

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	mintaws "github.com/nicholasgasior/mint/internal/aws"
	"github.com/nicholasgasior/mint/internal/tags"
	"github.com/nicholasgasior/mint/internal/vm"
)

// DefaultPollInterval is the default time between bootstrap status checks.
const DefaultPollInterval = 15 * time.Second

// DefaultPollTimeout is the maximum time to wait for bootstrap completion.
const DefaultPollTimeout = 7 * time.Minute

// PollConfig holds configurable timing for the bootstrap polling loop.
// Tests inject short durations to avoid real sleeping.
type PollConfig struct {
	Interval time.Duration
	Timeout  time.Duration
}

// BootstrapPoller polls an EC2 instance for bootstrap completion and handles
// timeout scenarios with user-interactive recovery options.
type BootstrapPoller struct {
	describeInstances mintaws.DescribeInstancesAPI
	stopInstances     mintaws.StopInstancesAPI
	terminateInstances mintaws.TerminateInstancesAPI
	createTags        mintaws.CreateTagsAPI
	output            io.Writer
	input             io.Reader

	// Config controls poll interval and timeout. Override for testing.
	Config PollConfig
}

// NewBootstrapPoller creates a BootstrapPoller with all required dependencies
// injected. Uses DefaultPollInterval and DefaultPollTimeout by default;
// override via the Config field for testing.
func NewBootstrapPoller(
	describeInstances mintaws.DescribeInstancesAPI,
	stopInstances mintaws.StopInstancesAPI,
	terminateInstances mintaws.TerminateInstancesAPI,
	createTags mintaws.CreateTagsAPI,
	output io.Writer,
	input io.Reader,
) *BootstrapPoller {
	return &BootstrapPoller{
		describeInstances: describeInstances,
		stopInstances:     stopInstances,
		terminateInstances: terminateInstances,
		createTags:        createTags,
		output:            output,
		input:             input,
		Config: PollConfig{
			Interval: DefaultPollInterval,
			Timeout:  DefaultPollTimeout,
		},
	}
}

// Poll checks the instance's mint:bootstrap tag at regular intervals until it
// reads "complete", the timeout expires, or the context is cancelled.
//
// On success (bootstrap=complete), returns nil.
// On bootstrap=failed, returns an error immediately.
// On timeout, presents three interactive options to the user.
// On context cancellation, returns the context error.
func (bp *BootstrapPoller) Poll(ctx context.Context, owner, vmName, instanceID string) error {
	ticker := time.NewTicker(bp.Config.Interval)
	defer ticker.Stop()

	deadline := time.NewTimer(bp.Config.Timeout)
	defer deadline.Stop()

	start := time.Now()

	// Check immediately before the first tick.
	status, err := bp.checkBootstrapStatus(ctx, owner, vmName)
	if err == nil {
		switch status {
		case tags.BootstrapComplete:
			fmt.Fprintln(bp.output, "Bootstrap complete.")
			return nil
		case tags.BootstrapFailed:
			return fmt.Errorf("bootstrap failed on instance %s", instanceID)
		}
	}

	fmt.Fprintf(bp.output, "Waiting for bootstrap... %s\n", formatElapsed(time.Since(start)))

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("bootstrap poll cancelled: %w", ctx.Err())

		case <-deadline.C:
			return bp.handleTimeout(ctx, instanceID)

		case <-ticker.C:
			status, err := bp.checkBootstrapStatus(ctx, owner, vmName)
			if err != nil {
				// Log the error but keep polling; transient API errors shouldn't abort.
				fmt.Fprintf(bp.output, "Waiting for bootstrap... %s (check failed: %v)\n", formatElapsed(time.Since(start)), err)
				continue
			}

			switch status {
			case tags.BootstrapComplete:
				fmt.Fprintln(bp.output, "Bootstrap complete.")
				return nil
			case tags.BootstrapFailed:
				return fmt.Errorf("bootstrap failed on instance %s", instanceID)
			default:
				fmt.Fprintf(bp.output, "Waiting for bootstrap... %s\n", formatElapsed(time.Since(start)))
			}
		}
	}
}

// checkBootstrapStatus uses FindVM to get the current bootstrap tag value.
func (bp *BootstrapPoller) checkBootstrapStatus(ctx context.Context, owner, vmName string) (string, error) {
	found, err := vm.FindVM(ctx, bp.describeInstances, owner, vmName)
	if err != nil {
		return "", fmt.Errorf("checking bootstrap status: %w", err)
	}
	if found == nil {
		return "", fmt.Errorf("VM not found for owner %q, vm %q", owner, vmName)
	}
	return found.BootstrapStatus, nil
}

// handleTimeout presents the user with three options when bootstrap does not
// complete within the timeout window.
func (bp *BootstrapPoller) handleTimeout(ctx context.Context, instanceID string) error {
	fmt.Fprintln(bp.output, "")
	fmt.Fprintln(bp.output, "Bootstrap did not complete within the timeout period.")
	fmt.Fprintln(bp.output, "")
	fmt.Fprintln(bp.output, "What would you like to do?")
	fmt.Fprintln(bp.output, "  1) Stop the instance (can restart later)")
	fmt.Fprintln(bp.output, "  2) Terminate the instance (destroy and clean up)")
	fmt.Fprintln(bp.output, "  3) Leave the instance running (investigate manually)")
	fmt.Fprintln(bp.output, "")
	fmt.Fprint(bp.output, "Choice [1/2/3]: ")

	scanner := bufio.NewScanner(bp.input)
	if !scanner.Scan() {
		return fmt.Errorf("failed to read user input")
	}
	choice := strings.TrimSpace(scanner.Text())

	switch choice {
	case "1":
		fmt.Fprintf(bp.output, "Stopping instance %s...\n", instanceID)
		_, err := bp.stopInstances.StopInstances(ctx, &ec2.StopInstancesInput{
			InstanceIds: []string{instanceID},
		})
		if err != nil {
			return fmt.Errorf("stopping instance %s: %w", instanceID, err)
		}
		fmt.Fprintln(bp.output, "Instance stopped.")
		return nil

	case "2":
		fmt.Fprintf(bp.output, "Terminating instance %s...\n", instanceID)
		_, err := bp.terminateInstances.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
			InstanceIds: []string{instanceID},
		})
		if err != nil {
			return fmt.Errorf("terminating instance %s: %w", instanceID, err)
		}

		_, err = bp.createTags.CreateTags(ctx, &ec2.CreateTagsInput{
			Resources: []string{instanceID},
			Tags: []ec2types.Tag{
				{Key: aws.String(tags.TagBootstrap), Value: aws.String(tags.BootstrapFailed)},
			},
		})
		if err != nil {
			return fmt.Errorf("tagging instance %s as failed: %w", instanceID, err)
		}
		fmt.Fprintln(bp.output, "Instance terminated and tagged as failed.")
		return nil

	case "3":
		fmt.Fprintln(bp.output, "Leaving instance running. SSH in to investigate.")
		return nil

	default:
		return fmt.Errorf("invalid choice %q; expected 1, 2, or 3", choice)
	}
}

// formatElapsed formats a duration as "Xm Ys" for progress output.
func formatElapsed(d time.Duration) string {
	d = d.Round(time.Second)
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

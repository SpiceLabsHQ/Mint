package cmd

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/SpiceLabsHQ/Mint/internal/cli"
	"github.com/spf13/cobra"
)

// ---------------------------------------------------------------------------
// Inline mocks for destroy command tests
// ---------------------------------------------------------------------------

type mockDestroyDescribeInstances struct {
	output *ec2.DescribeInstancesOutput
	err    error
}

func (m *mockDestroyDescribeInstances) DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return m.output, m.err
}

type mockDestroyTerminateInstances struct {
	output *ec2.TerminateInstancesOutput
	err    error
	called bool
}

func (m *mockDestroyTerminateInstances) TerminateInstances(ctx context.Context, params *ec2.TerminateInstancesInput, optFns ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error) {
	m.called = true
	return m.output, m.err
}

type mockDestroyDescribeVolumes struct {
	output *ec2.DescribeVolumesOutput
	err    error
}

func (m *mockDestroyDescribeVolumes) DescribeVolumes(ctx context.Context, params *ec2.DescribeVolumesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error) {
	return m.output, m.err
}

type mockDestroyDetachVolume struct {
	output *ec2.DetachVolumeOutput
	err    error
}

func (m *mockDestroyDetachVolume) DetachVolume(ctx context.Context, params *ec2.DetachVolumeInput, optFns ...func(*ec2.Options)) (*ec2.DetachVolumeOutput, error) {
	return m.output, m.err
}

type mockDestroyDeleteVolume struct {
	output *ec2.DeleteVolumeOutput
	err    error
}

func (m *mockDestroyDeleteVolume) DeleteVolume(ctx context.Context, params *ec2.DeleteVolumeInput, optFns ...func(*ec2.Options)) (*ec2.DeleteVolumeOutput, error) {
	return m.output, m.err
}

type mockDestroyDescribeAddresses struct {
	output *ec2.DescribeAddressesOutput
	err    error
}

func (m *mockDestroyDescribeAddresses) DescribeAddresses(ctx context.Context, params *ec2.DescribeAddressesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeAddressesOutput, error) {
	return m.output, m.err
}

type mockDestroyReleaseAddress struct {
	output *ec2.ReleaseAddressOutput
	err    error
}

func (m *mockDestroyReleaseAddress) ReleaseAddress(ctx context.Context, params *ec2.ReleaseAddressInput, optFns ...func(*ec2.Options)) (*ec2.ReleaseAddressOutput, error) {
	return m.output, m.err
}

type mockDestroyWaitTerminated struct {
	err    error
	called bool
}

func (m *mockDestroyWaitTerminated) Wait(ctx context.Context, params *ec2.DescribeInstancesInput, maxWaitDur time.Duration, optFns ...func(*ec2.InstanceTerminatedWaiterOptions)) error {
	m.called = true
	return m.err
}

// ---------------------------------------------------------------------------
// Helper: create destroyDeps with happy path mocks
// ---------------------------------------------------------------------------

func newHappyDestroyDeps(owner string) *destroyDeps {
	return &destroyDeps{
		describe: &mockDestroyDescribeInstances{
			output: makeRunningInstance("i-abc123", "default", owner),
		},
		terminate: &mockDestroyTerminateInstances{
			output: &ec2.TerminateInstancesOutput{},
		},
		// waitTerminated is nil by default so existing tests run without a waiter.
		waitTerminated: nil,
		describeVolumes: &mockDestroyDescribeVolumes{
			output: &ec2.DescribeVolumesOutput{
				Volumes: []ec2types.Volume{{
					VolumeId: aws.String("vol-proj1"),
					State:    ec2types.VolumeStateAvailable,
				}},
			},
		},
		detachVolume: &mockDestroyDetachVolume{
			output: &ec2.DetachVolumeOutput{},
		},
		deleteVolume: &mockDestroyDeleteVolume{
			output: &ec2.DeleteVolumeOutput{},
		},
		describeAddrs: &mockDestroyDescribeAddresses{
			output: &ec2.DescribeAddressesOutput{
				Addresses: []ec2types.Address{{
					AllocationId: aws.String("eipalloc-abc"),
					PublicIp:     aws.String("1.2.3.4"),
				}},
			},
		},
		releaseAddr: &mockDestroyReleaseAddress{
			output: &ec2.ReleaseAddressOutput{},
		},
		owner: owner,
	}
}

// newDestroyTestRoot creates a test root with --yes flag support and stdin override.
func newDestroyTestRoot(sub *cobra.Command) *cobra.Command {
	root := &cobra.Command{
		Use:           "mint",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			cliCtx := cli.NewCLIContext(cmd)
			cmd.SetContext(cli.WithContext(context.Background(), cliCtx))
			return nil
		},
	}
	root.PersistentFlags().Bool("verbose", false, "Show progress steps")
	root.PersistentFlags().Bool("debug", false, "Show AWS SDK details")
	root.PersistentFlags().Bool("json", false, "Machine-readable JSON output")
	root.PersistentFlags().Bool("yes", false, "Skip confirmation on destructive operations")
	root.PersistentFlags().String("vm", "default", "Target VM name")
	root.AddCommand(sub)
	return root
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestDestroyCommand(t *testing.T) {
	tests := []struct {
		name           string
		deps           *destroyDeps
		args           []string
		stdin          string
		wantErr        bool
		wantErrContain string
		wantOutput     []string
	}{
		{
			name:       "successful destroy with --yes",
			deps:       newHappyDestroyDeps("alice"),
			args:       []string{"destroy", "--yes"},
			wantOutput: []string{"destroyed", "i-abc123"},
		},
		{
			name:       "successful destroy with confirmation prompt",
			deps:       newHappyDestroyDeps("alice"),
			args:       []string{"destroy"},
			stdin:      "default\n",
			wantOutput: []string{"destroyed"},
		},
		{
			name:           "confirmation prompt rejects wrong name",
			deps:           newHappyDestroyDeps("alice"),
			args:           []string{"destroy"},
			stdin:          "wrong-name\n",
			wantErr:        true,
			wantErrContain: "does not match",
		},
		{
			name: "no VM found returns error",
			deps: func() *destroyDeps {
				d := newHappyDestroyDeps("alice")
				d.describe = &mockDestroyDescribeInstances{
					output: &ec2.DescribeInstancesOutput{},
				}
				return d
			}(),
			args:           []string{"destroy", "--yes"},
			wantErr:        true,
			wantErrContain: "no VM",
		},
		{
			name: "verbose shows progress phases",
			deps: newHappyDestroyDeps("alice"),
			args: []string{"destroy", "--yes", "--verbose"},
			wantOutput: []string{
				"Discovering VM",
				"Terminating VM",
			},
		},
		{
			name: "spinner shows termination and wait phases with waiter set",
			deps: func() *destroyDeps {
				d := newHappyDestroyDeps("alice")
				d.waitTerminated = &mockDestroyWaitTerminated{}
				return d
			}(),
			args: []string{"destroy", "--yes", "--verbose"},
			wantOutput: []string{
				"Terminating VM",
				"Waiting for termination",
			},
		},
		{
			name: "spinner shows volume deletion phase when volumes deleted",
			deps: func() *destroyDeps {
				d := newHappyDestroyDeps("alice")
				d.waitTerminated = &mockDestroyWaitTerminated{}
				return d
			}(),
			args: []string{"destroy", "--yes", "--verbose"},
			wantOutput: []string{
				"Terminating VM",
				"Deleting volume",
			},
		},
		{
			name: "shows what will be destroyed before confirming",
			deps: newHappyDestroyDeps("alice"),
			args: []string{"destroy"},
			stdin: "default\n",
			wantOutput: []string{
				"i-abc123",
				"default",
			},
		},
		{
			name: "non-default vm name",
			deps: func() *destroyDeps {
				d := newHappyDestroyDeps("bob")
				d.describe = &mockDestroyDescribeInstances{
					output: makeRunningInstance("i-dev456", "dev", "bob"),
				}
				return d
			}(),
			args:       []string{"destroy", "--vm", "dev", "--yes"},
			wantOutput: []string{"destroyed"},
		},
		{
			name: "describe error propagates",
			deps: func() *destroyDeps {
				d := newHappyDestroyDeps("alice")
				d.describe = &mockDestroyDescribeInstances{
					err: fmt.Errorf("API throttled"),
				}
				return d
			}(),
			args:           []string{"destroy", "--yes"},
			wantErr:        true,
			wantErrContain: "API throttled",
		},
		{
			name: "terminate error propagates",
			deps: func() *destroyDeps {
				d := newHappyDestroyDeps("alice")
				d.terminate = &mockDestroyTerminateInstances{
					err: fmt.Errorf("insufficient permissions"),
				}
				return d
			}(),
			args:           []string{"destroy", "--yes"},
			wantErr:        true,
			wantErrContain: "insufficient permissions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := new(bytes.Buffer)
			cmd := newDestroyCommandWithDeps(tt.deps)
			root := newDestroyTestRoot(cmd)
			root.SetOut(buf)
			root.SetErr(buf)

			if tt.stdin != "" {
				root.SetIn(strings.NewReader(tt.stdin))
			}

			root.SetArgs(tt.args)
			err := root.Execute()

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.wantErrContain != "" && !strings.Contains(err.Error(), tt.wantErrContain) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.wantErrContain)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			output := buf.String()
			for _, want := range tt.wantOutput {
				if !strings.Contains(output, want) {
					t.Errorf("output missing %q, got: %s", want, output)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Tests: WaitInstanceTerminated wiring in destroy command
// ---------------------------------------------------------------------------

// TestDestroyCommandWaiterCalled verifies that when a waitTerminated is set
// in destroyDeps, it is invoked during command execution.
func TestDestroyCommandWaiterCalled(t *testing.T) {
	deps := newHappyDestroyDeps("alice")
	waiter := &mockDestroyWaitTerminated{}
	deps.waitTerminated = waiter

	buf := new(bytes.Buffer)
	cmd := newDestroyCommandWithDeps(deps)
	root := newDestroyTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"destroy", "--yes"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !waiter.called {
		t.Fatal("WaitInstanceTerminated was not called by the destroy command")
	}
}

// TestDestroyCommandWaiterErrorPropagates verifies that a waiter error causes
// the destroy command to return an error (volume cleanup is skipped).
func TestDestroyCommandWaiterErrorPropagates(t *testing.T) {
	deps := newHappyDestroyDeps("alice")
	deps.waitTerminated = &mockDestroyWaitTerminated{
		err: fmt.Errorf("instance did not reach terminated state"),
	}

	buf := new(bytes.Buffer)
	cmd := newDestroyCommandWithDeps(deps)
	root := newDestroyTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"destroy", "--yes"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error when waiter fails, got nil")
	}
	if !strings.Contains(err.Error(), "instance did not reach terminated state") {
		t.Errorf("error %q does not contain expected message", err.Error())
	}
}

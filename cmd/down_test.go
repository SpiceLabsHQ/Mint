package cmd

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/nicholasgasior/mint/internal/cli"
	"github.com/spf13/cobra"
)

// mockDescribeInstances implements mintaws.DescribeInstancesAPI for testing.
type mockDescribeInstances struct {
	output *ec2.DescribeInstancesOutput
	err    error
}

func (m *mockDescribeInstances) DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return m.output, m.err
}

// mockStopInstances implements mintaws.StopInstancesAPI for testing.
type mockStopInstances struct {
	output *ec2.StopInstancesOutput
	err    error
	called bool
	input  *ec2.StopInstancesInput
}

func (m *mockStopInstances) StopInstances(ctx context.Context, params *ec2.StopInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StopInstancesOutput, error) {
	m.called = true
	m.input = params
	return m.output, m.err
}

// makeRunningInstance returns a DescribeInstancesOutput containing one running instance.
func makeRunningInstance(id, vmName, owner string) *ec2.DescribeInstancesOutput {
	return &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{
			Instances: []ec2types.Instance{{
				InstanceId:   aws.String(id),
				InstanceType: ec2types.InstanceTypeT3Medium,
				State: &ec2types.InstanceState{
					Name: ec2types.InstanceStateNameRunning,
				},
				Tags: []ec2types.Tag{
					{Key: aws.String("mint:vm"), Value: aws.String(vmName)},
					{Key: aws.String("mint:owner"), Value: aws.String(owner)},
				},
			}},
		}},
	}
}

// makeStoppedInstance returns a DescribeInstancesOutput containing one stopped instance.
func makeStoppedInstance(id, vmName, owner string) *ec2.DescribeInstancesOutput {
	return &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{
			Instances: []ec2types.Instance{{
				InstanceId:   aws.String(id),
				InstanceType: ec2types.InstanceTypeT3Medium,
				State: &ec2types.InstanceState{
					Name: ec2types.InstanceStateNameStopped,
				},
				Tags: []ec2types.Tag{
					{Key: aws.String("mint:vm"), Value: aws.String(vmName)},
					{Key: aws.String("mint:owner"), Value: aws.String(owner)},
				},
			}},
		}},
	}
}

// newTestRoot creates a minimal root command with global flags and
// PersistentPreRunE for CLI context propagation, without registering
// any default subcommands. This avoids collisions when injecting
// test commands.
func newTestRoot() *cobra.Command {
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
	return root
}

func TestDownCommand(t *testing.T) {
	tests := []struct {
		name           string
		describe       *mockDescribeInstances
		stop           *mockStopInstances
		owner          string
		vmName         string
		verbose        bool
		wantErr        bool
		wantErrContain string
		wantOutput     []string
		wantStopCalled bool
	}{
		{
			name: "successful stop of running instance",
			describe: &mockDescribeInstances{
				output: makeRunningInstance("i-abc123", "default", "alice"),
			},
			stop: &mockStopInstances{
				output: &ec2.StopInstancesOutput{},
			},
			owner:          "alice",
			vmName:         "default",
			wantStopCalled: true,
			wantOutput:     []string{"stopped"},
		},
		{
			name: "vm not found suggests mint up",
			describe: &mockDescribeInstances{
				output: &ec2.DescribeInstancesOutput{},
			},
			stop:           &mockStopInstances{},
			owner:          "alice",
			vmName:         "default",
			wantErr:        true,
			wantErrContain: "mint up",
			wantStopCalled: false,
		},
		{
			name: "already stopped vm shows informational message",
			describe: &mockDescribeInstances{
				output: makeStoppedInstance("i-abc123", "default", "alice"),
			},
			stop:           &mockStopInstances{},
			owner:          "alice",
			vmName:         "default",
			wantStopCalled: false,
			wantOutput:     []string{"already stopped"},
		},
		{
			name: "describe API error propagates",
			describe: &mockDescribeInstances{
				err: fmt.Errorf("throttled"),
			},
			stop:           &mockStopInstances{},
			owner:          "alice",
			vmName:         "default",
			wantErr:        true,
			wantErrContain: "throttled",
			wantStopCalled: false,
		},
		{
			name: "stop API error propagates",
			describe: &mockDescribeInstances{
				output: makeRunningInstance("i-abc123", "default", "alice"),
			},
			stop: &mockStopInstances{
				err: fmt.Errorf("insufficient permissions"),
			},
			owner:          "alice",
			vmName:         "default",
			wantErr:        true,
			wantErrContain: "insufficient permissions",
			wantStopCalled: true,
		},
		{
			name: "verbose shows progress steps",
			describe: &mockDescribeInstances{
				output: makeRunningInstance("i-abc123", "default", "alice"),
			},
			stop: &mockStopInstances{
				output: &ec2.StopInstancesOutput{},
			},
			owner:          "alice",
			vmName:         "default",
			verbose:        true,
			wantStopCalled: true,
			wantOutput:     []string{"Discovering VM", "Stopping instance", "i-abc123"},
		},
		{
			name: "stop sends correct instance ID to non-default vm",
			describe: &mockDescribeInstances{
				output: makeRunningInstance("i-specific456", "dev", "bob"),
			},
			stop: &mockStopInstances{
				output: &ec2.StopInstancesOutput{},
			},
			owner:          "bob",
			vmName:         "dev",
			wantStopCalled: true,
			wantOutput:     []string{"stopped"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := new(bytes.Buffer)

			deps := &downDeps{
				describe: tt.describe,
				stop:     tt.stop,
				owner:    tt.owner,
			}

			cmd := newDownCommandWithDeps(deps)
			root := newTestRoot()
			root.AddCommand(cmd)
			root.SetOut(buf)
			root.SetErr(buf)

			args := []string{"down"}
			if tt.vmName != "" && tt.vmName != "default" {
				args = append(args, "--vm", tt.vmName)
			}
			if tt.verbose {
				args = append(args, "--verbose")
			}
			root.SetArgs(args)

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

			if tt.wantStopCalled != tt.stop.called {
				t.Errorf("StopInstances called = %v, want %v", tt.stop.called, tt.wantStopCalled)
			}

			// Verify correct instance ID was sent to StopInstances
			if tt.stop.called && tt.stop.input != nil {
				if len(tt.stop.input.InstanceIds) != 1 {
					t.Fatalf("expected 1 instance ID in stop call, got %d", len(tt.stop.input.InstanceIds))
				}
				expectedID := aws.ToString(tt.describe.output.Reservations[0].Instances[0].InstanceId)
				if tt.stop.input.InstanceIds[0] != expectedID {
					t.Errorf("StopInstances called with %q, want %q", tt.stop.input.InstanceIds[0], expectedID)
				}
			}
		})
	}
}

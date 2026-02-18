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

// ---------------------------------------------------------------------------
// Inline mocks for resize command tests
// ---------------------------------------------------------------------------

type mockResizeDescribeInstances struct {
	output *ec2.DescribeInstancesOutput
	err    error
}

func (m *mockResizeDescribeInstances) DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return m.output, m.err
}

type mockResizeDescribeInstanceTypes struct {
	output *ec2.DescribeInstanceTypesOutput
	err    error
}

func (m *mockResizeDescribeInstanceTypes) DescribeInstanceTypes(ctx context.Context, params *ec2.DescribeInstanceTypesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstanceTypesOutput, error) {
	return m.output, m.err
}

type mockResizeStopInstances struct {
	output *ec2.StopInstancesOutput
	err    error
	called bool
	input  *ec2.StopInstancesInput
}

func (m *mockResizeStopInstances) StopInstances(ctx context.Context, params *ec2.StopInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StopInstancesOutput, error) {
	m.called = true
	m.input = params
	return m.output, m.err
}

type mockResizeModifyInstanceAttribute struct {
	output *ec2.ModifyInstanceAttributeOutput
	err    error
	called bool
	input  *ec2.ModifyInstanceAttributeInput
}

func (m *mockResizeModifyInstanceAttribute) ModifyInstanceAttribute(ctx context.Context, params *ec2.ModifyInstanceAttributeInput, optFns ...func(*ec2.Options)) (*ec2.ModifyInstanceAttributeOutput, error) {
	m.called = true
	m.input = params
	return m.output, m.err
}

type mockResizeStartInstances struct {
	output *ec2.StartInstancesOutput
	err    error
	called bool
	input  *ec2.StartInstancesInput
}

func (m *mockResizeStartInstances) StartInstances(ctx context.Context, params *ec2.StartInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error) {
	m.called = true
	m.input = params
	return m.output, m.err
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func makeResizeInstance(id, vmName, owner, instanceType string, state ec2types.InstanceStateName) *ec2.DescribeInstancesOutput {
	return &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{
			Instances: []ec2types.Instance{{
				InstanceId:   aws.String(id),
				InstanceType: ec2types.InstanceType(instanceType),
				State: &ec2types.InstanceState{
					Name: state,
				},
				Tags: []ec2types.Tag{
					{Key: aws.String("mint:vm"), Value: aws.String(vmName)},
					{Key: aws.String("mint:owner"), Value: aws.String(owner)},
				},
			}},
		}},
	}
}

func validInstanceTypeOutput() *ec2.DescribeInstanceTypesOutput {
	return &ec2.DescribeInstanceTypesOutput{
		InstanceTypes: []ec2types.InstanceTypeInfo{
			{InstanceType: ec2types.InstanceTypeM6iXlarge},
		},
	}
}

func newResizeTestRoot(sub *cobra.Command) *cobra.Command {
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

func newHappyResizeDeps(owner string) *resizeDeps {
	return &resizeDeps{
		describe:      &mockResizeDescribeInstances{output: makeResizeInstance("i-abc123", "default", owner, "t3.medium", ec2types.InstanceStateNameRunning)},
		describeTypes: &mockResizeDescribeInstanceTypes{output: validInstanceTypeOutput()},
		stop:          &mockResizeStopInstances{output: &ec2.StopInstancesOutput{}},
		modify:        &mockResizeModifyInstanceAttribute{output: &ec2.ModifyInstanceAttributeOutput{}},
		start:         &mockResizeStartInstances{output: &ec2.StartInstancesOutput{}},
		owner:         owner,
		region:        "us-west-2",
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestResizeCommand(t *testing.T) {
	tests := []struct {
		name           string
		deps           *resizeDeps
		args           []string
		wantErr        bool
		wantErrContain string
		wantOutput     []string
		wantStopCalled bool
		wantModify     bool
		wantStart      bool
	}{
		{
			name:           "successful resize of running instance",
			deps:           newHappyResizeDeps("alice"),
			args:           []string{"resize", "m6i.xlarge"},
			wantOutput:     []string{"resized", "m6i.xlarge"},
			wantStopCalled: true,
			wantModify:     true,
			wantStart:      true,
		},
		{
			name: "resize stopped instance skips stop and start",
			deps: func() *resizeDeps {
				d := newHappyResizeDeps("alice")
				d.describe = &mockResizeDescribeInstances{
					output: makeResizeInstance("i-abc123", "default", "alice", "t3.medium", ec2types.InstanceStateNameStopped),
				}
				return d
			}(),
			args:           []string{"resize", "m6i.xlarge"},
			wantOutput:     []string{"resized", "m6i.xlarge"},
			wantStopCalled: false,
			wantModify:     true,
			wantStart:      false,
		},
		{
			name:           "missing instance type argument",
			deps:           newHappyResizeDeps("alice"),
			args:           []string{"resize"},
			wantErr:        true,
			wantErrContain: "accepts 1 arg",
		},
		{
			name: "rejects resize to same instance type",
			deps: newHappyResizeDeps("alice"),
			args: []string{"resize", "t3.medium"},
			wantErr:        true,
			wantErrContain: "already running",
		},
		{
			name: "rejects resize when VM not found",
			deps: func() *resizeDeps {
				d := newHappyResizeDeps("alice")
				d.describe = &mockResizeDescribeInstances{
					output: &ec2.DescribeInstancesOutput{},
				}
				return d
			}(),
			args:           []string{"resize", "m6i.xlarge"},
			wantErr:        true,
			wantErrContain: "no VM",
		},
		{
			name: "rejects resize when VM in pending state",
			deps: func() *resizeDeps {
				d := newHappyResizeDeps("alice")
				d.describe = &mockResizeDescribeInstances{
					output: makeResizeInstance("i-abc123", "default", "alice", "t3.medium", ec2types.InstanceStateNamePending),
				}
				return d
			}(),
			args:           []string{"resize", "m6i.xlarge"},
			wantErr:        true,
			wantErrContain: "must be running or stopped",
		},
		{
			name: "rejects resize when VM is stopping",
			deps: func() *resizeDeps {
				d := newHappyResizeDeps("alice")
				d.describe = &mockResizeDescribeInstances{
					output: makeResizeInstance("i-abc123", "default", "alice", "t3.medium", ec2types.InstanceStateNameStopping),
				}
				return d
			}(),
			args:           []string{"resize", "m6i.xlarge"},
			wantErr:        true,
			wantErrContain: "must be running or stopped",
		},
		{
			name: "rejects invalid instance type",
			deps: func() *resizeDeps {
				d := newHappyResizeDeps("alice")
				d.describeTypes = &mockResizeDescribeInstanceTypes{
					output: &ec2.DescribeInstanceTypesOutput{
						InstanceTypes: []ec2types.InstanceTypeInfo{},
					},
				}
				return d
			}(),
			args:           []string{"resize", "z99.nonexistent"},
			wantErr:        true,
			wantErrContain: "z99.nonexistent",
		},
		{
			name: "describe API error propagates",
			deps: func() *resizeDeps {
				d := newHappyResizeDeps("alice")
				d.describe = &mockResizeDescribeInstances{
					err: fmt.Errorf("API throttled"),
				}
				return d
			}(),
			args:           []string{"resize", "m6i.xlarge"},
			wantErr:        true,
			wantErrContain: "API throttled",
		},
		{
			name: "stop API error propagates",
			deps: func() *resizeDeps {
				d := newHappyResizeDeps("alice")
				d.stop = &mockResizeStopInstances{
					err: fmt.Errorf("stop failed"),
				}
				return d
			}(),
			args:           []string{"resize", "m6i.xlarge"},
			wantErr:        true,
			wantErrContain: "stop failed",
		},
		{
			name: "modify API error propagates",
			deps: func() *resizeDeps {
				d := newHappyResizeDeps("alice")
				d.modify = &mockResizeModifyInstanceAttribute{
					err: fmt.Errorf("modify denied"),
				}
				return d
			}(),
			args:           []string{"resize", "m6i.xlarge"},
			wantErr:        true,
			wantErrContain: "modify denied",
		},
		{
			name: "start API error propagates",
			deps: func() *resizeDeps {
				d := newHappyResizeDeps("alice")
				d.start = &mockResizeStartInstances{
					err: fmt.Errorf("start failed"),
				}
				return d
			}(),
			args:           []string{"resize", "m6i.xlarge"},
			wantErr:        true,
			wantErrContain: "start failed",
		},
		{
			name: "verbose shows progress steps",
			deps: newHappyResizeDeps("alice"),
			args: []string{"resize", "m6i.xlarge", "--verbose"},
			wantOutput: []string{
				"Discovering VM",
				"Validating instance type",
				"Stopping instance",
				"Modifying instance type",
				"Starting instance",
			},
			wantStopCalled: true,
			wantModify:     true,
			wantStart:      true,
		},
		{
			name: "non-default VM name",
			deps: func() *resizeDeps {
				d := newHappyResizeDeps("bob")
				d.describe = &mockResizeDescribeInstances{
					output: makeResizeInstance("i-dev456", "dev", "bob", "t3.medium", ec2types.InstanceStateNameRunning),
				}
				return d
			}(),
			args:           []string{"resize", "m6i.xlarge", "--vm", "dev"},
			wantOutput:     []string{"resized"},
			wantStopCalled: true,
			wantModify:     true,
			wantStart:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := new(bytes.Buffer)
			cmd := newResizeCommandWithDeps(tt.deps)
			root := newResizeTestRoot(cmd)
			root.SetOut(buf)
			root.SetErr(buf)
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

			// Verify mock call expectations
			if stop, ok := tt.deps.stop.(*mockResizeStopInstances); ok {
				if stop.called != tt.wantStopCalled {
					t.Errorf("StopInstances called = %v, want %v", stop.called, tt.wantStopCalled)
				}
			}
			if modify, ok := tt.deps.modify.(*mockResizeModifyInstanceAttribute); ok {
				if modify.called != tt.wantModify {
					t.Errorf("ModifyInstanceAttribute called = %v, want %v", modify.called, tt.wantModify)
				}
			}
			if start, ok := tt.deps.start.(*mockResizeStartInstances); ok {
				if start.called != tt.wantStart {
					t.Errorf("StartInstances called = %v, want %v", start.called, tt.wantStart)
				}
			}
		})
	}
}

func TestResizeModifySendsCorrectInstanceType(t *testing.T) {
	deps := newHappyResizeDeps("alice")
	modify := deps.modify.(*mockResizeModifyInstanceAttribute)

	buf := new(bytes.Buffer)
	cmd := newResizeCommandWithDeps(deps)
	root := newResizeTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"resize", "c5.2xlarge"})

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !modify.called {
		t.Fatal("ModifyInstanceAttribute was not called")
	}

	if modify.input.InstanceId == nil || *modify.input.InstanceId != "i-abc123" {
		t.Errorf("modify called with instance ID %v, want i-abc123", modify.input.InstanceId)
	}

	if modify.input.InstanceType == nil || modify.input.InstanceType.Value == nil || *modify.input.InstanceType.Value != "c5.2xlarge" {
		t.Error("modify did not set correct instance type to c5.2xlarge")
	}
}

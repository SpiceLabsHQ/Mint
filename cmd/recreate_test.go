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
	"github.com/aws/aws-sdk-go-v2/service/ec2instanceconnect"
	mintaws "github.com/nicholasgasior/mint/internal/aws"
	"github.com/nicholasgasior/mint/internal/cli"
	"github.com/nicholasgasior/mint/internal/provision"
	"github.com/spf13/cobra"
)

// ---------------------------------------------------------------------------
// Inline mocks for recreate command tests
// ---------------------------------------------------------------------------

type mockRecreateDescribeInstances struct {
	output *ec2.DescribeInstancesOutput
	err    error
}

func (m *mockRecreateDescribeInstances) DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return m.output, m.err
}

type mockRecreateSendSSHPublicKey struct {
	output *ec2instanceconnect.SendSSHPublicKeyOutput
	err    error
}

func (m *mockRecreateSendSSHPublicKey) SendSSHPublicKey(ctx context.Context, params *ec2instanceconnect.SendSSHPublicKeyInput, optFns ...func(*ec2instanceconnect.Options)) (*ec2instanceconnect.SendSSHPublicKeyOutput, error) {
	return m.output, m.err
}

// mockRecreateRemoteRunner returns a RemoteCommandRunner that yields different
// output based on the command being run (tmux, who, docker, cat).
type mockRecreateRemoteRunner struct {
	tmuxOutput   []byte
	tmuxErr      error
	whoOutput    []byte
	whoErr       error
	dockerPsOut  []byte
	dockerPsErr  error
	dockerTopOut map[string][]byte
	dockerTopErr map[string]error
	catExtendOut []byte
	catExtendErr error
}

func (m *mockRecreateRemoteRunner) run(
	ctx context.Context,
	sendKey mintaws.SendSSHPublicKeyAPI,
	instanceID, az, host string,
	port int,
	user string,
	command []string,
) ([]byte, error) {
	if len(command) > 0 && command[0] == "tmux" {
		return m.tmuxOutput, m.tmuxErr
	}
	if len(command) > 0 && command[0] == "who" {
		return m.whoOutput, m.whoErr
	}
	if len(command) >= 2 && command[0] == "docker" && command[1] == "ps" {
		return m.dockerPsOut, m.dockerPsErr
	}
	if len(command) >= 3 && command[0] == "docker" && command[1] == "top" {
		containerID := command[2]
		if m.dockerTopErr != nil {
			if err, ok := m.dockerTopErr[containerID]; ok {
				return nil, err
			}
		}
		if m.dockerTopOut != nil {
			if out, ok := m.dockerTopOut[containerID]; ok {
				return out, nil
			}
		}
		return nil, fmt.Errorf("no mock for docker top %s", containerID)
	}
	if len(command) >= 2 && command[0] == "cat" && strings.Contains(command[1], "idle-extended-until") {
		return m.catExtendOut, m.catExtendErr
	}
	return nil, fmt.Errorf("unexpected command: %v", command)
}

// Lifecycle mocks for the 8-step recreate sequence.

type mockDescribeVolumes struct {
	output *ec2.DescribeVolumesOutput
	err    error
}

func (m *mockDescribeVolumes) DescribeVolumes(ctx context.Context, params *ec2.DescribeVolumesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error) {
	return m.output, m.err
}

type mockRecreateStopInstances struct {
	output *ec2.StopInstancesOutput
	err    error
}

func (m *mockRecreateStopInstances) StopInstances(ctx context.Context, params *ec2.StopInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StopInstancesOutput, error) {
	return m.output, m.err
}

type mockTerminateInstances struct {
	output *ec2.TerminateInstancesOutput
	err    error
}

func (m *mockTerminateInstances) TerminateInstances(ctx context.Context, params *ec2.TerminateInstancesInput, optFns ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error) {
	return m.output, m.err
}

type mockDetachVolume struct {
	output *ec2.DetachVolumeOutput
	err    error
}

func (m *mockDetachVolume) DetachVolume(ctx context.Context, params *ec2.DetachVolumeInput, optFns ...func(*ec2.Options)) (*ec2.DetachVolumeOutput, error) {
	return m.output, m.err
}

type mockAttachVolume struct {
	output *ec2.AttachVolumeOutput
	err    error
}

func (m *mockAttachVolume) AttachVolume(ctx context.Context, params *ec2.AttachVolumeInput, optFns ...func(*ec2.Options)) (*ec2.AttachVolumeOutput, error) {
	return m.output, m.err
}

type mockRunInstances struct {
	output *ec2.RunInstancesOutput
	err    error
	// captured stores the last RunInstancesInput for assertions.
	captured *ec2.RunInstancesInput
}

func (m *mockRunInstances) RunInstances(ctx context.Context, params *ec2.RunInstancesInput, optFns ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error) {
	m.captured = params
	return m.output, m.err
}

type mockCreateTags struct {
	calls []*ec2.CreateTagsInput
	err   error
	// failOnCall makes the Nth call (1-indexed) return an error.
	failOnCall int
	callCount  int
}

func (m *mockCreateTags) CreateTags(ctx context.Context, params *ec2.CreateTagsInput, optFns ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error) {
	m.callCount++
	m.calls = append(m.calls, params)
	if m.failOnCall > 0 && m.callCount == m.failOnCall {
		return nil, m.err
	}
	if m.failOnCall == 0 && m.err != nil {
		return nil, m.err
	}
	return &ec2.CreateTagsOutput{}, nil
}

type mockDeleteTags struct {
	calls []*ec2.DeleteTagsInput
	err   error
}

func (m *mockDeleteTags) DeleteTags(ctx context.Context, params *ec2.DeleteTagsInput, optFns ...func(*ec2.Options)) (*ec2.DeleteTagsOutput, error) {
	m.calls = append(m.calls, params)
	if m.err != nil {
		return nil, m.err
	}
	return &ec2.DeleteTagsOutput{}, nil
}

type mockDescribeSubnets struct {
	output *ec2.DescribeSubnetsOutput
	err    error
}

func (m *mockDescribeSubnets) DescribeSubnets(ctx context.Context, params *ec2.DescribeSubnetsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error) {
	return m.output, m.err
}

type mockDescribeSecurityGroups struct {
	// outputs maps component tag values to their responses.
	outputs map[string]*ec2.DescribeSecurityGroupsOutput
	err     error
}

func (m *mockDescribeSecurityGroups) DescribeSecurityGroups(ctx context.Context, params *ec2.DescribeSecurityGroupsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error) {
	if m.err != nil {
		return nil, m.err
	}
	// Find which component is being queried.
	for _, f := range params.Filters {
		if aws.ToString(f.Name) == "tag:mint:component" && len(f.Values) > 0 {
			if out, ok := m.outputs[f.Values[0]]; ok {
				return out, nil
			}
		}
	}
	// Fallback: return empty.
	return &ec2.DescribeSecurityGroupsOutput{}, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func makeRunningInstanceForRecreate(id, vmName, owner, ip, az string) *ec2.DescribeInstancesOutput {
	return &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{
			Instances: []ec2types.Instance{{
				InstanceId:      aws.String(id),
				InstanceType:    ec2types.InstanceTypeT3Medium,
				PublicIpAddress: aws.String(ip),
				State: &ec2types.InstanceState{
					Name: ec2types.InstanceStateNameRunning,
				},
				Placement: &ec2types.Placement{
					AvailabilityZone: aws.String(az),
				},
				Tags: []ec2types.Tag{
					{Key: aws.String("mint:vm"), Value: aws.String(vmName)},
					{Key: aws.String("mint:owner"), Value: aws.String(owner)},
				},
			}},
		}},
	}
}

func makeInstanceWithState(id, vmName, owner string, state ec2types.InstanceStateName) *ec2.DescribeInstancesOutput {
	return &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{
			Instances: []ec2types.Instance{{
				InstanceId:   aws.String(id),
				InstanceType: ec2types.InstanceTypeT3Medium,
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

func newRecreateTestRoot(sub *cobra.Command) *cobra.Command {
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

func noSessionsRunner() *mockRecreateRemoteRunner {
	return &mockRecreateRemoteRunner{
		tmuxOutput:   nil,
		tmuxErr:      fmt.Errorf("no server running on /tmp/tmux-1000/default"),
		whoOutput:    []byte(""),
		whoErr:       nil,
		dockerPsOut:  nil,
		dockerPsErr:  fmt.Errorf("docker: command not found"),
		catExtendOut: nil,
		catExtendErr: fmt.Errorf("No such file or directory"),
	}
}

func activeSessionsRunner() *mockRecreateRemoteRunner {
	return &mockRecreateRemoteRunner{
		tmuxOutput:   []byte("/dev/pts/0 main\n"),
		tmuxErr:      nil,
		whoOutput:    []byte("ec2-user pts/0        2025-01-15 10:30 (192.168.1.100)\n"),
		whoErr:       nil,
		dockerPsOut:  nil,
		dockerPsErr:  fmt.Errorf("docker: command not found"),
		catExtendOut: nil,
		catExtendErr: fmt.Errorf("No such file or directory"),
	}
}

func defaultLifecycleMocks() lifecycleMocks {
	return lifecycleMocks{
		describeVolumes: &mockDescribeVolumes{
			output: &ec2.DescribeVolumesOutput{
				Volumes: []ec2types.Volume{{
					VolumeId:         aws.String("vol-proj123"),
					AvailabilityZone: aws.String("us-east-1a"),
				}},
			},
		},
		stop:      &mockRecreateStopInstances{output: &ec2.StopInstancesOutput{}},
		terminate: &mockTerminateInstances{output: &ec2.TerminateInstancesOutput{}},
		detach:    &mockDetachVolume{output: &ec2.DetachVolumeOutput{}},
		attach:    &mockAttachVolume{output: &ec2.AttachVolumeOutput{}},
		run: &mockRunInstances{
			output: &ec2.RunInstancesOutput{
				Instances: []ec2types.Instance{{
					InstanceId: aws.String("i-new789"),
				}},
			},
		},
		createTags: &mockCreateTags{},
		deleteTags: &mockDeleteTags{},
		subnets: &mockDescribeSubnets{
			output: &ec2.DescribeSubnetsOutput{
				Subnets: []ec2types.Subnet{{
					SubnetId:         aws.String("subnet-abc"),
					AvailabilityZone: aws.String("us-east-1a"),
				}},
			},
		},
		sgs: &mockDescribeSecurityGroups{
			outputs: map[string]*ec2.DescribeSecurityGroupsOutput{
				"security-group": {
					SecurityGroups: []ec2types.SecurityGroup{{
						GroupId: aws.String("sg-user123"),
					}},
				},
				"admin": {
					SecurityGroups: []ec2types.SecurityGroup{{
						GroupId: aws.String("sg-admin456"),
					}},
				},
			},
		},
	}
}

type lifecycleMocks struct {
	describeVolumes *mockDescribeVolumes
	stop            *mockRecreateStopInstances
	terminate       *mockTerminateInstances
	detach          *mockDetachVolume
	attach          *mockAttachVolume
	run             *mockRunInstances
	createTags      *mockCreateTags
	deleteTags      *mockDeleteTags
	subnets         *mockDescribeSubnets
	sgs             *mockDescribeSecurityGroups
}

func newHappyRecreateDeps(owner string) *recreateDeps {
	runner := noSessionsRunner()
	lm := defaultLifecycleMocks()
	return &recreateDeps{
		describe:        &mockRecreateDescribeInstances{output: makeRunningInstanceForRecreate("i-abc123", "default", owner, "1.2.3.4", "us-east-1a")},
		sendKey:         &mockRecreateSendSSHPublicKey{output: &ec2instanceconnect.SendSSHPublicKeyOutput{}},
		remoteRun:       runner.run,
		owner:           owner,
		ownerARN:        "arn:aws:iam::123456789012:user/" + owner,
		describeVolumes: lm.describeVolumes,
		stop:            lm.stop,
		terminate:       lm.terminate,
		detachVolume:    lm.detach,
		attachVolume:    lm.attach,
		run:             lm.run,
		createTags:      lm.createTags,
		deleteTags:      lm.deleteTags,
		describeSubnets: lm.subnets,
		describeSGs:     lm.sgs,
		bootstrapScript: []byte("#!/bin/bash\necho hello"),
		resolveAMI: func(ctx context.Context, client mintaws.GetParameterAPI) (string, error) {
			return "ami-test123", nil
		},
	}
}

func newHappyRecreateDepsWithMocks(owner string, lm lifecycleMocks) *recreateDeps {
	runner := noSessionsRunner()
	return &recreateDeps{
		describe:        &mockRecreateDescribeInstances{output: makeRunningInstanceForRecreate("i-abc123", "default", owner, "1.2.3.4", "us-east-1a")},
		sendKey:         &mockRecreateSendSSHPublicKey{output: &ec2instanceconnect.SendSSHPublicKeyOutput{}},
		remoteRun:       runner.run,
		owner:           owner,
		ownerARN:        "arn:aws:iam::123456789012:user/" + owner,
		describeVolumes: lm.describeVolumes,
		stop:            lm.stop,
		terminate:       lm.terminate,
		detachVolume:    lm.detach,
		attachVolume:    lm.attach,
		run:             lm.run,
		createTags:      lm.createTags,
		deleteTags:      lm.deleteTags,
		describeSubnets: lm.subnets,
		describeSGs:     lm.sgs,
		bootstrapScript: []byte("#!/bin/bash\necho hello"),
		resolveAMI: func(ctx context.Context, client mintaws.GetParameterAPI) (string, error) {
			return "ami-test123", nil
		},
	}
}

// ---------------------------------------------------------------------------
// Tests — Guards (existing scaffold tests)
// ---------------------------------------------------------------------------

func TestRecreateCommand(t *testing.T) {
	tests := []struct {
		name           string
		deps           *recreateDeps
		args           []string
		stdin          string
		wantErr        bool
		wantErrContain string
		wantOutput     []string
	}{
		{
			name:       "successful recreate with --yes and no active sessions",
			deps:       newHappyRecreateDeps("alice"),
			args:       []string{"recreate", "--yes"},
			wantOutput: []string{"Proceeding with recreate", "i-abc123", "Recreate complete", "i-new789"},
		},
		{
			name:       "successful recreate with confirmation prompt",
			deps:       newHappyRecreateDeps("alice"),
			args:       []string{"recreate"},
			stdin:      "default\n",
			wantOutput: []string{"Proceeding with recreate", "Recreate complete"},
		},
		{
			name:           "confirmation prompt rejects wrong name",
			deps:           newHappyRecreateDeps("alice"),
			args:           []string{"recreate"},
			stdin:          "wrong-name\n",
			wantErr:        true,
			wantErrContain: "does not match",
		},
		{
			name:           "no confirmation input aborts",
			deps:           newHappyRecreateDeps("alice"),
			args:           []string{"recreate"},
			stdin:          "",
			wantErr:        true,
			wantErrContain: "no confirmation input received",
		},
		{
			name: "VM not found returns error",
			deps: func() *recreateDeps {
				d := newHappyRecreateDeps("alice")
				d.describe = &mockRecreateDescribeInstances{
					output: &ec2.DescribeInstancesOutput{},
				}
				return d
			}(),
			args:           []string{"recreate", "--yes"},
			wantErr:        true,
			wantErrContain: "no VM",
		},
		{
			name: "VM in stopped state returns error",
			deps: func() *recreateDeps {
				d := newHappyRecreateDeps("alice")
				d.describe = &mockRecreateDescribeInstances{
					output: makeInstanceWithState("i-abc123", "default", "alice", ec2types.InstanceStateNameStopped),
				}
				return d
			}(),
			args:           []string{"recreate", "--yes"},
			wantErr:        true,
			wantErrContain: "must be running",
		},
		{
			name: "VM in pending state returns error",
			deps: func() *recreateDeps {
				d := newHappyRecreateDeps("alice")
				d.describe = &mockRecreateDescribeInstances{
					output: makeInstanceWithState("i-abc123", "default", "alice", ec2types.InstanceStateNamePending),
				}
				return d
			}(),
			args:           []string{"recreate", "--yes"},
			wantErr:        true,
			wantErrContain: "must be running",
		},
		{
			name: "active sessions block without --force",
			deps: func() *recreateDeps {
				runner := activeSessionsRunner()
				d := newHappyRecreateDeps("alice")
				d.remoteRun = runner.run
				return d
			}(),
			args:           []string{"recreate", "--yes"},
			wantErr:        true,
			wantErrContain: "active sessions detected",
		},
		{
			name: "active sessions with --force proceeds with warning",
			deps: func() *recreateDeps {
				runner := activeSessionsRunner()
				d := newHappyRecreateDeps("alice")
				d.remoteRun = runner.run
				return d
			}(),
			args:       []string{"recreate", "--yes", "--force"},
			wantOutput: []string{"Warning: proceeding despite active sessions", "Proceeding with recreate", "Recreate complete"},
		},
		{
			name: "describe API error propagates",
			deps: func() *recreateDeps {
				d := newHappyRecreateDeps("alice")
				d.describe = &mockRecreateDescribeInstances{
					err: fmt.Errorf("API throttled"),
				}
				return d
			}(),
			args:           []string{"recreate", "--yes"},
			wantErr:        true,
			wantErrContain: "API throttled",
		},
		{
			name: "verbose shows progress steps",
			deps: newHappyRecreateDeps("alice"),
			args: []string{"recreate", "--yes", "--verbose"},
			wantOutput: []string{
				"Discovering VM",
				"Checking for active sessions",
				"Proceeding with recreate",
				"Step 1/8",
				"Step 2/8",
				"Step 3/8",
				"Step 4/8",
				"Step 5/8",
				"Step 6/8",
				"Step 7/8",
				"Step 8/8",
				"Recreate complete",
			},
		},
		{
			name: "non-default VM name",
			deps: func() *recreateDeps {
				runner := noSessionsRunner()
				lm := defaultLifecycleMocks()
				return &recreateDeps{
					describe:        &mockRecreateDescribeInstances{output: makeRunningInstanceForRecreate("i-dev456", "dev", "bob", "5.6.7.8", "us-west-2a")},
					sendKey:         &mockRecreateSendSSHPublicKey{output: &ec2instanceconnect.SendSSHPublicKeyOutput{}},
					remoteRun:       runner.run,
					owner:           "bob",
					ownerARN:        "arn:aws:iam::123456789012:user/bob",
					describeVolumes: lm.describeVolumes,
					stop:            lm.stop,
					terminate:       lm.terminate,
					detachVolume:    lm.detach,
					attachVolume:    lm.attach,
					run:             lm.run,
					createTags:      lm.createTags,
					deleteTags:      lm.deleteTags,
					describeSubnets: lm.subnets,
					describeSGs:     lm.sgs,
					bootstrapScript: []byte("#!/bin/bash\necho hello"),
					resolveAMI: func(ctx context.Context, client mintaws.GetParameterAPI) (string, error) {
						return "ami-test123", nil
					},
				}
			}(),
			args:       []string{"recreate", "--vm", "dev", "--yes"},
			wantOutput: []string{"Proceeding with recreate", "Recreate complete"},
		},
		{
			name: "non-default VM name confirmation requires correct name",
			deps: func() *recreateDeps {
				runner := noSessionsRunner()
				lm := defaultLifecycleMocks()
				return &recreateDeps{
					describe:        &mockRecreateDescribeInstances{output: makeRunningInstanceForRecreate("i-dev456", "dev", "bob", "5.6.7.8", "us-west-2a")},
					sendKey:         &mockRecreateSendSSHPublicKey{output: &ec2instanceconnect.SendSSHPublicKeyOutput{}},
					remoteRun:       runner.run,
					owner:           "bob",
					ownerARN:        "arn:aws:iam::123456789012:user/bob",
					describeVolumes: lm.describeVolumes,
					stop:            lm.stop,
					terminate:       lm.terminate,
					detachVolume:    lm.detach,
					attachVolume:    lm.attach,
					run:             lm.run,
					createTags:      lm.createTags,
					deleteTags:      lm.deleteTags,
					describeSubnets: lm.subnets,
					describeSGs:     lm.sgs,
					bootstrapScript: []byte("#!/bin/bash\necho hello"),
					resolveAMI: func(ctx context.Context, client mintaws.GetParameterAPI) (string, error) {
						return "ami-test123", nil
					},
				}
			}(),
			args:       []string{"recreate", "--vm", "dev"},
			stdin:      "dev\n",
			wantOutput: []string{"Proceeding with recreate", "Recreate complete"},
		},
		{
			name:  "shows what will be destroyed before confirming",
			deps:  newHappyRecreateDeps("alice"),
			args:  []string{"recreate"},
			stdin: "default\n",
			wantOutput: []string{
				"destroy and re-provision",
				"i-abc123",
			},
		},
		{
			name: "session detection failure is non-fatal in verbose mode",
			deps: func() *recreateDeps {
				runner := &mockRecreateRemoteRunner{
					tmuxOutput:   nil,
					tmuxErr:      fmt.Errorf("connection refused"),
					whoOutput:    nil,
					whoErr:       fmt.Errorf("connection refused"),
					dockerPsOut:  nil,
					dockerPsErr:  fmt.Errorf("connection refused"),
					catExtendOut: nil,
					catExtendErr: fmt.Errorf("connection refused"),
				}
				d := newHappyRecreateDeps("alice")
				d.remoteRun = runner.run
				return d
			}(),
			args: []string{"recreate", "--yes", "--verbose"},
			// Session detection error is non-fatal; command should proceed.
			wantOutput: []string{"Warning: could not detect active sessions", "Proceeding with recreate"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := new(bytes.Buffer)
			cmd := newRecreateCommandWithDeps(tt.deps)
			root := newRecreateTestRoot(cmd)
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

func TestRecreateForceFlag(t *testing.T) {
	// Verify --force is a local flag on recreate, not a persistent global flag.
	cmd := newRecreateCommandWithDeps(newHappyRecreateDeps("alice"))
	f := cmd.Flags().Lookup("force")
	if f == nil {
		t.Fatal("expected --force flag to be registered on recreate command")
	}
	if f.DefValue != "false" {
		t.Errorf("--force default value = %q, want %q", f.DefValue, "false")
	}
}

// ---------------------------------------------------------------------------
// Tests — Lifecycle (8-step recreate sequence)
// ---------------------------------------------------------------------------

func TestRecreateLifecycleHappyPath(t *testing.T) {
	lm := defaultLifecycleMocks()
	deps := newHappyRecreateDepsWithMocks("alice", lm)

	buf := new(bytes.Buffer)
	cmd := newRecreateCommandWithDeps(deps)
	root := newRecreateTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"recreate", "--yes"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()

	// Verify lifecycle completed.
	if !strings.Contains(output, "Recreate complete") {
		t.Errorf("output missing 'Recreate complete', got: %s", output)
	}
	if !strings.Contains(output, "i-new789") {
		t.Errorf("output missing new instance ID 'i-new789', got: %s", output)
	}

	// Verify pending-attach tag was set via CreateTags then cleared via DeleteTags.
	if len(lm.createTags.calls) < 1 {
		t.Fatalf("expected at least 1 CreateTags call, got %d", len(lm.createTags.calls))
	}

	// First CreateTags call: set pending-attach=true on the volume.
	setCall := lm.createTags.calls[0]
	if len(setCall.Resources) != 1 || setCall.Resources[0] != "vol-proj123" {
		t.Errorf("pending-attach set call resource = %v, want [vol-proj123]", setCall.Resources)
	}
	foundPendingSet := false
	for _, tag := range setCall.Tags {
		if aws.ToString(tag.Key) == "mint:pending-attach" && aws.ToString(tag.Value) == "true" {
			foundPendingSet = true
		}
	}
	if !foundPendingSet {
		t.Error("first CreateTags call did not set mint:pending-attach=true")
	}

	// DeleteTags call: remove pending-attach tag key entirely.
	if len(lm.deleteTags.calls) < 1 {
		t.Fatalf("expected at least 1 DeleteTags call, got %d", len(lm.deleteTags.calls))
	}
	delCall := lm.deleteTags.calls[0]
	if len(delCall.Resources) != 1 || delCall.Resources[0] != "vol-proj123" {
		t.Errorf("pending-attach delete call resource = %v, want [vol-proj123]", delCall.Resources)
	}
	foundPendingDelete := false
	for _, tag := range delCall.Tags {
		if aws.ToString(tag.Key) == "mint:pending-attach" {
			foundPendingDelete = true
		}
	}
	if !foundPendingDelete {
		t.Error("DeleteTags call did not target mint:pending-attach key")
	}

	// Verify the RunInstances input has correct AZ (via subnet in same AZ).
	if lm.run.captured == nil {
		t.Fatal("RunInstances was not called")
	}
	if aws.ToString(lm.run.captured.SubnetId) != "subnet-abc" {
		t.Errorf("RunInstances subnet = %q, want %q", aws.ToString(lm.run.captured.SubnetId), "subnet-abc")
	}
}

func TestRecreateLifecycleVolumeNotFound(t *testing.T) {
	lm := defaultLifecycleMocks()
	lm.describeVolumes = &mockDescribeVolumes{
		output: &ec2.DescribeVolumesOutput{Volumes: []ec2types.Volume{}},
	}
	deps := newHappyRecreateDepsWithMocks("alice", lm)

	buf := new(bytes.Buffer)
	cmd := newRecreateCommandWithDeps(deps)
	root := newRecreateTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"recreate", "--yes"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for missing volume, got nil")
	}
	if !strings.Contains(err.Error(), "no project volume found") {
		t.Errorf("error %q does not contain 'no project volume found'", err.Error())
	}
}

func TestRecreateLifecycleDescribeVolumesFails(t *testing.T) {
	lm := defaultLifecycleMocks()
	lm.describeVolumes = &mockDescribeVolumes{
		err: fmt.Errorf("describe volumes throttled"),
	}
	deps := newHappyRecreateDepsWithMocks("alice", lm)

	buf := new(bytes.Buffer)
	cmd := newRecreateCommandWithDeps(deps)
	root := newRecreateTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"recreate", "--yes"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "describe volumes throttled") {
		t.Errorf("error %q does not contain expected message", err.Error())
	}
}

func TestRecreateLifecycleStopFails(t *testing.T) {
	lm := defaultLifecycleMocks()
	lm.stop = &mockRecreateStopInstances{err: fmt.Errorf("stop instance timeout")}
	deps := newHappyRecreateDepsWithMocks("alice", lm)

	buf := new(bytes.Buffer)
	cmd := newRecreateCommandWithDeps(deps)
	root := newRecreateTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"recreate", "--yes"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "stopping instance") {
		t.Errorf("error %q does not contain 'stopping instance'", err.Error())
	}
	// Verify pending-attach was already set before the failure.
	if len(lm.createTags.calls) < 1 {
		t.Error("pending-attach tag should have been set before stop failed")
	}
}

func TestRecreateLifecycleDetachFails(t *testing.T) {
	lm := defaultLifecycleMocks()
	lm.detach = &mockDetachVolume{err: fmt.Errorf("volume still in-use")}
	deps := newHappyRecreateDepsWithMocks("alice", lm)

	buf := new(bytes.Buffer)
	cmd := newRecreateCommandWithDeps(deps)
	root := newRecreateTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"recreate", "--yes"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "detaching project volume") {
		t.Errorf("error %q does not contain 'detaching project volume'", err.Error())
	}
}

func TestRecreateLifecycleTerminateFails(t *testing.T) {
	lm := defaultLifecycleMocks()
	lm.terminate = &mockTerminateInstances{err: fmt.Errorf("terminate denied")}
	deps := newHappyRecreateDepsWithMocks("alice", lm)

	buf := new(bytes.Buffer)
	cmd := newRecreateCommandWithDeps(deps)
	root := newRecreateTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"recreate", "--yes"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "terminating instance") {
		t.Errorf("error %q does not contain 'terminating instance'", err.Error())
	}
}

func TestRecreateLifecycleLaunchFails(t *testing.T) {
	lm := defaultLifecycleMocks()
	lm.run = &mockRunInstances{err: fmt.Errorf("insufficient capacity")}
	deps := newHappyRecreateDepsWithMocks("alice", lm)

	buf := new(bytes.Buffer)
	cmd := newRecreateCommandWithDeps(deps)
	root := newRecreateTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"recreate", "--yes"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "launching new instance") {
		t.Errorf("error %q does not contain 'launching new instance'", err.Error())
	}
}

func TestRecreateLifecycleAttachFails(t *testing.T) {
	lm := defaultLifecycleMocks()
	lm.attach = &mockAttachVolume{err: fmt.Errorf("attach volume conflict")}
	deps := newHappyRecreateDepsWithMocks("alice", lm)

	buf := new(bytes.Buffer)
	cmd := newRecreateCommandWithDeps(deps)
	root := newRecreateTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"recreate", "--yes"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "attaching project volume") {
		t.Errorf("error %q does not contain 'attaching project volume'", err.Error())
	}
}

func TestRecreateLifecyclePendingAttachTagSetBeforeStop(t *testing.T) {
	// Verify that the pending-attach tag is set BEFORE the stop call.
	// If stop fails, the pending-attach tag should already be in place.
	lm := defaultLifecycleMocks()
	lm.stop = &mockRecreateStopInstances{err: fmt.Errorf("stop failed")}
	deps := newHappyRecreateDepsWithMocks("alice", lm)

	buf := new(bytes.Buffer)
	cmd := newRecreateCommandWithDeps(deps)
	root := newRecreateTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"recreate", "--yes"})

	_ = root.Execute() // Will fail at stop step.

	// The CreateTags call for pending-attach should have happened.
	if len(lm.createTags.calls) < 1 {
		t.Fatal("expected CreateTags call for pending-attach before stop")
	}
	pendingCall := lm.createTags.calls[0]
	foundPending := false
	for _, tag := range pendingCall.Tags {
		if aws.ToString(tag.Key) == "mint:pending-attach" {
			foundPending = true
		}
	}
	if !foundPending {
		t.Error("first CreateTags call should set pending-attach tag")
	}
}

func TestRecreateLifecyclePendingAttachTagRemovedAfterAttach(t *testing.T) {
	lm := defaultLifecycleMocks()
	deps := newHappyRecreateDepsWithMocks("alice", lm)

	buf := new(bytes.Buffer)
	cmd := newRecreateCommandWithDeps(deps)
	root := newRecreateTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"recreate", "--yes"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the DeleteTags call removes the pending-attach tag.
	if len(lm.deleteTags.calls) < 1 {
		t.Fatalf("expected at least 1 DeleteTags call, got %d", len(lm.deleteTags.calls))
	}

	delCall := lm.deleteTags.calls[0]
	foundClear := false
	for _, tag := range delCall.Tags {
		if aws.ToString(tag.Key) == "mint:pending-attach" {
			foundClear = true
		}
	}
	if !foundClear {
		t.Error("pending-attach tag was not removed via DeleteTags after attach")
	}
}

func TestRecreateLifecycleSameAZ(t *testing.T) {
	// Verify the new instance is launched in the same AZ as the project volume.
	lm := defaultLifecycleMocks()
	lm.describeVolumes = &mockDescribeVolumes{
		output: &ec2.DescribeVolumesOutput{
			Volumes: []ec2types.Volume{{
				VolumeId:         aws.String("vol-proj123"),
				AvailabilityZone: aws.String("us-west-2b"),
			}},
		},
	}
	lm.subnets = &mockDescribeSubnets{
		output: &ec2.DescribeSubnetsOutput{
			Subnets: []ec2types.Subnet{{
				SubnetId:         aws.String("subnet-west2b"),
				AvailabilityZone: aws.String("us-west-2b"),
			}},
		},
	}
	deps := newHappyRecreateDepsWithMocks("alice", lm)

	buf := new(bytes.Buffer)
	cmd := newRecreateCommandWithDeps(deps)
	root := newRecreateTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"recreate", "--yes"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if lm.run.captured == nil {
		t.Fatal("RunInstances was not called")
	}
	if aws.ToString(lm.run.captured.SubnetId) != "subnet-west2b" {
		t.Errorf("RunInstances subnet = %q, want %q (same AZ as volume)", aws.ToString(lm.run.captured.SubnetId), "subnet-west2b")
	}
}

func TestRecreateLifecycleVerboseOutput(t *testing.T) {
	deps := newHappyRecreateDeps("alice")

	buf := new(bytes.Buffer)
	cmd := newRecreateCommandWithDeps(deps)
	root := newRecreateTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"recreate", "--yes", "--verbose"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	steps := []string{
		"Step 1/8: Querying project EBS volume",
		"Found project volume vol-proj123",
		"Step 2/8: Tagging project volume with pending-attach",
		"Step 3/8: Stopping instance i-abc123",
		"Step 4/8: Detaching project volume vol-proj123",
		"Step 5/8: Terminating instance i-abc123",
		"Step 6/8: Launching new instance in us-east-1a",
		"Launched new instance i-new789",
		"Step 7/8: Attaching project volume vol-proj123 to i-new789",
		"Step 8/8: Waiting for bootstrap to complete",
		"Recreate complete. New instance: i-new789",
	}
	for _, step := range steps {
		if !strings.Contains(output, step) {
			t.Errorf("output missing %q, got:\n%s", step, output)
		}
	}
}

func TestRecreateLifecycleBootstrapPollError(t *testing.T) {
	lm := defaultLifecycleMocks()
	deps := newHappyRecreateDepsWithMocks("alice", lm)
	deps.pollBootstrap = func(ctx context.Context, owner, vmName, instanceID string) error {
		return fmt.Errorf("bootstrap timed out")
	}

	buf := new(bytes.Buffer)
	cmd := newRecreateCommandWithDeps(deps)
	root := newRecreateTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"recreate", "--yes"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error from bootstrap poll, got nil")
	}
	if !strings.Contains(err.Error(), "bootstrap timed out") {
		t.Errorf("error %q does not contain 'bootstrap timed out'", err.Error())
	}
}

func TestRecreateLifecycleBootstrapPollSuccess(t *testing.T) {
	lm := defaultLifecycleMocks()
	deps := newHappyRecreateDepsWithMocks("alice", lm)

	var polledOwner, polledVM, polledInstance string
	deps.pollBootstrap = func(ctx context.Context, owner, vmName, instanceID string) error {
		polledOwner = owner
		polledVM = vmName
		polledInstance = instanceID
		return nil
	}

	buf := new(bytes.Buffer)
	cmd := newRecreateCommandWithDeps(deps)
	root := newRecreateTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"recreate", "--yes"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if polledOwner != "alice" {
		t.Errorf("pollBootstrap owner = %q, want %q", polledOwner, "alice")
	}
	if polledVM != "default" {
		t.Errorf("pollBootstrap vmName = %q, want %q", polledVM, "default")
	}
	if polledInstance != "i-new789" {
		t.Errorf("pollBootstrap instanceID = %q, want %q", polledInstance, "i-new789")
	}
}

func TestRecreateLifecyclePendingAttachClearFailureIsNonFatal(t *testing.T) {
	// If removing the pending-attach tag fails, the recreate should still succeed
	// (the volume is attached; the tag is a safety net for crash recovery).
	lm := defaultLifecycleMocks()
	lm.deleteTags = &mockDeleteTags{
		err: fmt.Errorf("tag cleanup failed"),
	}
	deps := newHappyRecreateDepsWithMocks("alice", lm)

	buf := new(bytes.Buffer)
	cmd := newRecreateCommandWithDeps(deps)
	root := newRecreateTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"recreate", "--yes"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("expected no error (tag cleanup is non-fatal), got: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Warning: could not remove pending-attach tag") {
		t.Errorf("expected warning about tag cleanup failure, got:\n%s", output)
	}
	if !strings.Contains(output, "Recreate complete") {
		t.Errorf("output missing 'Recreate complete', got:\n%s", output)
	}
}

func TestRecreateLifecyclePendingAttachSetFailure(t *testing.T) {
	// If setting the pending-attach tag fails, the recreate should abort
	// (the safety net must be in place before destructive actions).
	lm := defaultLifecycleMocks()
	lm.createTags = &mockCreateTags{
		failOnCall: 1, // First call (the set) fails.
		err:        fmt.Errorf("cannot tag volume"),
	}
	deps := newHappyRecreateDepsWithMocks("alice", lm)

	buf := new(bytes.Buffer)
	cmd := newRecreateCommandWithDeps(deps)
	root := newRecreateTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"recreate", "--yes"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error when pending-attach tag set fails, got nil")
	}
	if !strings.Contains(err.Error(), "tagging project volume with pending-attach") {
		t.Errorf("error %q does not contain expected message", err.Error())
	}
}

func TestRecreateLifecycleAMIResolutionFails(t *testing.T) {
	lm := defaultLifecycleMocks()
	deps := newHappyRecreateDepsWithMocks("alice", lm)
	deps.resolveAMI = func(ctx context.Context, client mintaws.GetParameterAPI) (string, error) {
		return "", fmt.Errorf("SSM parameter not found")
	}

	buf := new(bytes.Buffer)
	cmd := newRecreateCommandWithDeps(deps)
	root := newRecreateTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"recreate", "--yes"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error from AMI resolution, got nil")
	}
	if !strings.Contains(err.Error(), "SSM parameter not found") {
		t.Errorf("error %q does not contain expected message", err.Error())
	}
}

func TestRecreateLifecycleSecurityGroupNotFound(t *testing.T) {
	lm := defaultLifecycleMocks()
	lm.sgs = &mockDescribeSecurityGroups{
		outputs: map[string]*ec2.DescribeSecurityGroupsOutput{
			"security-group": {SecurityGroups: []ec2types.SecurityGroup{}}, // empty
			"admin":          {SecurityGroups: []ec2types.SecurityGroup{{GroupId: aws.String("sg-admin456")}}},
		},
	}
	deps := newHappyRecreateDepsWithMocks("alice", lm)

	buf := new(bytes.Buffer)
	cmd := newRecreateCommandWithDeps(deps)
	root := newRecreateTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"recreate", "--yes"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for missing security group, got nil")
	}
	if !strings.Contains(err.Error(), "no security group found") {
		t.Errorf("error %q does not contain expected message", err.Error())
	}
}

func TestRecreateLifecycleSubnetNotFound(t *testing.T) {
	lm := defaultLifecycleMocks()
	lm.subnets = &mockDescribeSubnets{
		output: &ec2.DescribeSubnetsOutput{Subnets: []ec2types.Subnet{}},
	}
	deps := newHappyRecreateDepsWithMocks("alice", lm)

	buf := new(bytes.Buffer)
	cmd := newRecreateCommandWithDeps(deps)
	root := newRecreateTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"recreate", "--yes"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for missing subnet, got nil")
	}
	if !strings.Contains(err.Error(), "no default subnet found") {
		t.Errorf("error %q does not contain expected message", err.Error())
	}
}

func TestRecreateLifecycleBootstrapVerifyCalled(t *testing.T) {
	// Verify that verifyBootstrap is invoked during the lifecycle.
	// If it returns an error, the launch should fail.
	lm := defaultLifecycleMocks()
	deps := newHappyRecreateDepsWithMocks("alice", lm)

	verifyCalled := false
	deps.verifyBootstrap = func(content []byte) error {
		verifyCalled = true
		return nil
	}

	buf := new(bytes.Buffer)
	cmd := newRecreateCommandWithDeps(deps)
	root := newRecreateTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"recreate", "--yes"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !verifyCalled {
		t.Fatal("verifyBootstrap was not called during lifecycle execution")
	}
}

func TestRecreateLifecycleBootstrapVerifyRejectsScript(t *testing.T) {
	// When verifyBootstrap returns an error, the recreate must abort
	// before launching a new instance.
	lm := defaultLifecycleMocks()
	deps := newHappyRecreateDepsWithMocks("alice", lm)
	deps.verifyBootstrap = func(content []byte) error {
		return fmt.Errorf("SHA-256 hash mismatch: script has been tampered with")
	}

	buf := new(bytes.Buffer)
	cmd := newRecreateCommandWithDeps(deps)
	root := newRecreateTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"recreate", "--yes"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error from bootstrap verification, got nil")
	}
	if !strings.Contains(err.Error(), "bootstrap verification failed") {
		t.Errorf("error %q does not contain 'bootstrap verification failed'", err.Error())
	}
	if !strings.Contains(err.Error(), "SHA-256 hash mismatch") {
		t.Errorf("error %q does not contain original verification error", err.Error())
	}

	// RunInstances should NOT have been called.
	if lm.run.captured != nil {
		t.Error("RunInstances was called despite bootstrap verification failure")
	}
}

// ---------------------------------------------------------------------------
// Tests — TOFU host key reset
// ---------------------------------------------------------------------------

func TestRecreateLifecycleRemovesHostKey(t *testing.T) {
	// After successful recreate, removeHostKey should be called with the VM name.
	lm := defaultLifecycleMocks()
	deps := newHappyRecreateDepsWithMocks("alice", lm)

	var removedVM string
	deps.removeHostKey = func(vmName string) error {
		removedVM = vmName
		return nil
	}

	buf := new(bytes.Buffer)
	cmd := newRecreateCommandWithDeps(deps)
	root := newRecreateTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"recreate", "--yes"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if removedVM != "default" {
		t.Errorf("removeHostKey called with %q, want %q", removedVM, "default")
	}
}

func TestRecreateLifecycleHostKeyRemovalError(t *testing.T) {
	// When removeHostKey returns an error, the recreate should fail.
	lm := defaultLifecycleMocks()
	deps := newHappyRecreateDepsWithMocks("alice", lm)

	deps.removeHostKey = func(vmName string) error {
		return fmt.Errorf("known_hosts permission denied")
	}

	buf := new(bytes.Buffer)
	cmd := newRecreateCommandWithDeps(deps)
	root := newRecreateTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"recreate", "--yes"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error from host key removal, got nil")
	}
	if !strings.Contains(err.Error(), "clearing cached host key") {
		t.Errorf("error %q does not contain 'clearing cached host key'", err.Error())
	}
	if !strings.Contains(err.Error(), "known_hosts permission denied") {
		t.Errorf("error %q does not contain original error message", err.Error())
	}
}

func TestRecreateLifecycleHostKeyNotRemovedOnBootstrapFailure(t *testing.T) {
	// When bootstrap polling fails, removeHostKey should NOT be called.
	lm := defaultLifecycleMocks()
	deps := newHappyRecreateDepsWithMocks("alice", lm)

	deps.pollBootstrap = func(ctx context.Context, owner, vmName, instanceID string) error {
		return fmt.Errorf("bootstrap timed out")
	}

	hostKeyCalled := false
	deps.removeHostKey = func(vmName string) error {
		hostKeyCalled = true
		return nil
	}

	buf := new(bytes.Buffer)
	cmd := newRecreateCommandWithDeps(deps)
	root := newRecreateTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"recreate", "--yes"})

	_ = root.Execute() // Will fail at bootstrap polling.

	if hostKeyCalled {
		t.Error("removeHostKey should NOT be called when bootstrap polling fails")
	}
}

func TestRecreateLifecycleHostKeyRemovedWithNonDefaultVM(t *testing.T) {
	// Verify removeHostKey is called with the correct non-default VM name.
	runner := noSessionsRunner()
	lm := defaultLifecycleMocks()
	deps := &recreateDeps{
		describe:        &mockRecreateDescribeInstances{output: makeRunningInstanceForRecreate("i-dev456", "dev", "bob", "5.6.7.8", "us-west-2a")},
		sendKey:         &mockRecreateSendSSHPublicKey{output: &ec2instanceconnect.SendSSHPublicKeyOutput{}},
		remoteRun:       runner.run,
		owner:           "bob",
		ownerARN:        "arn:aws:iam::123456789012:user/bob",
		describeVolumes: lm.describeVolumes,
		stop:            lm.stop,
		terminate:       lm.terminate,
		detachVolume:    lm.detach,
		attachVolume:    lm.attach,
		run:             lm.run,
		createTags:      lm.createTags,
		describeSubnets: lm.subnets,
		describeSGs:     lm.sgs,
		bootstrapScript: []byte("#!/bin/bash\necho hello"),
		resolveAMI: func(ctx context.Context, client mintaws.GetParameterAPI) (string, error) {
			return "ami-test123", nil
		},
	}

	var removedVM string
	deps.removeHostKey = func(vmName string) error {
		removedVM = vmName
		return nil
	}

	buf := new(bytes.Buffer)
	cmd := newRecreateCommandWithDeps(deps)
	root := newRecreateTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"recreate", "--vm", "dev", "--yes"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if removedVM != "dev" {
		t.Errorf("removeHostKey called with %q, want %q", removedVM, "dev")
	}
}

// ---------------------------------------------------------------------------
// Tests — Claude-in-containers detection via recreate
// ---------------------------------------------------------------------------

func TestRecreateDetectsClaudeInContainers(t *testing.T) {
	// When claude processes are running in containers but no tmux/SSH
	// sessions exist, recreate should still block.
	runner := &mockRecreateRemoteRunner{
		tmuxOutput: nil,
		tmuxErr:    fmt.Errorf("no server running on /tmp/tmux-1000/default"),
		whoOutput:  []byte(""),
		whoErr:     nil,
		dockerPsOut: []byte("abc123\n"),
		dockerPsErr: nil,
		dockerTopOut: map[string][]byte{
			"abc123": []byte("PID COMMAND\n1 node\n42 claude\n"),
		},
		catExtendOut: nil,
		catExtendErr: fmt.Errorf("No such file or directory"),
	}
	d := newHappyRecreateDeps("alice")
	d.remoteRun = runner.run

	buf := new(bytes.Buffer)
	cmd := newRecreateCommandWithDeps(d)
	root := newRecreateTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"recreate", "--yes"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error when claude processes are active, got nil")
	}
	if !strings.Contains(err.Error(), "active sessions detected") {
		t.Errorf("error %q does not mention active sessions", err.Error())
	}
	if !strings.Contains(err.Error(), "Claude processes in containers") {
		t.Errorf("error %q does not mention claude processes", err.Error())
	}
}

func TestRecreateDetectsManualExtend(t *testing.T) {
	// When a manual extend is active, recreate should block.
	future := "2099-01-01T00:00:00Z"
	runner := &mockRecreateRemoteRunner{
		tmuxOutput:   nil,
		tmuxErr:      fmt.Errorf("no server running on /tmp/tmux-1000/default"),
		whoOutput:    []byte(""),
		whoErr:       nil,
		dockerPsOut:  nil,
		dockerPsErr:  fmt.Errorf("docker: command not found"),
		catExtendOut: []byte(future + "\n"),
		catExtendErr: nil,
	}
	d := newHappyRecreateDeps("alice")
	d.remoteRun = runner.run

	buf := new(bytes.Buffer)
	cmd := newRecreateCommandWithDeps(d)
	root := newRecreateTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"recreate", "--yes"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error when manual extend is active, got nil")
	}
	if !strings.Contains(err.Error(), "active sessions detected") {
		t.Errorf("error %q does not mention active sessions", err.Error())
	}
	if !strings.Contains(err.Error(), "Manual extend active until") {
		t.Errorf("error %q does not mention manual extend", err.Error())
	}
}

// Ensure provision.BootstrapPollFunc and provision.AMIResolver types are used correctly.
var _ provision.BootstrapPollFunc = func(ctx context.Context, owner, vmName, instanceID string) error { return nil }
var _ provision.AMIResolver = func(ctx context.Context, client mintaws.GetParameterAPI) (string, error) { return "", nil }

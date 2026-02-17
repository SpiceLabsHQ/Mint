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
// output based on the command being run (tmux list-clients vs who).
type mockRecreateRemoteRunner struct {
	tmuxOutput []byte
	tmuxErr    error
	whoOutput  []byte
	whoErr     error
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
	return nil, fmt.Errorf("unexpected command: %v", command)
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
		tmuxOutput: nil,
		tmuxErr:    fmt.Errorf("no server running on /tmp/tmux-1000/default"),
		whoOutput:  []byte(""),
		whoErr:     nil,
	}
}

func activeSessionsRunner() *mockRecreateRemoteRunner {
	return &mockRecreateRemoteRunner{
		tmuxOutput: []byte("/dev/pts/0 main\n"),
		tmuxErr:    nil,
		whoOutput:  []byte("ec2-user pts/0        2025-01-15 10:30 (192.168.1.100)\n"),
		whoErr:     nil,
	}
}

func newHappyRecreateDeps(owner string) *recreateDeps {
	runner := noSessionsRunner()
	return &recreateDeps{
		describe:  &mockRecreateDescribeInstances{output: makeRunningInstanceForRecreate("i-abc123", "default", owner, "1.2.3.4", "us-east-1a")},
		sendKey:   &mockRecreateSendSSHPublicKey{output: &ec2instanceconnect.SendSSHPublicKeyOutput{}},
		remoteRun: runner.run,
		owner:     owner,
	}
}

// ---------------------------------------------------------------------------
// Tests
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
			wantOutput: []string{"Proceeding with recreate", "i-abc123"},
		},
		{
			name:       "successful recreate with confirmation prompt",
			deps:       newHappyRecreateDeps("alice"),
			args:       []string{"recreate"},
			stdin:      "default\n",
			wantOutput: []string{"Proceeding with recreate"},
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
			wantOutput: []string{"Warning: proceeding despite active sessions", "Proceeding with recreate"},
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
			name:  "verbose shows progress steps",
			deps:  newHappyRecreateDeps("alice"),
			args:  []string{"recreate", "--yes", "--verbose"},
			wantOutput: []string{
				"Discovering VM",
				"Checking for active sessions",
				"Proceeding with recreate",
			},
		},
		{
			name: "non-default VM name",
			deps: func() *recreateDeps {
				runner := noSessionsRunner()
				return &recreateDeps{
					describe:  &mockRecreateDescribeInstances{output: makeRunningInstanceForRecreate("i-dev456", "dev", "bob", "5.6.7.8", "us-west-2a")},
					sendKey:   &mockRecreateSendSSHPublicKey{output: &ec2instanceconnect.SendSSHPublicKeyOutput{}},
					remoteRun: runner.run,
					owner:     "bob",
				}
			}(),
			args:       []string{"recreate", "--vm", "dev", "--yes"},
			wantOutput: []string{"Proceeding with recreate"},
		},
		{
			name: "non-default VM name confirmation requires correct name",
			deps: func() *recreateDeps {
				runner := noSessionsRunner()
				return &recreateDeps{
					describe:  &mockRecreateDescribeInstances{output: makeRunningInstanceForRecreate("i-dev456", "dev", "bob", "5.6.7.8", "us-west-2a")},
					sendKey:   &mockRecreateSendSSHPublicKey{output: &ec2instanceconnect.SendSSHPublicKeyOutput{}},
					remoteRun: runner.run,
					owner:     "bob",
				}
			}(),
			args:       []string{"recreate", "--vm", "dev"},
			stdin:      "dev\n",
			wantOutput: []string{"Proceeding with recreate"},
		},
		{
			name: "shows what will be destroyed before confirming",
			deps: newHappyRecreateDeps("alice"),
			args: []string{"recreate"},
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
					tmuxOutput: nil,
					tmuxErr:    fmt.Errorf("connection refused"),
					whoOutput:  nil,
					whoErr:     fmt.Errorf("connection refused"),
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

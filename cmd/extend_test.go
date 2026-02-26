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
	"github.com/SpiceLabsHQ/Mint/internal/cli"
	"github.com/spf13/cobra"

	mintaws "github.com/SpiceLabsHQ/Mint/internal/aws"
)

// mockDescribeForExtend implements mintaws.DescribeInstancesAPI for extend tests.
type mockDescribeForExtend struct {
	output *ec2.DescribeInstancesOutput
	err    error
}

func (m *mockDescribeForExtend) DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return m.output, m.err
}

// mockSendKeyForExtend implements mintaws.SendSSHPublicKeyAPI for extend tests.
type mockSendKeyForExtend struct {
	output *ec2instanceconnect.SendSSHPublicKeyOutput
	err    error
	called bool
}

func (m *mockSendKeyForExtend) SendSSHPublicKey(ctx context.Context, params *ec2instanceconnect.SendSSHPublicKeyInput, optFns ...func(*ec2instanceconnect.Options)) (*ec2instanceconnect.SendSSHPublicKeyOutput, error) {
	m.called = true
	return m.output, m.err
}

// capturedRemoteCommand records what the remote runner was called with.
type capturedRemoteCommand struct {
	called     bool
	command    []string
	instanceID string
}

// newMockRemoteRunner creates a RemoteCommandRunner that captures its arguments.
func newMockRemoteRunner(output []byte, err error) (RemoteCommandRunner, *capturedRemoteCommand) {
	captured := &capturedRemoteCommand{}
	runner := func(
		ctx context.Context,
		sendKey mintaws.SendSSHPublicKeyAPI,
		instanceID string,
		az string,
		host string,
		port int,
		user string,
		command []string,
	) ([]byte, error) {
		captured.called = true
		captured.command = command
		captured.instanceID = instanceID
		return output, err
	}
	return runner, captured
}

func makeRunningInstanceForExtend(id, vmName, owner, ip, az string) *ec2.DescribeInstancesOutput {
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

func makeStoppedInstanceForExtend(id, vmName, owner string) *ec2.DescribeInstancesOutput {
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

func newTestRootForExtend() *cobra.Command {
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

func TestExtendCommand(t *testing.T) {
	tests := []struct {
		name           string
		describe       *mockDescribeForExtend
		sendKey        *mockSendKeyForExtend
		remoteOutput   []byte
		remoteErr      error
		owner          string
		vmName         string
		args           []string // positional args (minutes)
		idleTimeout    int      // config default
		wantErr        bool
		wantErrContain string
		wantOutput     []string
		wantRemote     bool
		checkCommand   func(t *testing.T, command []string)
	}{
		{
			name: "extend with default minutes from config",
			describe: &mockDescribeForExtend{
				output: makeRunningInstanceForExtend("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForExtend{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remoteOutput: []byte("ok"),
			owner:        "alice",
			idleTimeout:  30,
			wantRemote:   true,
			wantOutput:   []string{"Extended idle timer by 30 minutes"},
			checkCommand: func(t *testing.T, command []string) {
				t.Helper()
				joined := strings.Join(command, " ")
				// Should compute seconds = 30 * 60 = 1800
				if !strings.Contains(joined, "1800") {
					t.Errorf("command should contain 1800 seconds (30 min), got: %s", joined)
				}
				if !strings.Contains(joined, "/var/lib/mint/idle-extended-until") {
					t.Errorf("command should write to /var/lib/mint/idle-extended-until, got: %s", joined)
				}
				if !strings.Contains(joined, "sudo") {
					t.Errorf("command should use sudo for /var/lib/mint/, got: %s", joined)
				}
			},
		},
		{
			name: "extend with explicit minutes overrides config default",
			describe: &mockDescribeForExtend{
				output: makeRunningInstanceForExtend("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForExtend{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remoteOutput: []byte("ok"),
			owner:        "alice",
			idleTimeout:  30,
			args:         []string{"45"},
			wantRemote:   true,
			wantOutput:   []string{"Extended idle timer by 45 minutes"},
			checkCommand: func(t *testing.T, command []string) {
				t.Helper()
				joined := strings.Join(command, " ")
				// 45 * 60 = 2700
				if !strings.Contains(joined, "2700") {
					t.Errorf("command should contain 2700 seconds (45 min), got: %s", joined)
				}
			},
		},
		{
			name: "extend with minutes below 15 fails validation",
			describe: &mockDescribeForExtend{
				output: makeRunningInstanceForExtend("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForExtend{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			owner:          "alice",
			idleTimeout:    30,
			args:           []string{"10"},
			wantErr:        true,
			wantErrContain: "15",
			wantRemote:     false,
		},
		{
			name: "extend with non-numeric arg fails",
			describe: &mockDescribeForExtend{
				output: makeRunningInstanceForExtend("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForExtend{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			owner:          "alice",
			idleTimeout:    30,
			args:           []string{"abc"},
			wantErr:        true,
			wantErrContain: "invalid",
			wantRemote:     false,
		},
		{
			name: "vm not found returns actionable error",
			describe: &mockDescribeForExtend{
				output: &ec2.DescribeInstancesOutput{},
			},
			sendKey:        &mockSendKeyForExtend{},
			owner:          "alice",
			idleTimeout:    30,
			wantErr:        true,
			wantErrContain: "mint up",
			wantRemote:     false,
		},
		{
			name: "stopped vm returns actionable error",
			describe: &mockDescribeForExtend{
				output: makeStoppedInstanceForExtend("i-abc123", "default", "alice"),
			},
			sendKey:        &mockSendKeyForExtend{},
			owner:          "alice",
			idleTimeout:    30,
			wantErr:        true,
			wantErrContain: "not running",
			wantRemote:     false,
		},
		{
			name: "describe API error propagates",
			describe: &mockDescribeForExtend{
				err: fmt.Errorf("throttled"),
			},
			sendKey:        &mockSendKeyForExtend{},
			owner:          "alice",
			idleTimeout:    30,
			wantErr:        true,
			wantErrContain: "throttled",
			wantRemote:     false,
		},
		{
			name: "remote command failure propagates",
			describe: &mockDescribeForExtend{
				output: makeRunningInstanceForExtend("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForExtend{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remoteErr:      fmt.Errorf("connection refused"),
			owner:          "alice",
			idleTimeout:    30,
			wantErr:        true,
			wantErrContain: "connection refused",
			wantRemote:     true,
		},
		{
			name: "SSH connection refused wrapped with bootstrap-aware context",
			describe: &mockDescribeForExtend{
				output: makeRunningInstanceForExtend("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForExtend{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remoteErr:      fmt.Errorf("remote command failed: exit status 255 (stderr: ssh: connect to host 1.2.3.4 port 41122: Connection refused)"),
			owner:          "alice",
			idleTimeout:    30,
			wantErr:        true,
			wantErrContain: "port 41122 refused",
			wantRemote:     true,
		},
		{
			name: "SSH connection refused bootstrap context mentions mint doctor",
			describe: &mockDescribeForExtend{
				output: makeRunningInstanceForExtend("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForExtend{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remoteErr:      fmt.Errorf("remote command failed: exit status 255 (stderr: ssh: connect to host 1.2.3.4 port 41122: Connection timed out)"),
			owner:          "alice",
			idleTimeout:    30,
			wantErr:        true,
			wantErrContain: "mint doctor",
			wantRemote:     true,
		},
		{
			name: "non-default vm name",
			describe: &mockDescribeForExtend{
				output: makeRunningInstanceForExtend("i-dev456", "dev", "bob", "10.0.0.1", "us-west-2a"),
			},
			sendKey: &mockSendKeyForExtend{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remoteOutput: []byte("ok"),
			owner:        "bob",
			vmName:       "dev",
			idleTimeout:  60,
			wantRemote:   true,
			wantOutput:   []string{"Extended idle timer by 60 minutes"},
		},
		{
			name: "uses config default of 60 when not overridden",
			describe: &mockDescribeForExtend{
				output: makeRunningInstanceForExtend("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForExtend{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remoteOutput: []byte("ok"),
			owner:        "alice",
			idleTimeout:  60,
			wantRemote:   true,
			wantOutput:   []string{"Extended idle timer by 60 minutes"},
			checkCommand: func(t *testing.T, command []string) {
				t.Helper()
				joined := strings.Join(command, " ")
				// 60 * 60 = 3600
				if !strings.Contains(joined, "3600") {
					t.Errorf("command should contain 3600 seconds (60 min), got: %s", joined)
				}
			},
		},
		{
			name: "boundary value: exactly 15 minutes is accepted",
			describe: &mockDescribeForExtend{
				output: makeRunningInstanceForExtend("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForExtend{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remoteOutput: []byte("ok"),
			owner:        "alice",
			idleTimeout:  30,
			args:         []string{"15"},
			wantRemote:   true,
			wantOutput:   []string{"Extended idle timer by 15 minutes"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := new(bytes.Buffer)

			runner, captured := newMockRemoteRunner(tt.remoteOutput, tt.remoteErr)

			deps := &extendDeps{
				describe:    tt.describe,
				sendKey:     tt.sendKey,
				owner:       tt.owner,
				remote:      runner,
				idleTimeout: tt.idleTimeout,
			}

			cmd := newExtendCommandWithDeps(deps)
			root := newTestRootForExtend()
			root.AddCommand(cmd)
			root.SetOut(buf)
			root.SetErr(buf)

			args := []string{}
			if tt.vmName != "" && tt.vmName != "default" {
				args = append(args, "--vm", tt.vmName)
			}
			args = append(args, "extend")
			args = append(args, tt.args...)
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

			if tt.wantRemote != captured.called {
				t.Errorf("remote runner called = %v, want %v", captured.called, tt.wantRemote)
			}

			if tt.checkCommand != nil && captured.called {
				tt.checkCommand(t, captured.command)
			}
		})
	}
}

func TestExtendCommandUseAndShort(t *testing.T) {
	cmd := newExtendCommand()
	if cmd.Use != "extend [minutes]" {
		t.Errorf("Use = %q, want %q", cmd.Use, "extend [minutes]")
	}
	if cmd.Short == "" {
		t.Error("Short description should not be empty")
	}
}

// TestExtendSpinnerWiring confirms that spinner messages are emitted during
// VM lookup and session extend phases when --verbose is active.
// In non-interactive (test) mode the spinner writes plain timestamped lines,
// so we can assert on their presence in the output buffer.
func TestExtendSpinnerWiring(t *testing.T) {
	t.Run("spinner emits Looking up VM during discovery", func(t *testing.T) {
		buf := new(bytes.Buffer)

		runner, _ := newMockRemoteRunner([]byte("ok"), nil)
		deps := &extendDeps{
			describe: &mockDescribeForExtend{
				output: makeRunningInstanceForExtend("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForExtend{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remote:      runner,
			owner:       "alice",
			idleTimeout: 30,
		}

		cmd := newExtendCommandWithDeps(deps)
		root := newTestRootForExtend()
		root.AddCommand(cmd)
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"--verbose", "extend"})

		if err := root.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		output := buf.String()
		if !strings.Contains(output, "Looking up VM") {
			t.Errorf("expected spinner message %q in output, got:\n%s", "Looking up VM", output)
		}
		if !strings.Contains(output, "Extending session") {
			t.Errorf("expected spinner message %q in output, got:\n%s", "Extending session", output)
		}
	})

	t.Run("spinner emits Looking up VM even when VM not found", func(t *testing.T) {
		buf := new(bytes.Buffer)

		runner, _ := newMockRemoteRunner(nil, nil)
		deps := &extendDeps{
			describe: &mockDescribeForExtend{
				output: &ec2.DescribeInstancesOutput{},
			},
			sendKey:     &mockSendKeyForExtend{},
			remote:      runner,
			owner:       "alice",
			idleTimeout: 30,
		}

		cmd := newExtendCommandWithDeps(deps)
		root := newTestRootForExtend()
		root.AddCommand(cmd)
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"--verbose", "extend"})

		err := root.Execute()
		if err == nil {
			t.Fatal("expected error for missing VM")
		}

		output := buf.String()
		if !strings.Contains(output, "Looking up VM") {
			t.Errorf("expected spinner message %q in output, got:\n%s", "Looking up VM", output)
		}
	})
}

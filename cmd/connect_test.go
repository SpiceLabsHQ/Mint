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
	"github.com/SpiceLabsHQ/Mint/internal/sshconfig"
	"github.com/spf13/cobra"
)

// mockDescribeForConnect implements mintaws.DescribeInstancesAPI for connect tests.
type mockDescribeForConnect struct {
	output *ec2.DescribeInstancesOutput
	err    error
}

func (m *mockDescribeForConnect) DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return m.output, m.err
}

// makeRunningInstanceForConnect creates a running instance for connect tests.
func makeRunningInstanceForConnect(id, vmName, owner, ip, az string) *ec2.DescribeInstancesOutput {
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

// makeRunningInstanceForConnectNoAZ creates a running instance without AZ for connect tests.
func makeRunningInstanceForConnectNoAZ(id, vmName, owner, ip string) *ec2.DescribeInstancesOutput {
	return &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{
			Instances: []ec2types.Instance{{
				InstanceId:      aws.String(id),
				InstanceType:    ec2types.InstanceTypeT3Medium,
				PublicIpAddress: aws.String(ip),
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

// newTestRootForConnect creates a minimal root command for connect tests.
func newTestRootForConnect() *cobra.Command {
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

func TestConnectCommandWithSessionName(t *testing.T) {
	tests := []struct {
		name           string
		describe       *mockDescribeForConnect
		sendKey        *mockSendSSHPublicKey
		owner          string
		sessionName    string
		vmName         string
		lookupMosh     func(string) (string, error)
		runnerErr      error
		wantErr        bool
		wantErrContain string
		wantExec       bool
		checkCmd       func(t *testing.T, captured capturedCommand)
	}{
		{
			name: "connects to named session via mosh and tmux",
			describe: &mockDescribeForConnect{
				output: makeRunningInstanceForConnect("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendSSHPublicKey{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			owner:       "alice",
			sessionName: "myproject",
			lookupMosh:  func(string) (string, error) { return "/usr/bin/mosh", nil },
			wantExec:    true,
			checkCmd: func(t *testing.T, captured capturedCommand) {
				t.Helper()
				if captured.name != "mosh" {
					t.Errorf("expected mosh command, got %q", captured.name)
				}
				argsStr := strings.Join(captured.args, " ")
				// Must have --ssh with embedded ssh command.
				if !strings.Contains(argsStr, "--ssh=") {
					t.Errorf("missing --ssh= flag, args: %v", captured.args)
				}
				// SSH within mosh must use port 41122.
				if !strings.Contains(argsStr, "-p 41122") {
					t.Errorf("missing port 41122 in --ssh arg, args: %v", captured.args)
				}
				// Must target ubuntu@ip.
				if !strings.Contains(argsStr, "ubuntu@1.2.3.4") {
					t.Errorf("missing user@host, args: %v", captured.args)
				}
				// Must include tmux new-session -A -s myproject.
				if !strings.Contains(argsStr, "tmux new-session -A -s myproject") {
					t.Errorf("missing tmux new-session -A -s, args: %v", captured.args)
				}
			},
		},
		{
			name: "mosh binary not found returns error",
			describe: &mockDescribeForConnect{
				output: makeRunningInstanceForConnect("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey:        &mockSendSSHPublicKey{},
			owner:          "alice",
			sessionName:    "myproject",
			lookupMosh:     func(string) (string, error) { return "", fmt.Errorf("not found") },
			wantErr:        true,
			wantErrContain: "mosh",
		},
		{
			name: "vm not found returns actionable error",
			describe: &mockDescribeForConnect{
				output: &ec2.DescribeInstancesOutput{},
			},
			sendKey:        &mockSendSSHPublicKey{},
			owner:          "alice",
			sessionName:    "myproject",
			lookupMosh:     func(string) (string, error) { return "/usr/bin/mosh", nil },
			wantErr:        true,
			wantErrContain: "mint up",
		},
		{
			name: "stopped vm returns actionable error",
			describe: &mockDescribeForConnect{
				output: &ec2.DescribeInstancesOutput{
					Reservations: []ec2types.Reservation{{
						Instances: []ec2types.Instance{{
							InstanceId:   aws.String("i-abc123"),
							InstanceType: ec2types.InstanceTypeT3Medium,
							State: &ec2types.InstanceState{
								Name: ec2types.InstanceStateNameStopped,
							},
							Tags: []ec2types.Tag{
								{Key: aws.String("mint:vm"), Value: aws.String("default")},
								{Key: aws.String("mint:owner"), Value: aws.String("alice")},
							},
						}},
					}},
				},
			},
			sendKey:        &mockSendSSHPublicKey{},
			owner:          "alice",
			sessionName:    "myproject",
			lookupMosh:     func(string) (string, error) { return "/usr/bin/mosh", nil },
			wantErr:        true,
			wantErrContain: "not running",
		},
		{
			name: "describe API error propagates",
			describe: &mockDescribeForConnect{
				err: fmt.Errorf("throttled"),
			},
			sendKey:        &mockSendSSHPublicKey{},
			owner:          "alice",
			sessionName:    "myproject",
			lookupMosh:     func(string) (string, error) { return "/usr/bin/mosh", nil },
			wantErr:        true,
			wantErrContain: "throttled",
		},
		{
			name: "send public key error propagates",
			describe: &mockDescribeForConnect{
				output: makeRunningInstanceForConnect("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendSSHPublicKey{
				err: fmt.Errorf("access denied"),
			},
			owner:          "alice",
			sessionName:    "myproject",
			lookupMosh:     func(string) (string, error) { return "/usr/bin/mosh", nil },
			wantErr:        true,
			wantErrContain: "access denied",
		},
		{
			name: "runner error with named session propagates",
			describe: &mockDescribeForConnect{
				output: makeRunningInstanceForConnect("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendSSHPublicKey{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			owner:          "alice",
			sessionName:    "myproject",
			lookupMosh:     func(string) (string, error) { return "/usr/bin/mosh", nil },
			runnerErr:      fmt.Errorf("connection lost"),
			wantErr:        true,
			wantErrContain: "connection lost",
		},
		{
			name: "non-default vm name",
			describe: &mockDescribeForConnect{
				output: makeRunningInstanceForConnect("i-dev456", "dev", "alice", "10.0.0.1", "us-west-2a"),
			},
			sendKey: &mockSendSSHPublicKey{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			owner:       "alice",
			sessionName: "work",
			vmName:      "dev",
			lookupMosh:  func(string) (string, error) { return "/usr/bin/mosh", nil },
			wantExec:    true,
			checkCmd: func(t *testing.T, captured capturedCommand) {
				t.Helper()
				argsStr := strings.Join(captured.args, " ")
				if !strings.Contains(argsStr, "ubuntu@10.0.0.1") {
					t.Errorf("wrong host, args: %v", captured.args)
				}
				if !strings.Contains(argsStr, "tmux new-session -A -s work") {
					t.Errorf("wrong tmux session name, args: %v", captured.args)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := new(bytes.Buffer)
			var captured *capturedCommand

			runner := func(name string, args ...string) error {
				captured = &capturedCommand{name: name, args: args}
				return tt.runnerErr
			}

			deps := &connectDeps{
				describe:   tt.describe,
				sendKey:    tt.sendKey,
				owner:      tt.owner,
				runner:     runner,
				lookupPath: tt.lookupMosh,
			}

			cmd := newConnectCommandWithDeps(deps)
			root := newTestRootForConnect()
			root.AddCommand(cmd)
			root.SetOut(buf)
			root.SetErr(buf)

			args := []string{}
			if tt.vmName != "" && tt.vmName != "default" {
				args = append(args, "--vm", tt.vmName)
			}
			args = append(args, "connect", tt.sessionName)
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

			if tt.wantExec {
				if captured == nil {
					t.Fatal("expected command execution, got none")
				}
				if tt.checkCmd != nil {
					tt.checkCmd(t, *captured)
				}
			} else if captured != nil {
				t.Errorf("unexpected command execution: %s %v", captured.name, captured.args)
			}

			// Verify SendSSHPublicKey input when called.
			if tt.sendKey.called && tt.sendKey.input != nil {
				if aws.ToString(tt.sendKey.input.InstanceId) == "" {
					t.Error("SendSSHPublicKey missing instance ID")
				}
				if aws.ToString(tt.sendKey.input.InstanceOSUser) != "ubuntu" {
					t.Errorf("SendSSHPublicKey OS user = %q, want ubuntu",
						aws.ToString(tt.sendKey.input.InstanceOSUser))
				}
				if aws.ToString(tt.sendKey.input.AvailabilityZone) == "" {
					t.Error("SendSSHPublicKey missing availability zone")
				}
			}
		})
	}
}

func TestConnectCommandNoSessionPickerSingle(t *testing.T) {
	// When no session name is provided and only one session exists,
	// it should auto-select that session and connect.
	describe := &mockDescribeForConnect{
		output: makeRunningInstanceForConnect("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
	}
	sendKey := &mockSendSSHPublicKey{
		output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
	}

	var captured capturedCommand
	runner := func(name string, args ...string) error {
		captured.name = name
		captured.args = args
		return nil
	}

	deps := &connectDeps{
		describe:   describe,
		sendKey:    sendKey,
		owner:      "alice",
		runner:     runner,
		lookupPath: func(string) (string, error) { return "/usr/bin/mosh", nil },
		remoteRun:  mockRemoteCommandRunner([]byte("onlyone 2 0 1700000000\n"), nil),
	}

	cmd := newConnectCommandWithDeps(deps)
	root := newTestRootForConnect()
	root.AddCommand(cmd)
	root.SetOut(new(bytes.Buffer))
	root.SetErr(new(bytes.Buffer))
	root.SetArgs([]string{"connect"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if captured.name != "mosh" {
		t.Fatalf("expected mosh command, got %q", captured.name)
	}

	argsStr := strings.Join(captured.args, " ")
	if !strings.Contains(argsStr, "tmux new-session -A -s onlyone") {
		t.Errorf("expected auto-selected session 'onlyone', args: %v", captured.args)
	}
}

func TestConnectCommandNoSessionPickerMultiple(t *testing.T) {
	// When multiple sessions exist and no name is provided,
	// present picker and read from stdin.
	describe := &mockDescribeForConnect{
		output: makeRunningInstanceForConnect("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
	}
	sendKey := &mockSendSSHPublicKey{
		output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
	}

	var captured capturedCommand
	runner := func(name string, args ...string) error {
		captured.name = name
		captured.args = args
		return nil
	}

	// Simulate user typing "2\n" into stdin.
	stdinBuf := strings.NewReader("2\n")

	deps := &connectDeps{
		describe:   describe,
		sendKey:    sendKey,
		owner:      "alice",
		runner:     runner,
		lookupPath: func(string) (string, error) { return "/usr/bin/mosh", nil },
		remoteRun:  mockRemoteCommandRunner([]byte("myproject 3 1 1700000000\nsidecar 1 0 1700001000\ndebug 2 0 1700002000\n"), nil),
		stdin:      stdinBuf,
	}

	outBuf := new(bytes.Buffer)
	cmd := newConnectCommandWithDeps(deps)
	root := newTestRootForConnect()
	root.AddCommand(cmd)
	root.SetOut(outBuf)
	root.SetErr(outBuf)
	root.SetArgs([]string{"connect"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have displayed the picker.
	output := outBuf.String()
	if !strings.Contains(output, "Active sessions:") {
		t.Errorf("missing picker header, got:\n%s", output)
	}
	if !strings.Contains(output, "myproject") {
		t.Errorf("missing session 'myproject' in picker, got:\n%s", output)
	}
	if !strings.Contains(output, "sidecar") {
		t.Errorf("missing session 'sidecar' in picker, got:\n%s", output)
	}
	if !strings.Contains(output, "Select session") {
		t.Errorf("missing prompt, got:\n%s", output)
	}

	// User selected "2", which is sidecar.
	if captured.name != "mosh" {
		t.Fatalf("expected mosh command, got %q", captured.name)
	}
	argsStr := strings.Join(captured.args, " ")
	if !strings.Contains(argsStr, "tmux new-session -A -s sidecar") {
		t.Errorf("expected selected session 'sidecar', args: %v", captured.args)
	}
}

func TestConnectCommandNoSessionsError(t *testing.T) {
	// When no sessions exist and no name is provided, error with guidance.
	describe := &mockDescribeForConnect{
		output: makeRunningInstanceForConnect("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
	}
	sendKey := &mockSendSSHPublicKey{}

	deps := &connectDeps{
		describe:   describe,
		sendKey:    sendKey,
		owner:      "alice",
		lookupPath: func(string) (string, error) { return "/usr/bin/mosh", nil },
		remoteRun:  mockRemoteCommandRunner([]byte(""), nil),
	}

	cmd := newConnectCommandWithDeps(deps)
	root := newTestRootForConnect()
	root.AddCommand(cmd)
	root.SetOut(new(bytes.Buffer))
	root.SetErr(new(bytes.Buffer))
	root.SetArgs([]string{"connect"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no active sessions") {
		t.Errorf("error %q does not contain 'no active sessions'", err.Error())
	}
	if !strings.Contains(err.Error(), "mint project add") {
		t.Errorf("error %q does not contain 'mint project add'", err.Error())
	}
}

func TestConnectCommandNoSessionsTmuxNotRunning(t *testing.T) {
	// When tmux server is not running and no name is provided, error.
	describe := &mockDescribeForConnect{
		output: makeRunningInstanceForConnect("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
	}
	sendKey := &mockSendSSHPublicKey{}

	deps := &connectDeps{
		describe:   describe,
		sendKey:    sendKey,
		owner:      "alice",
		lookupPath: func(string) (string, error) { return "/usr/bin/mosh", nil },
		remoteRun:  mockRemoteCommandRunner(nil, fmt.Errorf("remote command failed: exit status 1 (stderr: no server running on /tmp/tmux-1000/default)")),
	}

	cmd := newConnectCommandWithDeps(deps)
	root := newTestRootForConnect()
	root.AddCommand(cmd)
	root.SetOut(new(bytes.Buffer))
	root.SetErr(new(bytes.Buffer))
	root.SetArgs([]string{"connect"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no active sessions") {
		t.Errorf("error %q does not contain 'no active sessions'", err.Error())
	}
}

func TestConnectCommandPickerInvalidSelection(t *testing.T) {
	// Invalid selection (out of range) should return an error.
	describe := &mockDescribeForConnect{
		output: makeRunningInstanceForConnect("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
	}
	sendKey := &mockSendSSHPublicKey{}

	deps := &connectDeps{
		describe:   describe,
		sendKey:    sendKey,
		owner:      "alice",
		lookupPath: func(string) (string, error) { return "/usr/bin/mosh", nil },
		remoteRun:  mockRemoteCommandRunner([]byte("a 1 0 1700000000\nb 1 0 1700001000\n"), nil),
		stdin:      strings.NewReader("5\n"),
	}

	outBuf := new(bytes.Buffer)
	cmd := newConnectCommandWithDeps(deps)
	root := newTestRootForConnect()
	root.AddCommand(cmd)
	root.SetOut(outBuf)
	root.SetErr(outBuf)
	root.SetArgs([]string{"connect"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for invalid selection, got nil")
	}
	if !strings.Contains(err.Error(), "invalid selection") {
		t.Errorf("error %q does not contain 'invalid selection'", err.Error())
	}
}

func TestConnectCommandPickerNonNumericSelection(t *testing.T) {
	// Non-numeric input should return an error.
	describe := &mockDescribeForConnect{
		output: makeRunningInstanceForConnect("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
	}
	sendKey := &mockSendSSHPublicKey{}

	deps := &connectDeps{
		describe:   describe,
		sendKey:    sendKey,
		owner:      "alice",
		lookupPath: func(string) (string, error) { return "/usr/bin/mosh", nil },
		remoteRun:  mockRemoteCommandRunner([]byte("a 1 0 1700000000\nb 1 0 1700001000\n"), nil),
		stdin:      strings.NewReader("abc\n"),
	}

	cmd := newConnectCommandWithDeps(deps)
	root := newTestRootForConnect()
	root.AddCommand(cmd)
	root.SetOut(new(bytes.Buffer))
	root.SetErr(new(bytes.Buffer))
	root.SetArgs([]string{"connect"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for non-numeric selection, got nil")
	}
	if !strings.Contains(err.Error(), "invalid selection") {
		t.Errorf("error %q does not contain 'invalid selection'", err.Error())
	}
}

func TestConnectCommandEmptyAvailabilityZone(t *testing.T) {
	describe := &mockDescribeForConnect{
		output: makeRunningInstanceForConnectNoAZ("i-abc123", "default", "alice", "1.2.3.4"),
	}
	sendKey := &mockSendSSHPublicKey{}

	var captured capturedCommand
	deps := &connectDeps{
		describe:   describe,
		sendKey:    sendKey,
		owner:      "alice",
		runner: func(name string, args ...string) error {
			captured.name = name
			captured.args = args
			return nil
		},
		lookupPath: func(string) (string, error) { return "/usr/bin/mosh", nil },
	}

	cmd := newConnectCommandWithDeps(deps)
	root := newTestRootForConnect()
	root.AddCommand(cmd)
	root.SetOut(new(bytes.Buffer))
	root.SetErr(new(bytes.Buffer))
	root.SetArgs([]string{"connect", "myproject"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for empty availability zone, got nil")
	}
	if !strings.Contains(err.Error(), "no availability zone") {
		t.Errorf("error %q does not contain 'no availability zone'", err.Error())
	}
	// Mosh should NOT have been executed.
	if captured.name != "" {
		t.Errorf("mosh should not have been executed, got: %s %v", captured.name, captured.args)
	}
	// SendSSHPublicKey should NOT have been called.
	if sendKey.called {
		t.Error("SendSSHPublicKey should not have been called when AZ is empty")
	}
}

func TestConnectCommandTOFUFirstConnection(t *testing.T) {
	describe := &mockDescribeForConnect{
		output: makeRunningInstanceForConnect("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
	}
	sendKey := &mockSendSSHPublicKey{
		output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
	}
	scanner := mockHostKeyScanner("SHA256:testfp123", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest", nil)

	dir := t.TempDir()
	store := sshconfig.NewHostKeyStore(dir)

	var captured capturedCommand
	runner := func(name string, args ...string) error {
		captured.name = name
		captured.args = args
		return nil
	}

	deps := &connectDeps{
		describe:       describe,
		sendKey:        sendKey,
		owner:          "alice",
		runner:         runner,
		lookupPath:     func(string) (string, error) { return "/usr/bin/mosh", nil },
		hostKeyStore:   store,
		hostKeyScanner: scanner,
	}

	cmd := newConnectCommandWithDeps(deps)
	root := newTestRootForConnect()
	root.AddCommand(cmd)
	root.SetOut(new(bytes.Buffer))
	root.SetErr(new(bytes.Buffer))
	root.SetArgs([]string{"connect", "myproject"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if captured.name != "mosh" {
		t.Fatalf("expected mosh command, got %q", captured.name)
	}

	// Key should have been recorded.
	matched, existing, checkErr := store.CheckKey("default", "SHA256:testfp123")
	if checkErr != nil {
		t.Fatalf("CheckKey: %v", checkErr)
	}
	if !matched {
		t.Errorf("expected fingerprint to be stored, existing=%q", existing)
	}

	// Should use StrictHostKeyChecking=yes.
	argsStr := strings.Join(captured.args, " ")
	if !strings.Contains(argsStr, "StrictHostKeyChecking=yes") {
		t.Errorf("expected StrictHostKeyChecking=yes, args: %v", captured.args)
	}
}

func TestConnectCommandTOFUKeyMismatch(t *testing.T) {
	describe := &mockDescribeForConnect{
		output: makeRunningInstanceForConnect("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
	}
	sendKey := &mockSendSSHPublicKey{
		output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
	}
	scanner := mockHostKeyScanner("SHA256:newfp", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINew", nil)

	dir := t.TempDir()
	store := sshconfig.NewHostKeyStore(dir)

	deps := &connectDeps{
		describe:       describe,
		sendKey:        sendKey,
		owner:          "alice",
		runner:         func(name string, args ...string) error { return nil },
		lookupPath:     func(string) (string, error) { return "/usr/bin/mosh", nil },
		hostKeyStore:   store,
		hostKeyScanner: scanner,
	}

	// Pre-store a different key.
	if err := store.RecordKey("default", "SHA256:oldfp"); err != nil {
		t.Fatalf("RecordKey: %v", err)
	}

	cmd := newConnectCommandWithDeps(deps)
	root := newTestRootForConnect()
	root.AddCommand(cmd)
	root.SetOut(new(bytes.Buffer))
	root.SetErr(new(bytes.Buffer))
	root.SetArgs([]string{"connect", "myproject"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for key mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "HOST KEY CHANGED") {
		t.Errorf("error missing HOST KEY CHANGED, got: %s", err.Error())
	}
}

func TestConnectCommandMoshCommandConstruction(t *testing.T) {
	// Verify the complete mosh command structure including the -- separator
	// and tmux command.
	describe := &mockDescribeForConnect{
		output: makeRunningInstanceForConnect("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
	}
	sendKey := &mockSendSSHPublicKey{
		output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
	}

	var captured capturedCommand
	runner := func(name string, args ...string) error {
		captured.name = name
		captured.args = args
		return nil
	}

	deps := &connectDeps{
		describe:   describe,
		sendKey:    sendKey,
		owner:      "alice",
		runner:     runner,
		lookupPath: func(string) (string, error) { return "/usr/bin/mosh", nil },
	}

	cmd := newConnectCommandWithDeps(deps)
	root := newTestRootForConnect()
	root.AddCommand(cmd)
	root.SetOut(new(bytes.Buffer))
	root.SetErr(new(bytes.Buffer))
	root.SetArgs([]string{"connect", "myproject"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if captured.name != "mosh" {
		t.Fatalf("expected mosh, got %q", captured.name)
	}

	// Verify the mosh args contain the correct structure:
	// mosh --ssh="ssh -p 41122 -i <key> -o ..." ubuntu@1.2.3.4 -- tmux new-session -A -s myproject
	argsStr := strings.Join(captured.args, " ")

	if !strings.Contains(argsStr, "--ssh=") {
		t.Errorf("missing --ssh= flag, args: %v", captured.args)
	}
	if !strings.Contains(argsStr, "-p 41122") {
		t.Errorf("missing port 41122, args: %v", captured.args)
	}
	if !strings.Contains(argsStr, "-i") {
		t.Errorf("missing -i flag, args: %v", captured.args)
	}
	if !strings.Contains(argsStr, "ubuntu@1.2.3.4") {
		t.Errorf("missing user@host, args: %v", captured.args)
	}
	if !strings.Contains(argsStr, "-- tmux new-session -A -s myproject") {
		t.Errorf("missing tmux command after --, args: %v", captured.args)
	}
}

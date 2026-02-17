package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2instanceconnect"
	"github.com/nicholasgasior/mint/internal/cli"
	"github.com/nicholasgasior/mint/internal/sshconfig"
	"github.com/spf13/cobra"
)

// mockSendSSHPublicKey implements mintaws.SendSSHPublicKeyAPI for testing.
type mockSendSSHPublicKey struct {
	output *ec2instanceconnect.SendSSHPublicKeyOutput
	err    error
	called bool
	input  *ec2instanceconnect.SendSSHPublicKeyInput
}

func (m *mockSendSSHPublicKey) SendSSHPublicKey(ctx context.Context, params *ec2instanceconnect.SendSSHPublicKeyInput, optFns ...func(*ec2instanceconnect.Options)) (*ec2instanceconnect.SendSSHPublicKeyOutput, error) {
	m.called = true
	m.input = params
	return m.output, m.err
}

// mockDescribeForSSH implements mintaws.DescribeInstancesAPI with AZ support.
type mockDescribeForSSH struct {
	output *ec2.DescribeInstancesOutput
	err    error
}

func (m *mockDescribeForSSH) DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return m.output, m.err
}

// makeRunningInstanceWithAZ returns a DescribeInstancesOutput with placement info.
func makeRunningInstanceWithAZ(id, vmName, owner, ip, az string) *ec2.DescribeInstancesOutput {
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

// makeRunningInstanceNoAZ returns a DescribeInstancesOutput with a running instance
// that has no Placement/AvailabilityZone set — simulates a malformed response.
func makeRunningInstanceNoAZ(id, vmName, owner, ip string) *ec2.DescribeInstancesOutput {
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

// makeStoppedInstanceForSSH returns a DescribeInstancesOutput with a stopped instance.
func makeStoppedInstanceForSSH(id, vmName, owner string) *ec2.DescribeInstancesOutput {
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

// capturedCommand records the command that would have been executed.
type capturedCommand struct {
	name string
	args []string
}

// newTestRootForSSH creates a minimal root command for ssh/code tests.
func newTestRootForSSH() *cobra.Command {
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

func TestSSHCommand(t *testing.T) {
	tests := []struct {
		name           string
		describe       *mockDescribeForSSH
		sendKey        *mockSendSSHPublicKey
		owner          string
		vmName         string
		extraArgs      []string
		wantErr        bool
		wantErrContain string
		wantSendKey    bool
		wantExec       bool
		checkCmd       func(t *testing.T, captured capturedCommand)
	}{
		{
			name: "successful ssh to running instance",
			describe: &mockDescribeForSSH{
				output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendSSHPublicKey{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			owner:       "alice",
			wantSendKey: true,
			wantExec:    true,
			checkCmd: func(t *testing.T, captured capturedCommand) {
				t.Helper()
				if captured.name != "ssh" {
					t.Errorf("expected ssh command, got %q", captured.name)
				}
				argsStr := strings.Join(captured.args, " ")
				if !strings.Contains(argsStr, "-p 41122") {
					t.Errorf("missing port flag, args: %v", captured.args)
				}
				if !strings.Contains(argsStr, "ubuntu@1.2.3.4") {
					t.Errorf("missing user@host, args: %v", captured.args)
				}
				// Without hostKeyStore, falls back to insecure mode.
				if !strings.Contains(argsStr, "-o StrictHostKeyChecking=no") {
					t.Errorf("expected StrictHostKeyChecking=no (no TOFU store), args: %v", captured.args)
				}
				// Should have -i flag with a temp key file
				hasIdentity := false
				for _, arg := range captured.args {
					if arg == "-i" {
						hasIdentity = true
					}
				}
				if !hasIdentity {
					t.Errorf("missing -i flag for identity file, args: %v", captured.args)
				}
			},
		},
		{
			name: "vm not found returns actionable error",
			describe: &mockDescribeForSSH{
				output: &ec2.DescribeInstancesOutput{},
			},
			sendKey:        &mockSendSSHPublicKey{},
			owner:          "alice",
			wantErr:        true,
			wantErrContain: "mint up",
			wantSendKey:    false,
			wantExec:       false,
		},
		{
			name: "stopped vm returns actionable error",
			describe: &mockDescribeForSSH{
				output: makeStoppedInstanceForSSH("i-abc123", "default", "alice"),
			},
			sendKey:        &mockSendSSHPublicKey{},
			owner:          "alice",
			wantErr:        true,
			wantErrContain: "not running",
			wantSendKey:    false,
			wantExec:       false,
		},
		{
			name: "describe API error propagates",
			describe: &mockDescribeForSSH{
				err: fmt.Errorf("throttled"),
			},
			sendKey:        &mockSendSSHPublicKey{},
			owner:          "alice",
			wantErr:        true,
			wantErrContain: "throttled",
			wantSendKey:    false,
			wantExec:       false,
		},
		{
			name: "send public key error propagates",
			describe: &mockDescribeForSSH{
				output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendSSHPublicKey{
				err: fmt.Errorf("access denied"),
			},
			owner:          "alice",
			wantErr:        true,
			wantErrContain: "access denied",
			wantSendKey:    true,
			wantExec:       false,
		},
		{
			name: "extra args after -- are passed through",
			describe: &mockDescribeForSSH{
				output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendSSHPublicKey{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			owner:       "alice",
			extraArgs:   []string{"-L", "8080:localhost:8080"},
			wantSendKey: true,
			wantExec:    true,
			checkCmd: func(t *testing.T, captured capturedCommand) {
				t.Helper()
				argsStr := strings.Join(captured.args, " ")
				if !strings.Contains(argsStr, "-L 8080:localhost:8080") {
					t.Errorf("extra args not passed through, args: %v", captured.args)
				}
			},
		},
		{
			name: "send key called with correct params",
			describe: &mockDescribeForSSH{
				output: makeRunningInstanceWithAZ("i-xyz789", "default", "bob", "5.6.7.8", "eu-west-1b"),
			},
			sendKey: &mockSendSSHPublicKey{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			owner:       "bob",
			wantSendKey: true,
			wantExec:    true,
			checkCmd: func(t *testing.T, captured capturedCommand) {
				// Just verify it runs — the send key params are checked below
			},
		},
		{
			name: "non-default vm name",
			describe: &mockDescribeForSSH{
				output: makeRunningInstanceWithAZ("i-dev456", "dev", "alice", "10.0.0.1", "us-west-2a"),
			},
			sendKey: &mockSendSSHPublicKey{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			owner:       "alice",
			vmName:      "dev",
			wantSendKey: true,
			wantExec:    true,
			checkCmd: func(t *testing.T, captured capturedCommand) {
				t.Helper()
				argsStr := strings.Join(captured.args, " ")
				if !strings.Contains(argsStr, "ubuntu@10.0.0.1") {
					t.Errorf("wrong host, args: %v", captured.args)
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
				return nil
			}

			deps := &sshDeps{
				describe: tt.describe,
				sendKey:  tt.sendKey,
				owner:    tt.owner,
				runner:   runner,
			}

			cmd := newSSHCommandWithDeps(deps)
			root := newTestRootForSSH()
			root.AddCommand(cmd)
			root.SetOut(buf)
			root.SetErr(buf)

			args := []string{"ssh"}
			if tt.vmName != "" && tt.vmName != "default" {
				args = append([]string{"--vm", tt.vmName}, args...)
			}
			if len(tt.extraArgs) > 0 {
				args = append(args, "--")
				args = append(args, tt.extraArgs...)
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

			if tt.wantSendKey != tt.sendKey.called {
				t.Errorf("SendSSHPublicKey called = %v, want %v", tt.sendKey.called, tt.wantSendKey)
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

			// Verify SendSSHPublicKey input when called
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
				if aws.ToString(tt.sendKey.input.SSHPublicKey) == "" {
					t.Error("SendSSHPublicKey missing SSH public key")
				}
			}
		})
	}
}

func TestSSHCommandEmptyAvailabilityZone(t *testing.T) {
	describe := &mockDescribeForSSH{
		output: makeRunningInstanceNoAZ("i-abc123", "default", "alice", "1.2.3.4"),
	}
	sendKey := &mockSendSSHPublicKey{}

	var captured capturedCommand
	deps := &sshDeps{
		describe: describe,
		sendKey:  sendKey,
		owner:    "alice",
		runner: func(name string, args ...string) error {
			captured.name = name
			captured.args = args
			return nil
		},
	}

	err := runSSHWithDeps(t, deps, "default")
	if err == nil {
		t.Fatal("expected error for empty availability zone, got nil")
	}
	if !strings.Contains(err.Error(), "no availability zone") {
		t.Errorf("error %q does not contain 'no availability zone'", err.Error())
	}
	// SSH should NOT have been executed.
	if captured.name != "" {
		t.Errorf("ssh should not have been executed, got: %s %v", captured.name, captured.args)
	}
	// SendSSHPublicKey should NOT have been called.
	if sendKey.called {
		t.Error("SendSSHPublicKey should not have been called when AZ is empty")
	}
}

func TestSSHKeyGeneration(t *testing.T) {
	pubKey, privKeyPath, cleanup, err := generateEphemeralKeyPair()
	if err != nil {
		t.Fatalf("generateEphemeralKeyPair: %v", err)
	}
	defer cleanup()

	if pubKey == "" {
		t.Error("public key is empty")
	}
	if privKeyPath == "" {
		t.Error("private key path is empty")
	}

	// Public key should be in OpenSSH format (starts with ssh-ed25519 or ecdsa-)
	if !strings.HasPrefix(pubKey, "ssh-ed25519 ") {
		t.Errorf("public key not in expected format, got prefix: %q", pubKey[:min(30, len(pubKey))])
	}

	// Verify the temp file exists
	if _, err := os.Stat(privKeyPath); os.IsNotExist(err) {
		t.Error("private key temp file does not exist")
	}

	// Cleanup should remove the file
	cleanup()
	if _, err := os.Stat(privKeyPath); !os.IsNotExist(err) {
		t.Error("cleanup did not remove private key file")
	}
}

// mockHostKeyScanner returns a HostKeyScanner that yields fixed values.
func mockHostKeyScanner(fingerprint, hostKeyLine string, err error) HostKeyScanner {
	return func(host string, port int) (string, string, error) {
		return fingerprint, hostKeyLine, err
	}
}

// newTOFUDeps creates sshDeps with TOFU support for testing. The store
// is backed by a temp directory that is cleaned up by the test.
func newTOFUDeps(t *testing.T, describe *mockDescribeForSSH, sendKey *mockSendSSHPublicKey, owner string, scanner HostKeyScanner) (*sshDeps, *capturedCommand) {
	t.Helper()
	dir := t.TempDir()
	store := sshconfig.NewHostKeyStore(dir)

	var captured capturedCommand
	runner := func(name string, args ...string) error {
		captured.name = name
		captured.args = args
		return nil
	}

	return &sshDeps{
		describe:       describe,
		sendKey:        sendKey,
		owner:          owner,
		runner:         runner,
		hostKeyStore:   store,
		hostKeyScanner: scanner,
	}, &captured
}

// runSSHWithDeps is a helper to execute the ssh command with given deps.
func runSSHWithDeps(t *testing.T, deps *sshDeps, vmName string) error {
	t.Helper()
	cmd := newSSHCommandWithDeps(deps)
	root := newTestRootForSSH()
	root.AddCommand(cmd)
	root.SetOut(new(bytes.Buffer))
	root.SetErr(new(bytes.Buffer))

	args := []string{"ssh"}
	if vmName != "" && vmName != "default" {
		args = append([]string{"--vm", vmName}, args...)
	}
	root.SetArgs(args)
	return root.Execute()
}

func TestSSHCommandTOFUFirstConnection(t *testing.T) {
	describe := &mockDescribeForSSH{
		output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
	}
	sendKey := &mockSendSSHPublicKey{
		output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
	}
	scanner := mockHostKeyScanner("SHA256:testfp123", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest", nil)

	deps, captured := newTOFUDeps(t, describe, sendKey, "alice", scanner)

	err := runSSHWithDeps(t, deps, "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// SSH should have been executed.
	if captured.name != "ssh" {
		t.Fatalf("expected ssh command, got %q", captured.name)
	}

	// Key should have been recorded.
	matched, existing, checkErr := deps.hostKeyStore.CheckKey("default", "SHA256:testfp123")
	if checkErr != nil {
		t.Fatalf("CheckKey: %v", checkErr)
	}
	if !matched {
		t.Errorf("expected fingerprint to be stored, existing=%q", existing)
	}
}

func TestSSHCommandTOFUKeyMatch(t *testing.T) {
	describe := &mockDescribeForSSH{
		output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
	}
	sendKey := &mockSendSSHPublicKey{
		output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
	}
	scanner := mockHostKeyScanner("SHA256:matchfp", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest", nil)

	deps, captured := newTOFUDeps(t, describe, sendKey, "alice", scanner)

	// Pre-store the matching key.
	if err := deps.hostKeyStore.RecordKey("default", "SHA256:matchfp"); err != nil {
		t.Fatalf("RecordKey: %v", err)
	}

	err := runSSHWithDeps(t, deps, "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// SSH should have been executed.
	if captured.name != "ssh" {
		t.Fatalf("expected ssh command, got %q", captured.name)
	}
}

func TestSSHCommandTOFUKeyMismatch(t *testing.T) {
	describe := &mockDescribeForSSH{
		output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
	}
	sendKey := &mockSendSSHPublicKey{
		output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
	}
	// Scanner returns a different fingerprint than what's stored.
	scanner := mockHostKeyScanner("SHA256:newfp", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINew", nil)

	deps, captured := newTOFUDeps(t, describe, sendKey, "alice", scanner)

	// Pre-store a different key.
	if err := deps.hostKeyStore.RecordKey("default", "SHA256:oldfp"); err != nil {
		t.Fatalf("RecordKey: %v", err)
	}

	err := runSSHWithDeps(t, deps, "default")
	if err == nil {
		t.Fatal("expected error for key mismatch, got nil")
	}

	// Error should contain both fingerprints and instructions.
	errMsg := err.Error()
	if !strings.Contains(errMsg, "HOST KEY CHANGED") {
		t.Errorf("error missing HOST KEY CHANGED, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "SHA256:oldfp") {
		t.Errorf("error missing old fingerprint, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "SHA256:newfp") {
		t.Errorf("error missing new fingerprint, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "mint destroy && mint up") {
		t.Errorf("error missing recovery instructions, got: %s", errMsg)
	}

	// SSH should NOT have been executed.
	if captured.name != "" {
		t.Errorf("ssh should not have been executed, got: %s %v", captured.name, captured.args)
	}
}

func TestSSHCommandTOFUSkippedWhenNoStore(t *testing.T) {
	describe := &mockDescribeForSSH{
		output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
	}
	sendKey := &mockSendSSHPublicKey{
		output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
	}

	var captured capturedCommand
	deps := &sshDeps{
		describe: describe,
		sendKey:  sendKey,
		owner:    "alice",
		runner: func(name string, args ...string) error {
			captured.name = name
			captured.args = args
			return nil
		},
		// hostKeyStore and hostKeyScanner are nil — TOFU should be skipped.
	}

	err := runSSHWithDeps(t, deps, "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// SSH should execute with insecure fallback.
	if captured.name != "ssh" {
		t.Fatalf("expected ssh command, got %q", captured.name)
	}
	argsStr := strings.Join(captured.args, " ")
	if !strings.Contains(argsStr, "StrictHostKeyChecking=no") {
		t.Errorf("expected StrictHostKeyChecking=no (no TOFU), args: %v", captured.args)
	}
	if !strings.Contains(argsStr, "UserKnownHostsFile=/dev/null") {
		t.Errorf("expected UserKnownHostsFile=/dev/null (no TOFU), args: %v", captured.args)
	}
}

func TestSSHCommandTOFUScannerError(t *testing.T) {
	describe := &mockDescribeForSSH{
		output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
	}
	sendKey := &mockSendSSHPublicKey{
		output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
	}
	// Scanner returns an error.
	scanner := mockHostKeyScanner("", "", fmt.Errorf("connection refused"))

	deps, captured := newTOFUDeps(t, describe, sendKey, "alice", scanner)

	err := runSSHWithDeps(t, deps, "default")
	if err == nil {
		t.Fatal("expected error from scanner, got nil")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("error should contain scanner error, got: %s", err.Error())
	}

	// SSH should NOT have been executed.
	if captured.name != "" {
		t.Errorf("ssh should not have been executed, got: %s %v", captured.name, captured.args)
	}
}

func TestSSHCommandUsesStrictHostKeyChecking(t *testing.T) {
	describe := &mockDescribeForSSH{
		output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
	}
	sendKey := &mockSendSSHPublicKey{
		output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
	}
	scanner := mockHostKeyScanner("SHA256:strictfp", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIStrict", nil)

	deps, captured := newTOFUDeps(t, describe, sendKey, "alice", scanner)

	err := runSSHWithDeps(t, deps, "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	argsStr := strings.Join(captured.args, " ")

	// Must use StrictHostKeyChecking=yes (not no).
	if !strings.Contains(argsStr, "StrictHostKeyChecking=yes") {
		t.Errorf("expected StrictHostKeyChecking=yes, args: %v", captured.args)
	}
	if strings.Contains(argsStr, "StrictHostKeyChecking=no") {
		t.Errorf("should NOT have StrictHostKeyChecking=no, args: %v", captured.args)
	}

	// Must NOT use UserKnownHostsFile=/dev/null.
	if strings.Contains(argsStr, "UserKnownHostsFile=/dev/null") {
		t.Errorf("should NOT have UserKnownHostsFile=/dev/null, args: %v", captured.args)
	}

	// Must have a UserKnownHostsFile pointing to a real file.
	hasKnownHostsFile := false
	for _, arg := range captured.args {
		if strings.HasPrefix(arg, "UserKnownHostsFile=") && !strings.Contains(arg, "/dev/null") {
			hasKnownHostsFile = true
		}
	}
	if !hasKnownHostsFile {
		t.Errorf("expected UserKnownHostsFile with temp file path, args: %v", captured.args)
	}
}

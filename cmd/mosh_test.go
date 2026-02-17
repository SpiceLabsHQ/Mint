package cmd

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2instanceconnect"
	"github.com/nicholasgasior/mint/internal/sshconfig"
)

func TestMoshCommand(t *testing.T) {
	tests := []struct {
		name           string
		describe       *mockDescribeForSSH
		sendKey        *mockSendSSHPublicKey
		owner          string
		vmName         string
		lookupMosh     func(string) (string, error) // mock exec.LookPath
		wantErr        bool
		wantErrContain string
		wantSendKey    bool
		wantExec       bool
		checkCmd       func(t *testing.T, captured capturedCommand)
	}{
		{
			name: "successful mosh to running instance",
			describe: &mockDescribeForSSH{
				output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendSSHPublicKey{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			owner:       "alice",
			lookupMosh:  func(string) (string, error) { return "/usr/bin/mosh", nil },
			wantSendKey: true,
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
				// Must include -i for identity file.
				if !strings.Contains(argsStr, "-i") {
					t.Errorf("missing -i flag for identity file, args: %v", captured.args)
				}
				// Without hostKeyStore, falls back to insecure mode.
				if !strings.Contains(argsStr, "StrictHostKeyChecking=no") {
					t.Errorf("expected StrictHostKeyChecking=no (no TOFU store), args: %v", captured.args)
				}
			},
		},
		{
			name: "mosh binary not found returns error",
			describe: &mockDescribeForSSH{
				output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey:    &mockSendSSHPublicKey{},
			owner:      "alice",
			lookupMosh: func(string) (string, error) { return "", fmt.Errorf("not found") },
			wantErr:    true,
			wantErrContain: "mosh",
			wantSendKey:    false,
			wantExec:       false,
		},
		{
			name: "vm not found returns actionable error",
			describe: &mockDescribeForSSH{
				output: &ec2.DescribeInstancesOutput{},
			},
			sendKey:        &mockSendSSHPublicKey{},
			owner:          "alice",
			lookupMosh:     func(string) (string, error) { return "/usr/bin/mosh", nil },
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
			lookupMosh:     func(string) (string, error) { return "/usr/bin/mosh", nil },
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
			lookupMosh:     func(string) (string, error) { return "/usr/bin/mosh", nil },
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
			lookupMosh:     func(string) (string, error) { return "/usr/bin/mosh", nil },
			wantErr:        true,
			wantErrContain: "access denied",
			wantSendKey:    true,
			wantExec:       false,
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
			lookupMosh:  func(string) (string, error) { return "/usr/bin/mosh", nil },
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
		{
			name: "send key called with correct params",
			describe: &mockDescribeForSSH{
				output: makeRunningInstanceWithAZ("i-xyz789", "default", "bob", "5.6.7.8", "eu-west-1b"),
			},
			sendKey: &mockSendSSHPublicKey{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			owner:       "bob",
			lookupMosh:  func(string) (string, error) { return "/usr/bin/mosh", nil },
			wantSendKey: true,
			wantExec:    true,
			checkCmd:    func(t *testing.T, captured capturedCommand) {},
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

			deps := &moshDeps{
				describe:   tt.describe,
				sendKey:    tt.sendKey,
				owner:      tt.owner,
				runner:     runner,
				lookupPath: tt.lookupMosh,
			}

			cmd := newMoshCommandWithDeps(deps)
			root := newTestRootForSSH()
			root.AddCommand(cmd)
			root.SetOut(buf)
			root.SetErr(buf)

			args := []string{"mosh"}
			if tt.vmName != "" && tt.vmName != "default" {
				args = append([]string{"--vm", tt.vmName}, args...)
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
				if aws.ToString(tt.sendKey.input.SSHPublicKey) == "" {
					t.Error("SendSSHPublicKey missing SSH public key")
				}
			}
		})
	}
}

func TestMoshCommandTOFUFirstConnection(t *testing.T) {
	describe := &mockDescribeForSSH{
		output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
	}
	sendKey := &mockSendSSHPublicKey{
		output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
	}
	scanner := mockHostKeyScanner("SHA256:testfp123", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest", nil)

	deps, captured := newTOFUMoshDeps(t, describe, sendKey, "alice", scanner)

	err := runMoshWithDeps(t, deps, "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Mosh should have been executed.
	if captured.name != "mosh" {
		t.Fatalf("expected mosh command, got %q", captured.name)
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

func TestMoshCommandTOFUKeyMismatch(t *testing.T) {
	describe := &mockDescribeForSSH{
		output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
	}
	sendKey := &mockSendSSHPublicKey{
		output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
	}
	scanner := mockHostKeyScanner("SHA256:newfp", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINew", nil)

	deps, captured := newTOFUMoshDeps(t, describe, sendKey, "alice", scanner)

	// Pre-store a different key.
	if err := deps.hostKeyStore.RecordKey("default", "SHA256:oldfp"); err != nil {
		t.Fatalf("RecordKey: %v", err)
	}

	err := runMoshWithDeps(t, deps, "default")
	if err == nil {
		t.Fatal("expected error for key mismatch, got nil")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "HOST KEY CHANGED") {
		t.Errorf("error missing HOST KEY CHANGED, got: %s", errMsg)
	}

	// Mosh should NOT have been executed.
	if captured.name != "" {
		t.Errorf("mosh should not have been executed, got: %s %v", captured.name, captured.args)
	}
}

func TestMoshCommandTOFUScannerError(t *testing.T) {
	describe := &mockDescribeForSSH{
		output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
	}
	sendKey := &mockSendSSHPublicKey{
		output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
	}
	scanner := mockHostKeyScanner("", "", fmt.Errorf("connection refused"))

	deps, captured := newTOFUMoshDeps(t, describe, sendKey, "alice", scanner)

	err := runMoshWithDeps(t, deps, "default")
	if err == nil {
		t.Fatal("expected error from scanner, got nil")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("error should contain scanner error, got: %s", err.Error())
	}

	// Mosh should NOT have been executed.
	if captured.name != "" {
		t.Errorf("mosh should not have been executed, got: %s %v", captured.name, captured.args)
	}
}

func TestMoshCommandUsesStrictHostKeyChecking(t *testing.T) {
	describe := &mockDescribeForSSH{
		output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
	}
	sendKey := &mockSendSSHPublicKey{
		output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
	}
	scanner := mockHostKeyScanner("SHA256:strictfp", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIStrict", nil)

	deps, captured := newTOFUMoshDeps(t, describe, sendKey, "alice", scanner)

	err := runMoshWithDeps(t, deps, "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	argsStr := strings.Join(captured.args, " ")

	// Must use StrictHostKeyChecking=yes within the --ssh arg.
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
}

// newTOFUMoshDeps creates moshDeps with TOFU support for testing.
func newTOFUMoshDeps(t *testing.T, describe *mockDescribeForSSH, sendKey *mockSendSSHPublicKey, owner string, scanner HostKeyScanner) (*moshDeps, *capturedCommand) {
	t.Helper()
	dir := t.TempDir()
	store := sshconfig.NewHostKeyStore(dir)

	var captured capturedCommand
	runner := func(name string, args ...string) error {
		captured.name = name
		captured.args = args
		return nil
	}

	return &moshDeps{
		describe:       describe,
		sendKey:        sendKey,
		owner:          owner,
		runner:         runner,
		lookupPath:     func(string) (string, error) { return "/usr/bin/mosh", nil },
		hostKeyStore:   store,
		hostKeyScanner: scanner,
	}, &captured
}

// runMoshWithDeps is a helper to execute the mosh command with given deps.
func runMoshWithDeps(t *testing.T, deps *moshDeps, vmName string) error {
	t.Helper()
	cmd := newMoshCommandWithDeps(deps)
	root := newTestRootForSSH()
	root.AddCommand(cmd)
	root.SetOut(new(bytes.Buffer))
	root.SetErr(new(bytes.Buffer))

	args := []string{"mosh"}
	if vmName != "" && vmName != "default" {
		args = append([]string{"--vm", vmName}, args...)
	}
	root.SetArgs(args)
	return root.Execute()
}

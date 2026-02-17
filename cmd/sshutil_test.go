package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2instanceconnect"

	mintaws "github.com/nicholasgasior/mint/internal/aws"
	"github.com/nicholasgasior/mint/internal/sshconfig"
)

// mockSendKeyForRemote implements mintaws.SendSSHPublicKeyAPI for remote runner tests.
type mockSendKeyForRemote struct {
	output *ec2instanceconnect.SendSSHPublicKeyOutput
	err    error
	called bool
	input  *ec2instanceconnect.SendSSHPublicKeyInput
}

func (m *mockSendKeyForRemote) SendSSHPublicKey(ctx context.Context, params *ec2instanceconnect.SendSSHPublicKeyInput, optFns ...func(*ec2instanceconnect.Options)) (*ec2instanceconnect.SendSSHPublicKeyOutput, error) {
	m.called = true
	m.input = params
	return m.output, m.err
}

// Verify interface compliance at compile time.
var _ mintaws.SendSSHPublicKeyAPI = (*mockSendKeyForRemote)(nil)

func TestRemoteCommandRunnerType(t *testing.T) {
	// Verify that defaultRemoteRunner satisfies the RemoteCommandRunner type.
	var runner RemoteCommandRunner = defaultRemoteRunner
	if runner == nil {
		t.Fatal("defaultRemoteRunner should not be nil")
	}
}

func TestRemoteCommandRunnerMockInjection(t *testing.T) {
	tests := []struct {
		name       string
		mockRunner RemoteCommandRunner
		wantOutput string
		wantErr    bool
		wantErrMsg string
	}{
		{
			name: "mock returns stdout",
			mockRunner: func(ctx context.Context, sendKey mintaws.SendSSHPublicKeyAPI, instanceID, az, host string, port int, user string, command []string) ([]byte, error) {
				return []byte("session-abc\nsession-def\n"), nil
			},
			wantOutput: "session-abc\nsession-def\n",
		},
		{
			name: "mock returns error",
			mockRunner: func(ctx context.Context, sendKey mintaws.SendSSHPublicKeyAPI, instanceID, az, host string, port int, user string, command []string) ([]byte, error) {
				return nil, fmt.Errorf("connection refused")
			},
			wantErr:    true,
			wantErrMsg: "connection refused",
		},
		{
			name: "mock receives correct parameters",
			mockRunner: func(ctx context.Context, sendKey mintaws.SendSSHPublicKeyAPI, instanceID, az, host string, port int, user string, command []string) ([]byte, error) {
				if instanceID != "i-test123" {
					return nil, fmt.Errorf("wrong instanceID: %s", instanceID)
				}
				if az != "us-east-1a" {
					return nil, fmt.Errorf("wrong az: %s", az)
				}
				if host != "1.2.3.4" {
					return nil, fmt.Errorf("wrong host: %s", host)
				}
				if port != 41122 {
					return nil, fmt.Errorf("wrong port: %d", port)
				}
				if user != "ubuntu" {
					return nil, fmt.Errorf("wrong user: %s", user)
				}
				if len(command) != 2 || command[0] != "tmux" || command[1] != "list-sessions" {
					return nil, fmt.Errorf("wrong command: %v", command)
				}
				return []byte("ok"), nil
			},
			wantOutput: "ok",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			mock := &mockSendKeyForRemote{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			}

			out, err := tt.mockRunner(ctx, mock, "i-test123", "us-east-1a", "1.2.3.4", 41122, "ubuntu", []string{"tmux", "list-sessions"})

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.wantErrMsg != "" && !strings.Contains(err.Error(), tt.wantErrMsg) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.wantErrMsg)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(out) != tt.wantOutput {
				t.Errorf("output = %q, want %q", string(out), tt.wantOutput)
			}
		})
	}
}

func TestDefaultRemoteRunnerSendKeyError(t *testing.T) {
	// When Instance Connect rejects the key, defaultRemoteRunner should
	// propagate the error without attempting to run ssh.
	ctx := context.Background()
	mock := &mockSendKeyForRemote{
		err: fmt.Errorf("access denied"),
	}

	_, err := defaultRemoteRunner(ctx, mock, "i-test123", "us-east-1a", "1.2.3.4", 41122, "ubuntu", []string{"whoami"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "access denied") {
		t.Errorf("error %q does not contain 'access denied'", err.Error())
	}
	if !mock.called {
		t.Error("SendSSHPublicKey should have been called")
	}

	// Verify the input was populated correctly.
	if aws.ToString(mock.input.InstanceId) != "i-test123" {
		t.Errorf("InstanceId = %q, want %q", aws.ToString(mock.input.InstanceId), "i-test123")
	}
	if aws.ToString(mock.input.AvailabilityZone) != "us-east-1a" {
		t.Errorf("AvailabilityZone = %q, want %q", aws.ToString(mock.input.AvailabilityZone), "us-east-1a")
	}
	if aws.ToString(mock.input.InstanceOSUser) != "ubuntu" {
		t.Errorf("InstanceOSUser = %q, want %q", aws.ToString(mock.input.InstanceOSUser), "ubuntu")
	}
	if aws.ToString(mock.input.SSHPublicKey) == "" {
		t.Error("SSHPublicKey should not be empty (key was generated before send)")
	}
}

func TestGenerateEphemeralKeyPairFromUtil(t *testing.T) {
	// Verify the extracted generateEphemeralKeyPair still works correctly
	// when called from sshutil.go context.
	pubKey, privKeyPath, cleanup, err := generateEphemeralKeyPair()
	if err != nil {
		t.Fatalf("generateEphemeralKeyPair: %v", err)
	}
	defer cleanup()

	if pubKey == "" {
		t.Error("public key is empty")
	}
	if !strings.HasPrefix(pubKey, "ssh-ed25519 ") {
		t.Errorf("public key not in expected format, got prefix: %q", pubKey[:min(30, len(pubKey))])
	}

	if _, err := os.Stat(privKeyPath); os.IsNotExist(err) {
		t.Error("private key temp file does not exist")
	}

	// Verify file permissions are 0600.
	info, err := os.Stat(privKeyPath)
	if err != nil {
		t.Fatalf("stat private key: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("private key permissions = %o, want 0600", perm)
	}

	cleanup()
	if _, err := os.Stat(privKeyPath); !os.IsNotExist(err) {
		t.Error("cleanup did not remove private key file")
	}
}

func TestLookupInstanceAZFromUtil(t *testing.T) {
	// Verify lookupInstanceAZ works when called through sshutil.go.
	mock := &mockDescribeForSSH{
		output: makeRunningInstanceWithAZ("i-test", "default", "alice", "1.2.3.4", "ap-southeast-1b"),
	}

	az, err := lookupInstanceAZ(context.Background(), mock, "i-test")
	if err != nil {
		t.Fatalf("lookupInstanceAZ: %v", err)
	}
	if az != "ap-southeast-1b" {
		t.Errorf("az = %q, want %q", az, "ap-southeast-1b")
	}
}

func TestLookupInstanceAZNoPlacement(t *testing.T) {
	mock := &mockDescribeForSSH{
		output: &ec2.DescribeInstancesOutput{},
	}

	_, err := lookupInstanceAZ(context.Background(), mock, "i-noplacement")
	if err == nil {
		t.Fatal("expected error for missing placement")
	}
	if !strings.Contains(err.Error(), "no placement info") {
		t.Errorf("error %q does not mention missing placement", err.Error())
	}
}

// --- TOFURemoteRunner tests ---

// tofuMockInner is a mock RemoteCommandRunner that records calls for TOFU tests.
type tofuMockInner struct {
	calls  int
	output []byte
	err    error
}

func (m *tofuMockInner) run(
	ctx context.Context,
	sendKey mintaws.SendSSHPublicKeyAPI,
	instanceID, az, host string,
	port int,
	user string,
	command []string,
) ([]byte, error) {
	m.calls++
	return m.output, m.err
}

func TestTOFURemoteRunnerFirstCallTriggersKeyscan(t *testing.T) {
	store := sshconfig.NewHostKeyStore(t.TempDir())
	scanCalls := 0
	scanner := func(host string, port int) (string, string, error) {
		scanCalls++
		return "SHA256:testfp", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest", nil
	}

	inner := &tofuMockInner{output: []byte("ok")}
	runner := NewTOFURemoteRunner(inner.run, store, scanner, "default")

	ctx := context.Background()
	mock := &mockSendKeyForRemote{
		output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
	}

	// First call should trigger keyscan.
	out, err := runner.Run(ctx, mock, "i-test", "us-east-1a", "1.2.3.4", 41122, "ubuntu", []string{"whoami"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != "ok" {
		t.Errorf("output = %q, want %q", string(out), "ok")
	}
	if scanCalls != 1 {
		t.Errorf("keyscan calls = %d, want 1", scanCalls)
	}
	if inner.calls != 1 {
		t.Errorf("inner calls = %d, want 1", inner.calls)
	}

	// Verify key was recorded.
	matched, _, checkErr := store.CheckKey("default", "SHA256:testfp")
	if checkErr != nil {
		t.Fatalf("CheckKey: %v", checkErr)
	}
	if !matched {
		t.Error("host key should have been recorded via TOFU")
	}
}

func TestTOFURemoteRunnerSecondCallSkipsKeyscan(t *testing.T) {
	store := sshconfig.NewHostKeyStore(t.TempDir())
	scanCalls := 0
	scanner := func(host string, port int) (string, string, error) {
		scanCalls++
		return "SHA256:testfp", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest", nil
	}

	inner := &tofuMockInner{output: []byte("ok")}
	runner := NewTOFURemoteRunner(inner.run, store, scanner, "default")

	ctx := context.Background()
	mock := &mockSendKeyForRemote{
		output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
	}

	// First call.
	_, err := runner.Run(ctx, mock, "i-test", "us-east-1a", "1.2.3.4", 41122, "ubuntu", []string{"cmd1"})
	if err != nil {
		t.Fatalf("first call error: %v", err)
	}

	// Second call should skip keyscan.
	_, err = runner.Run(ctx, mock, "i-test", "us-east-1a", "1.2.3.4", 41122, "ubuntu", []string{"cmd2"})
	if err != nil {
		t.Fatalf("second call error: %v", err)
	}

	if scanCalls != 1 {
		t.Errorf("keyscan calls = %d, want 1 (second call should be cached)", scanCalls)
	}
	if inner.calls != 2 {
		t.Errorf("inner calls = %d, want 2", inner.calls)
	}
}

func TestTOFURemoteRunnerKeyMismatchRejects(t *testing.T) {
	store := sshconfig.NewHostKeyStore(t.TempDir())
	// Pre-record a different key.
	if err := store.RecordKey("default", "SHA256:oldfp"); err != nil {
		t.Fatalf("RecordKey: %v", err)
	}

	scanner := func(host string, port int) (string, string, error) {
		return "SHA256:newfp", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINew", nil
	}

	inner := &tofuMockInner{output: []byte("should not run")}
	runner := NewTOFURemoteRunner(inner.run, store, scanner, "default")

	ctx := context.Background()
	mock := &mockSendKeyForRemote{
		output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
	}

	_, err := runner.Run(ctx, mock, "i-test", "us-east-1a", "1.2.3.4", 41122, "ubuntu", []string{"whoami"})
	if err == nil {
		t.Fatal("expected error for host key mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "HOST KEY CHANGED") {
		t.Errorf("error should mention HOST KEY CHANGED, got: %s", err.Error())
	}
	if inner.calls != 0 {
		t.Errorf("inner should not be called on key mismatch, got %d calls", inner.calls)
	}
}

func TestTOFURemoteRunnerKeyscanError(t *testing.T) {
	store := sshconfig.NewHostKeyStore(t.TempDir())
	scanner := func(host string, port int) (string, string, error) {
		return "", "", fmt.Errorf("connection refused")
	}

	inner := &tofuMockInner{output: []byte("should not run")}
	runner := NewTOFURemoteRunner(inner.run, store, scanner, "default")

	ctx := context.Background()
	mock := &mockSendKeyForRemote{
		output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
	}

	_, err := runner.Run(ctx, mock, "i-test", "us-east-1a", "1.2.3.4", 41122, "ubuntu", []string{"whoami"})
	if err == nil {
		t.Fatal("expected error for keyscan failure, got nil")
	}
	if !strings.Contains(err.Error(), "scanning host key") {
		t.Errorf("error should mention scanning host key, got: %s", err.Error())
	}
	if inner.calls != 0 {
		t.Errorf("inner should not be called on keyscan error, got %d calls", inner.calls)
	}
}

func TestTOFURemoteRunnerMatchingKeyProceeds(t *testing.T) {
	store := sshconfig.NewHostKeyStore(t.TempDir())
	// Pre-record matching key.
	if err := store.RecordKey("default", "SHA256:matchfp"); err != nil {
		t.Fatalf("RecordKey: %v", err)
	}

	scanner := func(host string, port int) (string, string, error) {
		return "SHA256:matchfp", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest", nil
	}

	inner := &tofuMockInner{output: []byte("matched")}
	runner := NewTOFURemoteRunner(inner.run, store, scanner, "default")

	ctx := context.Background()
	mock := &mockSendKeyForRemote{
		output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
	}

	out, err := runner.Run(ctx, mock, "i-test", "us-east-1a", "1.2.3.4", 41122, "ubuntu", []string{"whoami"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != "matched" {
		t.Errorf("output = %q, want %q", string(out), "matched")
	}
	if inner.calls != 1 {
		t.Errorf("inner calls = %d, want 1", inner.calls)
	}
}

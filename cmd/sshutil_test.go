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

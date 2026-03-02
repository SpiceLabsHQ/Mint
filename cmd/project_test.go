package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2instanceconnect"
	mintaws "github.com/SpiceLabsHQ/Mint/internal/aws"
	"github.com/SpiceLabsHQ/Mint/internal/cli"
	"github.com/SpiceLabsHQ/Mint/internal/sshconfig"
	"github.com/spf13/cobra"
)

// --- Project name validation tests ---

func TestValidateProjectName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		// Valid names.
		{name: "simple alpha", input: "myproject", wantErr: false},
		{name: "alphanumeric", input: "project123", wantErr: false},
		{name: "with hyphens", input: "my-project", wantErr: false},
		{name: "with underscores", input: "my_project", wantErr: false},
		{name: "with dots", input: "my.project", wantErr: false},
		{name: "mixed valid chars", input: "my-project_v2.0", wantErr: false},
		{name: "single char", input: "a", wantErr: false},
		{name: "starts with digit", input: "1project", wantErr: false},

		// Invalid names: shell metacharacters.
		{name: "semicolon injection", input: "foo;rm -rf /", wantErr: true},
		{name: "backtick injection", input: "foo`whoami`", wantErr: true},
		{name: "dollar substitution", input: "foo$(whoami)", wantErr: true},
		{name: "pipe injection", input: "foo|cat /etc/passwd", wantErr: true},
		{name: "ampersand injection", input: "foo&&evil", wantErr: true},
		{name: "space in name", input: "foo bar", wantErr: true},
		{name: "newline injection", input: "foo\nbar", wantErr: true},
		{name: "single quote", input: "foo'bar", wantErr: true},
		{name: "double quote", input: "foo\"bar", wantErr: true},
		{name: "angle brackets", input: "foo>bar", wantErr: true},
		{name: "slash in name", input: "foo/bar", wantErr: true},
		{name: "backslash in name", input: "foo\\bar", wantErr: true},

		// Invalid names: structural issues.
		{name: "empty string", input: "", wantErr: true},
		{name: "starts with hyphen", input: "-project", wantErr: true},
		{name: "starts with dot", input: ".project", wantErr: true},
		{name: "starts with underscore", input: "_project", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateProjectName(tt.input)
			if tt.wantErr && err == nil {
				t.Errorf("validateProjectName(%q) expected error, got nil", tt.input)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("validateProjectName(%q) unexpected error: %v", tt.input, err)
			}
		})
	}
}

// --- URL parsing tests ---

func TestExtractProjectName(t *testing.T) {
	tests := []struct {
		url      string
		wantName string
		wantErr  bool
	}{
		{url: "https://github.com/org/repo.git", wantName: "repo"},
		{url: "https://github.com/org/repo", wantName: "repo"},
		{url: "git@github.com:org/repo.git", wantName: "repo"},
		{url: "git@github.com:org/repo", wantName: "repo"},
		{url: "https://gitlab.com/group/subgroup/project.git", wantName: "project"},
		{url: "git@gitlab.com:group/subgroup/project.git", wantName: "project"},
		{url: "https://github.com/org/my-cool-repo.git", wantName: "my-cool-repo"},
		{url: "", wantErr: true},
		{url: "not-a-url", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			name, err := extractProjectName(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for URL %q, got name %q", tt.url, name)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for URL %q: %v", tt.url, err)
			}
			if name != tt.wantName {
				t.Errorf("extractProjectName(%q) = %q, want %q", tt.url, name, tt.wantName)
			}
		})
	}
}

func TestExpandGitHubShorthand(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"SpiceLabsHQ/bqe-lumen", "https://github.com/SpiceLabsHQ/bqe-lumen"},
		{"org/repo.git", "https://github.com/org/repo.git"},
		{"https://github.com/org/repo", "https://github.com/org/repo"},
		{"git@github.com:org/repo.git", "git@github.com:org/repo.git"},
		{"not-a-url", "not-a-url"},
		{"too/many/parts", "too/many/parts"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := expandGitHubShorthand(tt.input)
			if got != tt.want {
				t.Errorf("expandGitHubShorthand(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// --- Mock infrastructure for project tests ---

// mockDescribeForProject implements mintaws.DescribeInstancesAPI for project tests.
type mockDescribeForProject struct {
	output *ec2.DescribeInstancesOutput
	err    error
}

func (m *mockDescribeForProject) DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return m.output, m.err
}

// mockSendKeyForProject implements mintaws.SendSSHPublicKeyAPI for project tests.
type mockSendKeyForProject struct {
	output *ec2instanceconnect.SendSSHPublicKeyOutput
	err    error
}

func (m *mockSendKeyForProject) SendSSHPublicKey(ctx context.Context, params *ec2instanceconnect.SendSSHPublicKeyInput, optFns ...func(*ec2instanceconnect.Options)) (*ec2instanceconnect.SendSSHPublicKeyOutput, error) {
	return m.output, m.err
}

// projectRemoteCall records a single remote command invocation.
type projectRemoteCall struct {
	instanceID string
	az         string
	host       string
	port       int
	user       string
	command    []string
}

// projectMockRemote records calls and returns configurable results per call index.
type projectMockRemote struct {
	calls   []projectRemoteCall
	outputs [][]byte
	errors  []error
}

func (m *projectMockRemote) run(ctx context.Context, sendKey mintaws.SendSSHPublicKeyAPI, instanceID, az, host string, port int, user string, command []string) ([]byte, error) {
	idx := len(m.calls)
	m.calls = append(m.calls, projectRemoteCall{
		instanceID: instanceID,
		az:         az,
		host:       host,
		port:       port,
		user:       user,
		command:    command,
	})

	if idx < len(m.errors) && m.errors[idx] != nil {
		return nil, m.errors[idx]
	}
	if idx < len(m.outputs) {
		return m.outputs[idx], nil
	}
	return nil, nil
}

// projectStreamingCall records a single streaming remote command invocation.
type projectStreamingCall struct {
	instanceID string
	az         string
	host       string
	port       int
	user       string
	command    []string
}

// projectMockStreamingRemote records streaming calls and returns configurable results.
type projectMockStreamingRemote struct {
	calls   []projectStreamingCall
	outputs [][]byte
	errors  []error
}

func (m *projectMockStreamingRemote) run(ctx context.Context, sendKey mintaws.SendSSHPublicKeyAPI, instanceID, az, host string, port int, user string, command []string, stderr io.Writer) ([]byte, error) {
	idx := len(m.calls)
	m.calls = append(m.calls, projectStreamingCall{
		instanceID: instanceID,
		az:         az,
		host:       host,
		port:       port,
		user:       user,
		command:    command,
	})

	if idx < len(m.errors) && m.errors[idx] != nil {
		return nil, m.errors[idx]
	}
	if idx < len(m.outputs) {
		return m.outputs[idx], nil
	}
	return nil, nil
}

func makeRunningInstanceForProject(id, vmName, owner, ip, az string) *ec2.DescribeInstancesOutput {
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

func makeStoppedInstanceForProject(id, vmName, owner string) *ec2.DescribeInstancesOutput {
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

func newTestRootForProject() *cobra.Command {
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

// --- Command tests ---

func TestProjectAddCommand(t *testing.T) {
	tests := []struct {
		name                string
		describe            *mockDescribeForProject
		sendKey             *mockSendKeyForProject
		remote              *projectMockRemote
		streaming           *projectMockStreamingRemote
		owner               string
		args                []string
		wantErr             bool
		wantErrContain      string
		wantCalls           int
		wantStreamingCalls  int
		checkCalls          func(t *testing.T, calls []projectRemoteCall)
		checkStreamingCalls func(t *testing.T, calls []projectStreamingCall)
		checkOutput         func(t *testing.T, output string)
	}{
		{
			name: "successful project add with https url",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForProject{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			// remote: test -d (dir doesn't exist), docker ps, tmux new-session
			remote: &projectMockRemote{
				outputs: [][]byte{nil, []byte("abc123def456\n"), nil},
				errors:  []error{fmt.Errorf("exit status 1"), nil, nil},
			},
			// streaming: clone, devcontainer up
			streaming:          &projectMockStreamingRemote{outputs: [][]byte{nil, nil}, errors: []error{nil, nil}},
			owner:              "alice",
			args:               []string{"project", "add", "https://github.com/org/repo.git"},
			wantCalls:          3,
			wantStreamingCalls: 2,
			checkStreamingCalls: func(t *testing.T, calls []projectStreamingCall) {
				t.Helper()
				// Streaming call 0: git clone with credential suppression
				cloneCmd := strings.Join(calls[0].command, " ")
				if !strings.Contains(cloneCmd, "clone") {
					t.Errorf("first streaming call should be git clone, got: %s", cloneCmd)
				}
				if !strings.Contains(cloneCmd, "GIT_CONFIG_NOSYSTEM=1") {
					t.Errorf("clone should suppress credentials via GIT_CONFIG_NOSYSTEM=1, got: %s", cloneCmd)
				}
				if !strings.Contains(cloneCmd, "https://github.com/org/repo.git") {
					t.Errorf("clone should include URL, got: %s", cloneCmd)
				}
				if !strings.Contains(cloneCmd, "/mint/projects/repo") {
					t.Errorf("clone should target /mint/projects/repo, got: %s", cloneCmd)
				}
				// Streaming call 1: devcontainer up
				buildCmd := strings.Join(calls[1].command, " ")
				if !strings.Contains(buildCmd, "devcontainer up") {
					t.Errorf("second streaming call should be devcontainer up, got: %s", buildCmd)
				}
				if !strings.Contains(buildCmd, "--workspace-folder /mint/projects/repo") {
					t.Errorf("devcontainer up should target workspace folder, got: %s", buildCmd)
				}
			},
			checkCalls: func(t *testing.T, calls []projectRemoteCall) {
				t.Helper()
				// Remote call 0: state check (test -d, fails because dir doesn't exist)
				preCheck := strings.Join(calls[0].command, " ")
				if !strings.Contains(preCheck, "test -d") {
					t.Errorf("first remote call should be dir check, got: %s", preCheck)
				}
				// Remote call 1: docker ps to discover container
				dockerCmd := strings.Join(calls[1].command, " ")
				if !strings.Contains(dockerCmd, "docker ps -q") {
					t.Errorf("second remote call should be docker ps, got: %s", dockerCmd)
				}
				// Remote call 2: tmux new-session with docker exec
				tmuxCmd := strings.Join(calls[2].command, " ")
				if !strings.Contains(tmuxCmd, "tmux new-session") {
					t.Errorf("third remote call should be tmux new-session, got: %s", tmuxCmd)
				}
				if !strings.Contains(tmuxCmd, "-s repo") {
					t.Errorf("tmux session should use project name, got: %s", tmuxCmd)
				}
				if !strings.Contains(tmuxCmd, "docker exec -it abc123def456 /bin/bash") {
					t.Errorf("tmux session should docker exec into container, got: %s", tmuxCmd)
				}
			},
			checkOutput: func(t *testing.T, output string) {
				t.Helper()
				if !strings.Contains(output, "Cloning") {
					t.Errorf("output should show cloning progress, got: %s", output)
				}
				if !strings.Contains(output, "Building devcontainer") {
					t.Errorf("output should show build progress, got: %s", output)
				}
				if !strings.Contains(output, "Creating tmux session") {
					t.Errorf("output should show session progress, got: %s", output)
				}
			},
		},
		{
			name: "successful project add with git ssh url",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForProject{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remote:             &projectMockRemote{outputs: [][]byte{nil, []byte("container789\n"), nil}, errors: []error{fmt.Errorf("exit status 1"), nil, nil}},
			streaming:          &projectMockStreamingRemote{outputs: [][]byte{nil, nil}, errors: []error{nil, nil}},
			owner:              "alice",
			args:               []string{"project", "add", "git@github.com:org/my-app.git"},
			wantCalls:          3,
			wantStreamingCalls: 2,
			checkStreamingCalls: func(t *testing.T, calls []projectStreamingCall) {
				t.Helper()
				cloneCmd := strings.Join(calls[0].command, " ")
				if !strings.Contains(cloneCmd, "/mint/projects/my-app") {
					t.Errorf("clone should target /mint/projects/my-app, got: %s", cloneCmd)
				}
			},
		},
		{
			name: "name flag overrides extracted name",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForProject{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remote:             &projectMockRemote{outputs: [][]byte{nil, []byte("ctr1\n"), nil}, errors: []error{fmt.Errorf("exit status 1"), nil, nil}},
			streaming:          &projectMockStreamingRemote{outputs: [][]byte{nil, nil}, errors: []error{nil, nil}},
			owner:              "alice",
			args:               []string{"project", "add", "--name", "custom-name", "https://github.com/org/repo.git"},
			wantCalls:          3,
			wantStreamingCalls: 2,
			checkCalls: func(t *testing.T, calls []projectRemoteCall) {
				t.Helper()
				tmuxCmd := strings.Join(calls[2].command, " ")
				if !strings.Contains(tmuxCmd, "-s custom-name") {
					t.Errorf("tmux session should use custom name, got: %s", tmuxCmd)
				}
			},
			checkStreamingCalls: func(t *testing.T, calls []projectStreamingCall) {
				t.Helper()
				cloneCmd := strings.Join(calls[0].command, " ")
				if !strings.Contains(cloneCmd, "/mint/projects/custom-name") {
					t.Errorf("clone should use custom name, got: %s", cloneCmd)
				}
			},
		},
		{
			name: "branch flag passed to git clone",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForProject{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remote:             &projectMockRemote{outputs: [][]byte{nil, []byte("ctr1\n"), nil}, errors: []error{fmt.Errorf("exit status 1"), nil, nil}},
			streaming:          &projectMockStreamingRemote{outputs: [][]byte{nil, nil}, errors: []error{nil, nil}},
			owner:              "alice",
			args:               []string{"project", "add", "--branch", "develop", "https://github.com/org/repo.git"},
			wantCalls:          3,
			wantStreamingCalls: 2,
			checkStreamingCalls: func(t *testing.T, calls []projectStreamingCall) {
				t.Helper()
				cloneCmd := strings.Join(calls[0].command, " ")
				if !strings.Contains(cloneCmd, "--branch develop") {
					t.Errorf("clone should include --branch flag, got: %s", cloneCmd)
				}
			},
		},
		{
			name: "vm not found returns actionable error",
			describe: &mockDescribeForProject{
				output: &ec2.DescribeInstancesOutput{},
			},
			sendKey:        &mockSendKeyForProject{},
			remote:         &projectMockRemote{},
			streaming:      &projectMockStreamingRemote{},
			owner:          "alice",
			args:           []string{"project", "add", "https://github.com/org/repo.git"},
			wantErr:        true,
			wantErrContain: "mint up",
		},
		{
			name: "stopped vm returns actionable error",
			describe: &mockDescribeForProject{
				output: makeStoppedInstanceForProject("i-abc123", "default", "alice"),
			},
			sendKey:        &mockSendKeyForProject{},
			remote:         &projectMockRemote{},
			streaming:      &projectMockStreamingRemote{},
			owner:          "alice",
			args:           []string{"project", "add", "https://github.com/org/repo.git"},
			wantErr:        true,
			wantErrContain: "not running",
		},
		{
			name: "resume from build when dir exists but no container",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForProject{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			// remote: test -d (exists), docker ps check (empty),
			//         docker ps for ID after build, tmux new-session
			remote: &projectMockRemote{
				outputs: [][]byte{nil, []byte(""), []byte("ctr999\n"), nil},
				errors:  []error{nil, nil, nil, nil},
			},
			// streaming: devcontainer up only (clone skipped)
			streaming:          &projectMockStreamingRemote{outputs: [][]byte{nil}, errors: []error{nil}},
			owner:              "alice",
			args:               []string{"project", "add", "https://github.com/org/repo.git"},
			wantCalls:          4,
			wantStreamingCalls: 1,
			checkStreamingCalls: func(t *testing.T, calls []projectStreamingCall) {
				t.Helper()
				// Only devcontainer up should be called (no clone)
				buildCmd := strings.Join(calls[0].command, " ")
				if !strings.Contains(buildCmd, "devcontainer up") {
					t.Errorf("streaming call should be devcontainer up, got: %s", buildCmd)
				}
			},
			checkOutput: func(t *testing.T, output string) {
				t.Helper()
				if !strings.Contains(output, "Found existing clone") {
					t.Errorf("output should indicate resume from existing clone, got: %s", output)
				}
				if strings.Contains(output, "Cloning") {
					t.Errorf("output should NOT show cloning (skipped), got: %s", output)
				}
			},
		},
		{
			name: "resume from tmux when dir and container exist",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForProject{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			// remote: test -d (exists), docker ps check (container running), tmux has-session (error),
			//         tmux new-session
			remote: &projectMockRemote{
				outputs: [][]byte{nil, []byte("abc123\n"), nil, nil},
				errors:  []error{nil, nil, fmt.Errorf("exit status 1"), nil},
			},
			// streaming: nothing (both clone and devcontainer skipped)
			streaming:          &projectMockStreamingRemote{},
			owner:              "alice",
			args:               []string{"project", "add", "https://github.com/org/repo.git"},
			wantCalls:          4,
			wantStreamingCalls: 0,
			checkCalls: func(t *testing.T, calls []projectRemoteCall) {
				t.Helper()
				// Last call should be tmux new-session with docker exec
				tmuxCmd := strings.Join(calls[3].command, " ")
				if !strings.Contains(tmuxCmd, "tmux new-session") {
					t.Errorf("last call should be tmux new-session, got: %s", tmuxCmd)
				}
				if !strings.Contains(tmuxCmd, "docker exec -it abc123 /bin/bash") {
					t.Errorf("tmux should docker exec into container, got: %s", tmuxCmd)
				}
			},
			checkOutput: func(t *testing.T, output string) {
				t.Helper()
				if !strings.Contains(output, "Found running container") {
					t.Errorf("output should indicate resume from running container, got: %s", output)
				}
				if strings.Contains(output, "Cloning") {
					t.Errorf("output should NOT show cloning (skipped), got: %s", output)
				}
				if strings.Contains(output, "Building devcontainer") {
					t.Errorf("output should NOT show building (skipped), got: %s", output)
				}
			},
		},
		{
			name: "already complete when dir container and tmux all exist",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForProject{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			// remote: test -d (exists), docker ps check (container running), tmux has-session (success)
			remote: &projectMockRemote{
				outputs: [][]byte{nil, []byte("abc123\n"), nil},
				errors:  []error{nil, nil, nil},
			},
			// streaming: nothing
			streaming:          &projectMockStreamingRemote{},
			owner:              "alice",
			args:               []string{"project", "add", "https://github.com/org/repo.git"},
			wantCalls:          3,
			wantStreamingCalls: 0,
			checkOutput: func(t *testing.T, output string) {
				t.Helper()
				if !strings.Contains(output, "already fully set up") {
					t.Errorf("output should indicate already set up, got: %s", output)
				}
				if strings.Contains(output, "Cloning") {
					t.Errorf("output should NOT show cloning, got: %s", output)
				}
				if strings.Contains(output, "Building devcontainer") {
					t.Errorf("output should NOT show building, got: %s", output)
				}
				if strings.Contains(output, "Creating tmux session") {
					t.Errorf("output should NOT show session creation, got: %s", output)
				}
			},
		},
		{
			name: "devcontainer build failure returns clear error",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForProject{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remote: &projectMockRemote{
				outputs: [][]byte{nil},
				errors:  []error{fmt.Errorf("exit status 1")},
			},
			streaming: &projectMockStreamingRemote{
				outputs: [][]byte{nil},
				errors:  []error{nil, fmt.Errorf("devcontainer build failed: Dockerfile syntax error")},
			},
			owner:              "alice",
			args:               []string{"project", "add", "https://github.com/org/repo.git"},
			wantErr:            true,
			wantErrContain:     "devcontainer",
			wantCalls:          1,
			wantStreamingCalls: 2,
		},
		{
			name: "missing git url argument",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey:   &mockSendKeyForProject{},
			remote:    &projectMockRemote{},
			streaming: &projectMockStreamingRemote{},
			owner:     "alice",
			args:      []string{"project", "add"},
			wantErr:   true,
		},
		{
			name: "non-default vm name",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-dev456", "dev", "alice", "10.0.0.1", "us-west-2a"),
			},
			sendKey: &mockSendKeyForProject{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remote:             &projectMockRemote{outputs: [][]byte{nil, []byte("ctr1\n"), nil}, errors: []error{fmt.Errorf("exit status 1"), nil, nil}},
			streaming:          &projectMockStreamingRemote{outputs: [][]byte{nil, nil}, errors: []error{nil, nil}},
			owner:              "alice",
			args:               []string{"--vm", "dev", "project", "add", "https://github.com/org/repo.git"},
			wantCalls:          3,
			wantStreamingCalls: 2,
			checkCalls: func(t *testing.T, calls []projectRemoteCall) {
				t.Helper()
				if calls[0].host != "10.0.0.1" {
					t.Errorf("expected host 10.0.0.1, got %s", calls[0].host)
				}
				if calls[0].az != "us-west-2a" {
					t.Errorf("expected az us-west-2a, got %s", calls[0].az)
				}
			},
		},
		{
			name: "name flag with shell metacharacters rejected",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForProject{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remote:         &projectMockRemote{},
			streaming:      &projectMockStreamingRemote{},
			owner:          "alice",
			args:           []string{"project", "add", "--name", "foo;rm -rf /", "https://github.com/org/repo.git"},
			wantErr:        true,
			wantErrContain: "invalid project name",
			wantCalls:      0,
		},
		{
			name: "name flag with backtick injection rejected",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForProject{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remote:         &projectMockRemote{},
			streaming:      &projectMockStreamingRemote{},
			owner:          "alice",
			args:           []string{"project", "add", "--name", "foo`whoami`", "https://github.com/org/repo.git"},
			wantErr:        true,
			wantErrContain: "invalid project name",
			wantCalls:      0,
		},
		{
			name: "describe API error propagates",
			describe: &mockDescribeForProject{
				err: fmt.Errorf("throttled"),
			},
			sendKey:        &mockSendKeyForProject{},
			remote:         &projectMockRemote{},
			streaming:      &projectMockStreamingRemote{},
			owner:          "alice",
			args:           []string{"project", "add", "https://github.com/org/repo.git"},
			wantErr:        true,
			wantErrContain: "throttled",
		},
		{
			name: "remote commands use correct SSH params",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForProject{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remote:             &projectMockRemote{outputs: [][]byte{nil, []byte("ctr1\n"), nil}, errors: []error{fmt.Errorf("exit status 1"), nil, nil}},
			streaming:          &projectMockStreamingRemote{outputs: [][]byte{nil, nil}, errors: []error{nil, nil}},
			owner:              "alice",
			args:               []string{"project", "add", "https://github.com/org/repo.git"},
			wantCalls:          3,
			wantStreamingCalls: 2,
			checkCalls: func(t *testing.T, calls []projectRemoteCall) {
				t.Helper()
				for i, call := range calls {
					if call.instanceID != "i-abc123" {
						t.Errorf("call %d: instanceID = %q, want i-abc123", i, call.instanceID)
					}
					if call.host != "1.2.3.4" {
						t.Errorf("call %d: host = %q, want 1.2.3.4", i, call.host)
					}
					if call.port != 41122 {
						t.Errorf("call %d: port = %d, want 41122", i, call.port)
					}
					if call.user != "ubuntu" {
						t.Errorf("call %d: user = %q, want ubuntu", i, call.user)
					}
					if call.az != "us-east-1a" {
						t.Errorf("call %d: az = %q, want us-east-1a", i, call.az)
					}
				}
			},
		},
		{
			name: "tmux session created as bare session without docker exec",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForProject{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			// remote: test -d (dir doesn't exist), docker ps (empty), tmux new-session (bare)
			remote: &projectMockRemote{
				outputs: [][]byte{nil, []byte(""), nil},
				errors:  []error{fmt.Errorf("exit status 1"), nil, nil},
			},
			streaming:          &projectMockStreamingRemote{outputs: [][]byte{nil, nil}, errors: []error{nil, nil}},
			owner:              "alice",
			args:               []string{"project", "add", "https://github.com/org/repo.git"},
			wantCalls:          3,
			wantStreamingCalls: 2,
			checkCalls: func(t *testing.T, calls []projectRemoteCall) {
				t.Helper()
				// Remote call 1: docker ps (returns empty)
				dockerCmd := strings.Join(calls[1].command, " ")
				if !strings.Contains(dockerCmd, "docker ps -q") {
					t.Errorf("second remote call should be docker ps, got: %s", dockerCmd)
				}
				// Remote call 2: bare tmux session (no docker exec)
				tmuxCmd := strings.Join(calls[2].command, " ")
				if !strings.Contains(tmuxCmd, "tmux new-session") {
					t.Errorf("third remote call should be tmux new-session, got: %s", tmuxCmd)
				}
				if strings.Contains(tmuxCmd, "docker exec") {
					t.Errorf("tmux session should NOT have docker exec when container not found, got: %s", tmuxCmd)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := new(bytes.Buffer)

			deps := &projectAddDeps{
				describe:        tt.describe,
				sendKey:         tt.sendKey,
				owner:           tt.owner,
				remote:          tt.remote.run,
				streamingRunner: tt.streaming.run,
			}

			projectCmd := newProjectCommandWithDeps(deps)
			root := newTestRootForProject()
			root.AddCommand(projectCmd)
			root.SetOut(buf)
			root.SetErr(buf)
			root.SetArgs(tt.args)

			err := root.Execute()

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.wantErrContain != "" && !strings.Contains(err.Error(), tt.wantErrContain) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.wantErrContain)
				}
				if tt.wantCalls > 0 && len(tt.remote.calls) != tt.wantCalls {
					t.Errorf("expected %d remote calls, got %d", tt.wantCalls, len(tt.remote.calls))
				}
				if tt.wantStreamingCalls > 0 && len(tt.streaming.calls) != tt.wantStreamingCalls {
					t.Errorf("expected %d streaming calls, got %d", tt.wantStreamingCalls, len(tt.streaming.calls))
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantCalls > 0 && len(tt.remote.calls) != tt.wantCalls {
				t.Errorf("expected %d remote calls, got %d", tt.wantCalls, len(tt.remote.calls))
			}

			if tt.wantStreamingCalls > 0 && len(tt.streaming.calls) != tt.wantStreamingCalls {
				t.Errorf("expected %d streaming calls, got %d", tt.wantStreamingCalls, len(tt.streaming.calls))
			}

			if tt.checkCalls != nil {
				tt.checkCalls(t, tt.remote.calls)
			}

			if tt.checkStreamingCalls != nil {
				tt.checkStreamingCalls(t, tt.streaming.calls)
			}

			if tt.checkOutput != nil {
				tt.checkOutput(t, buf.String())
			}
		})
	}
}

// TestBuildCloneCommandSuppressesCredentials verifies that buildCloneCommand
// sets the three env vars that ensure a fully anonymous clone regardless of
// what credential helpers are installed on the VM:
//   - GIT_TERMINAL_PROMPT=0: no interactive prompts
//   - GIT_CONFIG_NOSYSTEM=1: skips /etc/gitconfig (system credential helpers)
//   - GIT_CONFIG_GLOBAL=/dev/null: skips ~/.gitconfig (user credential helpers)
func TestBuildCloneCommandSuppressesCredentials(t *testing.T) {
	tests := []struct {
		name        string
		gitURL      string
		projectPath string
		branch      string
	}{
		{
			name:        "https url no branch",
			gitURL:      "https://github.com/org/repo.git",
			projectPath: "/mint/projects/repo",
			branch:      "",
		},
		{
			name:        "https url with branch",
			gitURL:      "https://github.com/org/repo.git",
			projectPath: "/mint/projects/repo",
			branch:      "develop",
		},
		{
			name:        "ssh url no branch",
			gitURL:      "git@gitlab.com:org/repo.git",
			projectPath: "/mint/projects/repo",
			branch:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := buildCloneCommand(tt.gitURL, tt.projectPath, tt.branch)

			// Verify the command starts with: env GIT_TERMINAL_PROMPT=0 GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null git
			wantPrefix := []string{"env", "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "git"}
			if len(cmd) < len(wantPrefix) {
				t.Fatalf("command too short: %v", cmd)
			}
			for i, want := range wantPrefix {
				if cmd[i] != want {
					t.Errorf("cmd[%d] = %q, want %q (full cmd: %v)", i, cmd[i], want, cmd)
				}
			}
			foundClone := false
			for _, arg := range cmd {
				if arg == "clone" {
					foundClone = true
					break
				}
			}
			if !foundClone {
				t.Errorf("clone subcommand missing from %v", cmd)
			}

			// Verify URL and path are still present.
			foundURL := false
			foundPath := false
			for _, arg := range cmd {
				if arg == tt.gitURL {
					foundURL = true
				}
				if arg == tt.projectPath {
					foundPath = true
				}
			}
			if !foundURL {
				t.Errorf("git URL %q missing from %v", tt.gitURL, cmd)
			}
			if !foundPath {
				t.Errorf("project path %q missing from %v", tt.projectPath, cmd)
			}

			// Verify branch flag is present when specified.
			if tt.branch != "" {
				foundBranch := false
				for i, arg := range cmd {
					if arg == "--branch" && i+1 < len(cmd) && cmd[i+1] == tt.branch {
						foundBranch = true
						break
					}
				}
				if !foundBranch {
					t.Errorf("--branch %q missing from %v", tt.branch, cmd)
				}
			}
		})
	}
}

func TestProjectParentCommandHasNoRun(t *testing.T) {
	// The parent "project" command should not have a RunE -- it only groups subcommands.
	cmd := newProjectCommand()
	if cmd.RunE != nil {
		t.Error("project parent command should not have RunE")
	}
	if cmd.Run != nil {
		t.Error("project parent command should not have Run")
	}
}

// --- Project list tests ---

func TestProjectListCommand(t *testing.T) {
	tests := []struct {
		name           string
		describe       *mockDescribeForProject
		sendKey        *mockSendKeyForProject
		remote         *projectMockRemote
		owner          string
		vmName         string
		jsonOutput     bool
		wantErr        bool
		wantErrContain string
		wantOutput     []string
		wantNotOutput  []string
	}{
		{
			name: "lists projects with running containers",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForProject{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remote: &projectMockRemote{
				outputs: [][]byte{
					[]byte("myproject\nsidecar\n"),
					[]byte("myproject_devcontainer-app-1\tUp 2 hours\tmcr.microsoft.com/devcontainers/go:1.21\t/mint/projects/myproject\n"),
				},
				errors: []error{nil, nil},
			},
			owner:      "alice",
			wantOutput: []string{"myproject", "running", "mcr.microsoft.com/devcontainers/go:1.21", "sidecar", "none"},
		},
		{
			name: "json output returns array",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForProject{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remote: &projectMockRemote{
				outputs: [][]byte{
					[]byte("myproject\n"),
					[]byte("myproject_devcontainer-app-1\tUp 2 hours\tmcr.microsoft.com/devcontainers/go:1.21\t/mint/projects/myproject\n"),
				},
				errors: []error{nil, nil},
			},
			owner:      "alice",
			jsonOutput: true,
			wantOutput: []string{`"name"`, `"myproject"`, `"container_status"`, `"running"`, `"image"`},
		},
		{
			name: "no projects directory shows message",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForProject{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remote: &projectMockRemote{
				outputs: [][]byte{
					[]byte(""),
					[]byte(""),
				},
				errors: []error{nil, nil},
			},
			owner:      "alice",
			wantOutput: []string{"No projects yet — run mint project add <git-url> to clone one."},
		},
		{
			name: "no projects json returns empty array",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForProject{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remote: &projectMockRemote{
				outputs: [][]byte{
					[]byte(""),
					[]byte(""),
				},
				errors: []error{nil, nil},
			},
			owner:      "alice",
			jsonOutput: true,
			wantOutput: []string{"[]"},
		},
		{
			name: "projects with no containers show none status",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForProject{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remote: &projectMockRemote{
				outputs: [][]byte{
					[]byte("experiments\n"),
					[]byte(""),
				},
				errors: []error{nil, nil},
			},
			owner:      "alice",
			wantOutput: []string{"experiments", "none"},
		},
		{
			name: "VM not found returns error",
			describe: &mockDescribeForProject{
				output: &ec2.DescribeInstancesOutput{},
			},
			sendKey:        &mockSendKeyForProject{},
			remote:         &projectMockRemote{},
			owner:          "alice",
			wantErr:        true,
			wantErrContain: "mint up",
		},
		{
			name: "stopped VM returns error",
			describe: &mockDescribeForProject{
				output: makeStoppedInstanceForProject("i-abc123", "default", "alice"),
			},
			sendKey:        &mockSendKeyForProject{},
			remote:         &projectMockRemote{},
			owner:          "alice",
			wantErr:        true,
			wantErrContain: "not running",
		},
		{
			name: "describe API error propagates",
			describe: &mockDescribeForProject{
				err: fmt.Errorf("throttled"),
			},
			sendKey:        &mockSendKeyForProject{},
			remote:         &projectMockRemote{},
			owner:          "alice",
			wantErr:        true,
			wantErrContain: "throttled",
		},
		{
			name: "ls command error propagates",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForProject{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remote: &projectMockRemote{
				errors: []error{fmt.Errorf("connection refused")},
			},
			owner:          "alice",
			wantErr:        true,
			wantErrContain: "connection refused",
		},
		{
			name: "non-default VM name",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-dev456", "dev", "alice", "10.0.0.1", "us-west-2a"),
			},
			sendKey: &mockSendKeyForProject{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remote: &projectMockRemote{
				outputs: [][]byte{
					[]byte("myapp\n"),
					[]byte(""),
				},
				errors: []error{nil, nil},
			},
			owner:      "alice",
			vmName:     "dev",
			wantOutput: []string{"myapp"},
		},
		{
			name: "multiple projects mixed container states",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForProject{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remote: &projectMockRemote{
				outputs: [][]byte{
					[]byte("myproject\nsidecar\nexperiments\n"),
					[]byte("myproject_devcontainer-app-1\tUp 2 hours\tmcr.microsoft.com/devcontainers/go:1.21\t/mint/projects/myproject\nsidecar_devcontainer-app-1\tExited (0) 5 minutes ago\tnode:18\t/mint/projects/sidecar\n"),
				},
				errors: []error{nil, nil},
			},
			owner:      "alice",
			wantOutput: []string{"myproject", "running", "sidecar", "exited", "experiments", "none"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := new(bytes.Buffer)

			listDeps := &projectListDeps{
				describe: tt.describe,
				sendKey:  tt.sendKey,
				owner:    tt.owner,
				remote:   tt.remote.run,
			}

			projectCmd := newProjectCommandWithListDeps(listDeps)
			root := newTestRootForProject()
			root.AddCommand(projectCmd)
			root.SetOut(buf)
			root.SetErr(buf)

			args := []string{"project", "list"}
			if tt.vmName != "" && tt.vmName != "default" {
				args = append([]string{"--vm", tt.vmName}, args...)
			}
			if tt.jsonOutput {
				args = append(args, "--json")
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

			output := buf.String()
			for _, want := range tt.wantOutput {
				if !strings.Contains(output, want) {
					t.Errorf("output missing %q, got:\n%s", want, output)
				}
			}
			for _, notWant := range tt.wantNotOutput {
				if strings.Contains(output, notWant) {
					t.Errorf("output should not contain %q, got:\n%s", notWant, output)
				}
			}

			// Validate JSON output is parseable as an array.
			if tt.jsonOutput {
				trimmed := strings.TrimSpace(output)
				var result []interface{}
				if err := json.Unmarshal([]byte(trimmed), &result); err != nil {
					t.Errorf("JSON output is not a valid array: %v\nOutput: %s", err, output)
				}
			}
		})
	}
}

func TestParseProjectsAndContainers(t *testing.T) {
	tests := []struct {
		name            string
		lsOutput        string
		dockerOutput    string
		expectedCount   int
		check           func(t *testing.T, projects []projectInfo)
	}{
		{
			name:          "projects with matching containers",
			lsOutput:      "myproject\nsidecar\n",
			dockerOutput:  "myproject_devcontainer-app-1\tUp 2 hours\tmcr.microsoft.com/devcontainers/go:1.21\t/mint/projects/myproject\n",
			expectedCount: 2,
			check: func(t *testing.T, projects []projectInfo) {
				if projects[0].Name != "myproject" {
					t.Errorf("name = %q, want myproject", projects[0].Name)
				}
				if projects[0].ContainerStatus != "running" {
					t.Errorf("status = %q, want running", projects[0].ContainerStatus)
				}
				if projects[0].Image != "mcr.microsoft.com/devcontainers/go:1.21" {
					t.Errorf("image = %q, want mcr.microsoft.com/devcontainers/go:1.21", projects[0].Image)
				}
				if projects[1].Name != "sidecar" {
					t.Errorf("name = %q, want sidecar", projects[1].Name)
				}
				if projects[1].ContainerStatus != "none" {
					t.Errorf("status = %q, want none", projects[1].ContainerStatus)
				}
			},
		},
		{
			name:          "empty projects",
			lsOutput:      "",
			dockerOutput:  "",
			expectedCount: 0,
		},
		{
			name:          "exited container",
			lsOutput:      "sidecar\n",
			dockerOutput:  "sidecar_devcontainer-app-1\tExited (0) 5 minutes ago\tnode:18\t/mint/projects/sidecar\n",
			expectedCount: 1,
			check: func(t *testing.T, projects []projectInfo) {
				if projects[0].ContainerStatus != "exited" {
					t.Errorf("status = %q, want exited", projects[0].ContainerStatus)
				}
				if projects[0].Image != "node:18" {
					t.Errorf("image = %q, want node:18", projects[0].Image)
				}
			},
		},
		{
			name:          "whitespace-only ls output",
			lsOutput:      "  \n  \n",
			dockerOutput:  "",
			expectedCount: 0,
		},
		{
			name:          "lost+found filtered out",
			lsOutput:      "lost+found\nmyproject\n",
			dockerOutput:  "",
			expectedCount: 1,
			check: func(t *testing.T, projects []projectInfo) {
				if projects[0].Name != "myproject" {
					t.Errorf("name = %q, want myproject", projects[0].Name)
				}
			},
		},
		{
			name:          "only lost+found",
			lsOutput:      "lost+found\n",
			dockerOutput:  "",
			expectedCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projects := parseProjectsAndContainers(tt.lsOutput, tt.dockerOutput)
			if len(projects) != tt.expectedCount {
				t.Fatalf("got %d projects, want %d", len(projects), tt.expectedCount)
			}
			if tt.check != nil {
				tt.check(t, projects)
			}
		})
	}
}

func TestProjectAddRequiresGitURL(t *testing.T) {
	// Verify the command requires exactly 1 argument.
	deps := &projectAddDeps{
		describe: &mockDescribeForProject{output: &ec2.DescribeInstancesOutput{}},
		sendKey:  &mockSendKeyForProject{},
		owner:    "alice",
		remote: func(ctx context.Context, sendKey mintaws.SendSSHPublicKeyAPI, instanceID, az, host string, port int, user string, command []string) ([]byte, error) {
			return nil, nil
		},
	}

	projectCmd := newProjectCommandWithDeps(deps)
	root := newTestRootForProject()
	root.AddCommand(projectCmd)
	root.SetOut(new(bytes.Buffer))
	root.SetErr(new(bytes.Buffer))
	root.SetArgs([]string{"project", "add"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for missing argument")
	}
}

// --- Project add TOFU tests ---

func TestProjectAddTOFUKeyscanTriggered(t *testing.T) {
	// Verify that TOFU keyscan is triggered on project add when
	// hostKeyStore and hostKeyScanner are provided.
	scanCalls := 0
	scanner := func(host string, port int) (string, string, error) {
		scanCalls++
		return "SHA256:projectfp", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest", nil
	}

	remote := &projectMockRemote{
		// remote: test -d (dir doesn't exist), docker ps, tmux new-session
		outputs: [][]byte{nil, []byte("ctr1\n"), nil},
		errors:  []error{fmt.Errorf("exit status 1"), nil, nil},
	}
	streaming := &projectMockStreamingRemote{
		outputs: [][]byte{nil, nil},
		errors:  []error{nil, nil},
	}
	deps := &projectAddDeps{
		describe:        &mockDescribeForProject{output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a")},
		sendKey:         &mockSendKeyForProject{output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true}},
		owner:           "alice",
		remote:          remote.run,
		streamingRunner: streaming.run,
		hostKeyStore:    sshconfig.NewHostKeyStore(t.TempDir()),
		hostKeyScanner:  scanner,
	}

	projectCmd := newProjectCommandWithDeps(deps)
	root := newTestRootForProject()
	root.AddCommand(projectCmd)

	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"project", "add", "https://github.com/org/repo.git"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 3 remote calls (test -d, docker ps, tmux) + 2 streaming (clone, devcontainer up), keyscan once.
	if len(remote.calls) != 3 {
		t.Fatalf("expected 3 remote calls, got %d", len(remote.calls))
	}
	if len(streaming.calls) != 2 {
		t.Fatalf("expected 2 streaming calls, got %d", len(streaming.calls))
	}
	if scanCalls != 1 {
		t.Errorf("keyscan should be called exactly once (cached), got %d calls", scanCalls)
	}
}

func TestProjectAddTOFUHostKeyMismatch(t *testing.T) {
	store := sshconfig.NewHostKeyStore(t.TempDir())
	// Pre-record a different key.
	if err := store.RecordKey("default", "SHA256:oldfp"); err != nil {
		t.Fatalf("RecordKey: %v", err)
	}

	scanner := func(host string, port int) (string, string, error) {
		return "SHA256:newfp", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINew", nil
	}

	remote := &projectMockRemote{}
	deps := &projectAddDeps{
		describe:       &mockDescribeForProject{output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a")},
		sendKey:        &mockSendKeyForProject{output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true}},
		owner:          "alice",
		remote:         remote.run,
		hostKeyStore:   store,
		hostKeyScanner: scanner,
	}

	projectCmd := newProjectCommandWithDeps(deps)
	root := newTestRootForProject()
	root.AddCommand(projectCmd)

	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"project", "add", "https://github.com/org/repo.git"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for host key mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "HOST KEY CHANGED") {
		t.Errorf("error should mention HOST KEY CHANGED, got: %s", err.Error())
	}
	if len(remote.calls) != 0 {
		t.Errorf("remote runner should not be called on host key mismatch, got %d calls", len(remote.calls))
	}
}

// --- Project rebuild tests ---

func TestProjectRebuildTOFUKeyscanTriggered(t *testing.T) {
	scanCalls := 0
	scanner := func(host string, port int) (string, string, error) {
		scanCalls++
		return "SHA256:rebuildfp", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest", nil
	}

	remote := &projectMockRemote{
		// remote: test -d, stop, rm, docker ps, tmux kill, tmux new
		outputs: [][]byte{nil, nil, nil, []byte("newctr\n"), nil, nil},
		errors:  []error{nil, nil, nil, nil, nil, nil},
	}
	streaming := &projectMockStreamingRemote{
		outputs: [][]byte{nil},
		errors:  []error{nil},
	}
	deps := &projectRebuildDeps{
		describe:        &mockDescribeForProject{output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a")},
		sendKey:         &mockSendKeyForProject{output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true}},
		owner:           "alice",
		remote:          remote.run,
		streamingRunner: streaming.run,
		stdin:           strings.NewReader(""),
		hostKeyStore:    sshconfig.NewHostKeyStore(t.TempDir()),
		hostKeyScanner:  scanner,
	}

	projectCmd := newProjectCommandWithRebuildDeps(deps)
	root := newTestRootForProject()
	root.AddCommand(projectCmd)

	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"--yes", "project", "rebuild", "myproject"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 6 remote calls (test -d, stop, rm, docker ps, tmux kill, tmux new) + 1 streaming (devcontainer up), keyscan once.
	if len(remote.calls) != 6 {
		t.Fatalf("expected 6 remote calls, got %d", len(remote.calls))
	}
	if len(streaming.calls) != 1 {
		t.Fatalf("expected 1 streaming call, got %d", len(streaming.calls))
	}
	if scanCalls != 1 {
		t.Errorf("keyscan should be called exactly once (cached), got %d calls", scanCalls)
	}
}

func TestProjectRebuildTOFUHostKeyMismatch(t *testing.T) {
	store := sshconfig.NewHostKeyStore(t.TempDir())
	if err := store.RecordKey("default", "SHA256:oldfp"); err != nil {
		t.Fatalf("RecordKey: %v", err)
	}

	scanner := func(host string, port int) (string, string, error) {
		return "SHA256:newfp", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINew", nil
	}

	remote := &projectMockRemote{}
	deps := &projectRebuildDeps{
		describe:       &mockDescribeForProject{output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a")},
		sendKey:        &mockSendKeyForProject{output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true}},
		owner:          "alice",
		remote:         remote.run,
		stdin:          strings.NewReader(""),
		hostKeyStore:   store,
		hostKeyScanner: scanner,
	}

	projectCmd := newProjectCommandWithRebuildDeps(deps)
	root := newTestRootForProject()
	root.AddCommand(projectCmd)

	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"--yes", "project", "rebuild", "myproject"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for host key mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "HOST KEY CHANGED") {
		t.Errorf("error should mention HOST KEY CHANGED, got: %s", err.Error())
	}
	if len(remote.calls) != 0 {
		t.Errorf("remote runner should not be called on host key mismatch, got %d calls", len(remote.calls))
	}
}

func TestProjectRebuildCommand(t *testing.T) {
	tests := []struct {
		name               string
		describe           *mockDescribeForProject
		sendKey            *mockSendKeyForProject
		remote             *projectMockRemote
		streaming          *projectMockStreamingRemote
		owner              string
		args               []string
		stdinInput         string
		wantErr            bool
		wantErrContain     string
		wantCalls          int
		wantStreamingCalls int
		checkCalls         func(t *testing.T, calls []projectRemoteCall)
		checkOutput        func(t *testing.T, output string)
	}{
		{
			name: "successful rebuild with yes flag",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForProject{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			// remote: test -d, stop, rm, docker ps, tmux kill, tmux new
			remote: &projectMockRemote{
				outputs: [][]byte{nil, nil, nil, []byte("newctr789\n"), nil, nil},
				errors:  []error{nil, nil, nil, nil, nil, nil},
			},
			// streaming: devcontainer up
			streaming:          &projectMockStreamingRemote{outputs: [][]byte{nil}, errors: []error{nil}},
			owner:              "alice",
			args:               []string{"--yes", "project", "rebuild", "myproject"},
			wantCalls:          6,
			wantStreamingCalls: 1,
			checkCalls: func(t *testing.T, calls []projectRemoteCall) {
				t.Helper()
				// Call 0: test -d /mint/projects/myproject
				testCmd := strings.Join(calls[0].command, " ")
				if !strings.Contains(testCmd, "test -d /mint/projects/myproject") {
					t.Errorf("first call should verify project exists, got: %s", testCmd)
				}
				// Call 1: docker stop
				stopCmd := strings.Join(calls[1].command, " ")
				if !strings.Contains(stopCmd, "docker stop") {
					t.Errorf("second call should stop container, got: %s", stopCmd)
				}
				if !strings.Contains(stopCmd, "devcontainer.local_folder=/mint/projects/myproject") {
					t.Errorf("stop should filter by project path, got: %s", stopCmd)
				}
				// Call 2: docker rm
				rmCmd := strings.Join(calls[2].command, " ")
				if !strings.Contains(rmCmd, "docker rm") {
					t.Errorf("third call should remove container, got: %s", rmCmd)
				}
				// Call 3: docker ps to discover new container
				dockerCmd := strings.Join(calls[3].command, " ")
				if !strings.Contains(dockerCmd, "docker ps -q") {
					t.Errorf("fourth call should be docker ps, got: %s", dockerCmd)
				}
				if !strings.Contains(dockerCmd, "devcontainer.local_folder=/mint/projects/myproject") {
					t.Errorf("docker ps should filter by project path, got: %s", dockerCmd)
				}
				// Call 4: tmux kill-session
				killCmd := strings.Join(calls[4].command, " ")
				if !strings.Contains(killCmd, "tmux kill-session") {
					t.Errorf("fifth call should kill tmux session, got: %s", killCmd)
				}
				if !strings.Contains(killCmd, "-t myproject") {
					t.Errorf("kill-session should target project name, got: %s", killCmd)
				}
				// Call 5: tmux new-session with docker exec
				tmuxCmd := strings.Join(calls[5].command, " ")
				if !strings.Contains(tmuxCmd, "tmux new-session") {
					t.Errorf("sixth call should be tmux new-session, got: %s", tmuxCmd)
				}
				if !strings.Contains(tmuxCmd, "-s myproject") {
					t.Errorf("tmux session should use project name, got: %s", tmuxCmd)
				}
				if !strings.Contains(tmuxCmd, "docker exec -it newctr789 /bin/bash") {
					t.Errorf("tmux session should docker exec into container, got: %s", tmuxCmd)
				}
			},
			checkOutput: func(t *testing.T, output string) {
				t.Helper()
				if !strings.Contains(output, "Verifying") {
					t.Errorf("output should show verify step, got: %s", output)
				}
				if !strings.Contains(output, "Stopping") {
					t.Errorf("output should show stop step, got: %s", output)
				}
				if !strings.Contains(output, "Removing") {
					t.Errorf("output should show remove step, got: %s", output)
				}
				if !strings.Contains(output, "Rebuilding") {
					t.Errorf("output should show rebuild step, got: %s", output)
				}
				if !strings.Contains(output, "Rebuilt devcontainer for") {
					t.Errorf("output should show success message, got: %s", output)
				}
			},
		},
		{
			name: "successful rebuild with confirmation prompt",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForProject{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remote: &projectMockRemote{
				outputs: [][]byte{nil, nil, nil, []byte("ctr123\n"), nil, nil},
				errors:  []error{nil, nil, nil, nil, nil, nil},
			},
			streaming:          &projectMockStreamingRemote{outputs: [][]byte{nil}, errors: []error{nil}},
			owner:              "alice",
			args:               []string{"project", "rebuild", "myproject"},
			stdinInput:         "myproject\n",
			wantCalls:          6,
			wantStreamingCalls: 1,
			checkOutput: func(t *testing.T, output string) {
				t.Helper()
				if !strings.Contains(output, "Type the project name to confirm") {
					t.Errorf("output should show confirmation prompt, got: %s", output)
				}
				if !strings.Contains(output, "Rebuilt devcontainer for") {
					t.Errorf("output should show success, got: %s", output)
				}
			},
		},
		{
			name: "confirmation mismatch aborts rebuild",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForProject{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remote: &projectMockRemote{
				outputs: [][]byte{nil},
				errors:  []error{nil},
			},
			streaming:      &projectMockStreamingRemote{},
			owner:          "alice",
			args:           []string{"project", "rebuild", "myproject"},
			stdinInput:     "wrong-name\n",
			wantErr:        true,
			wantErrContain: "rebuild aborted",
			wantCalls:      1,
		},
		{
			name: "empty confirmation aborts rebuild",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForProject{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remote: &projectMockRemote{
				outputs: [][]byte{nil},
				errors:  []error{nil},
			},
			streaming:      &projectMockStreamingRemote{},
			owner:          "alice",
			args:           []string{"project", "rebuild", "myproject"},
			stdinInput:     "",
			wantErr:        true,
			wantErrContain: "no confirmation input received",
			wantCalls:      1,
		},
		{
			name: "project not found returns error",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForProject{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remote: &projectMockRemote{
				errors: []error{fmt.Errorf("exit status 1")},
			},
			streaming:      &projectMockStreamingRemote{},
			owner:          "alice",
			args:           []string{"--yes", "project", "rebuild", "nonexistent"},
			wantErr:        true,
			wantErrContain: "not found",
			wantCalls:      1,
		},
		{
			name: "rebuild with shell metacharacters rejected",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForProject{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remote:         &projectMockRemote{},
			streaming:      &projectMockStreamingRemote{},
			owner:          "alice",
			args:           []string{"--yes", "project", "rebuild", "foo$(whoami)"},
			wantErr:        true,
			wantErrContain: "invalid project name",
			wantCalls:      0,
		},
		{
			name: "rebuild with pipe injection rejected",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForProject{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remote:         &projectMockRemote{},
			streaming:      &projectMockStreamingRemote{},
			owner:          "alice",
			args:           []string{"--yes", "project", "rebuild", "foo|cat /etc/passwd"},
			wantErr:        true,
			wantErrContain: "invalid project name",
			wantCalls:      0,
		},
		{
			name: "VM not found returns error",
			describe: &mockDescribeForProject{
				output: &ec2.DescribeInstancesOutput{},
			},
			sendKey:        &mockSendKeyForProject{},
			remote:         &projectMockRemote{},
			streaming:      &projectMockStreamingRemote{},
			owner:          "alice",
			args:           []string{"--yes", "project", "rebuild", "myproject"},
			wantErr:        true,
			wantErrContain: "mint up",
		},
		{
			name: "stopped VM returns error",
			describe: &mockDescribeForProject{
				output: makeStoppedInstanceForProject("i-abc123", "default", "alice"),
			},
			sendKey:        &mockSendKeyForProject{},
			remote:         &projectMockRemote{},
			streaming:      &projectMockStreamingRemote{},
			owner:          "alice",
			args:           []string{"--yes", "project", "rebuild", "myproject"},
			wantErr:        true,
			wantErrContain: "not running",
		},
		{
			name: "devcontainer rebuild failure returns error",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForProject{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remote: &projectMockRemote{
				outputs: [][]byte{nil, nil, nil},
				errors:  []error{nil, nil, nil},
			},
			streaming: &projectMockStreamingRemote{
				errors: []error{fmt.Errorf("Dockerfile syntax error")},
			},
			owner:              "alice",
			args:               []string{"--yes", "project", "rebuild", "myproject"},
			wantErr:            true,
			wantErrContain:     "rebuilding devcontainer",
			wantCalls:          3,
			wantStreamingCalls: 1,
		},
		{
			name: "missing project name argument",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey:   &mockSendKeyForProject{},
			remote:    &projectMockRemote{},
			streaming: &projectMockStreamingRemote{},
			owner:     "alice",
			args:      []string{"project", "rebuild"},
			wantErr:   true,
		},
		{
			name: "remote commands use correct SSH params",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForProject{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remote: &projectMockRemote{
				outputs: [][]byte{nil, nil, nil, []byte("ctr1\n"), nil, nil},
				errors:  []error{nil, nil, nil, nil, nil, nil},
			},
			streaming:          &projectMockStreamingRemote{outputs: [][]byte{nil}, errors: []error{nil}},
			owner:              "alice",
			args:               []string{"--yes", "project", "rebuild", "myproject"},
			wantCalls:          6,
			wantStreamingCalls: 1,
			checkCalls: func(t *testing.T, calls []projectRemoteCall) {
				t.Helper()
				for i, call := range calls {
					if call.instanceID != "i-abc123" {
						t.Errorf("call %d: instanceID = %q, want i-abc123", i, call.instanceID)
					}
					if call.host != "1.2.3.4" {
						t.Errorf("call %d: host = %q, want 1.2.3.4", i, call.host)
					}
					if call.port != 41122 {
						t.Errorf("call %d: port = %d, want 41122", i, call.port)
					}
					if call.user != "ubuntu" {
						t.Errorf("call %d: user = %q, want ubuntu", i, call.user)
					}
					if call.az != "us-east-1a" {
						t.Errorf("call %d: az = %q, want us-east-1a", i, call.az)
					}
				}
			},
		},
		{
			name: "stop container failure propagates",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForProject{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remote: &projectMockRemote{
				outputs: [][]byte{nil},
				errors:  []error{nil, fmt.Errorf("connection reset")},
			},
			streaming:      &projectMockStreamingRemote{},
			owner:          "alice",
			args:           []string{"--yes", "project", "rebuild", "myproject"},
			wantErr:        true,
			wantErrContain: "stopping container",
			wantCalls:      2,
		},
		{
			name: "remove container failure propagates",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForProject{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remote: &projectMockRemote{
				outputs: [][]byte{nil, nil},
				errors:  []error{nil, nil, fmt.Errorf("permission denied")},
			},
			streaming: &projectMockStreamingRemote{},
			owner:          "alice",
			args:           []string{"--yes", "project", "rebuild", "myproject"},
			wantErr:        true,
			wantErrContain: "removing container",
			wantCalls:      3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := new(bytes.Buffer)

			deps := &projectRebuildDeps{
				describe:        tt.describe,
				sendKey:         tt.sendKey,
				owner:           tt.owner,
				remote:          tt.remote.run,
				streamingRunner: tt.streaming.run,
				stdin:           strings.NewReader(tt.stdinInput),
			}

			projectCmd := newProjectCommandWithRebuildDeps(deps)
			root := newTestRootForProject()
			root.AddCommand(projectCmd)
			root.SetOut(buf)
			root.SetErr(buf)
			root.SetArgs(tt.args)

			err := root.Execute()

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.wantErrContain != "" && !strings.Contains(err.Error(), tt.wantErrContain) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.wantErrContain)
				}
				if tt.wantCalls > 0 && len(tt.remote.calls) != tt.wantCalls {
					t.Errorf("expected %d remote calls, got %d", tt.wantCalls, len(tt.remote.calls))
				}
				if tt.wantStreamingCalls > 0 && len(tt.streaming.calls) != tt.wantStreamingCalls {
					t.Errorf("expected %d streaming calls, got %d", tt.wantStreamingCalls, len(tt.streaming.calls))
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantCalls > 0 && len(tt.remote.calls) != tt.wantCalls {
				t.Errorf("expected %d remote calls, got %d", tt.wantCalls, len(tt.remote.calls))
			}

			if tt.wantStreamingCalls > 0 && len(tt.streaming.calls) != tt.wantStreamingCalls {
				t.Errorf("expected %d streaming calls, got %d", tt.wantStreamingCalls, len(tt.streaming.calls))
			}

			if tt.checkCalls != nil {
				tt.checkCalls(t, tt.remote.calls)
			}

			if tt.checkOutput != nil {
				tt.checkOutput(t, buf.String())
			}
		})
	}
}

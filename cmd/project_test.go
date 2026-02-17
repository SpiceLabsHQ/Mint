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
		name           string
		describe       *mockDescribeForProject
		sendKey        *mockSendKeyForProject
		remote         *projectMockRemote
		owner          string
		args           []string
		wantErr        bool
		wantErrContain string
		wantCalls      int
		checkCalls     func(t *testing.T, calls []projectRemoteCall)
		checkOutput    func(t *testing.T, output string)
	}{
		{
			name: "successful project add with https url",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForProject{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remote:    &projectMockRemote{outputs: [][]byte{nil, nil, nil}, errors: []error{nil, nil, nil}},
			owner:     "alice",
			args:      []string{"project", "add", "https://github.com/org/repo.git"},
			wantCalls: 3,
			checkCalls: func(t *testing.T, calls []projectRemoteCall) {
				t.Helper()
				// Step 1: git clone
				if len(calls) < 1 {
					t.Fatal("expected at least 1 call")
				}
				cloneCmd := strings.Join(calls[0].command, " ")
				if !strings.Contains(cloneCmd, "git clone") {
					t.Errorf("first call should be git clone, got: %s", cloneCmd)
				}
				if !strings.Contains(cloneCmd, "https://github.com/org/repo.git") {
					t.Errorf("clone should include URL, got: %s", cloneCmd)
				}
				if !strings.Contains(cloneCmd, "/mint/projects/repo") {
					t.Errorf("clone should target /mint/projects/repo, got: %s", cloneCmd)
				}

				// Step 2: devcontainer up
				if len(calls) < 2 {
					t.Fatal("expected at least 2 calls")
				}
				buildCmd := strings.Join(calls[1].command, " ")
				if !strings.Contains(buildCmd, "devcontainer up") {
					t.Errorf("second call should be devcontainer up, got: %s", buildCmd)
				}
				if !strings.Contains(buildCmd, "--workspace-folder /mint/projects/repo") {
					t.Errorf("devcontainer up should target workspace folder, got: %s", buildCmd)
				}

				// Step 3: tmux session
				if len(calls) < 3 {
					t.Fatal("expected 3 calls")
				}
				tmuxCmd := strings.Join(calls[2].command, " ")
				if !strings.Contains(tmuxCmd, "tmux new-session") {
					t.Errorf("third call should be tmux new-session, got: %s", tmuxCmd)
				}
				if !strings.Contains(tmuxCmd, "-s repo") {
					t.Errorf("tmux session should use project name, got: %s", tmuxCmd)
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
			remote:    &projectMockRemote{outputs: [][]byte{nil, nil, nil}, errors: []error{nil, nil, nil}},
			owner:     "alice",
			args:      []string{"project", "add", "git@github.com:org/my-app.git"},
			wantCalls: 3,
			checkCalls: func(t *testing.T, calls []projectRemoteCall) {
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
			remote:    &projectMockRemote{outputs: [][]byte{nil, nil, nil}, errors: []error{nil, nil, nil}},
			owner:     "alice",
			args:      []string{"project", "add", "--name", "custom-name", "https://github.com/org/repo.git"},
			wantCalls: 3,
			checkCalls: func(t *testing.T, calls []projectRemoteCall) {
				t.Helper()
				cloneCmd := strings.Join(calls[0].command, " ")
				if !strings.Contains(cloneCmd, "/mint/projects/custom-name") {
					t.Errorf("clone should use custom name, got: %s", cloneCmd)
				}
				tmuxCmd := strings.Join(calls[2].command, " ")
				if !strings.Contains(tmuxCmd, "-s custom-name") {
					t.Errorf("tmux session should use custom name, got: %s", tmuxCmd)
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
			remote:    &projectMockRemote{outputs: [][]byte{nil, nil, nil}, errors: []error{nil, nil, nil}},
			owner:     "alice",
			args:      []string{"project", "add", "--branch", "develop", "https://github.com/org/repo.git"},
			wantCalls: 3,
			checkCalls: func(t *testing.T, calls []projectRemoteCall) {
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
			owner:          "alice",
			args:           []string{"project", "add", "https://github.com/org/repo.git"},
			wantErr:        true,
			wantErrContain: "not running",
		},
		{
			name: "clone failure with exists error",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForProject{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remote: &projectMockRemote{
				errors: []error{fmt.Errorf("fatal: destination path '/mint/projects/repo' already exists")},
			},
			owner:          "alice",
			args:           []string{"project", "add", "https://github.com/org/repo.git"},
			wantErr:        true,
			wantErrContain: "already exists",
			wantCalls:      1,
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
				errors:  []error{nil, fmt.Errorf("devcontainer build failed: Dockerfile syntax error")},
			},
			owner:          "alice",
			args:           []string{"project", "add", "https://github.com/org/repo.git"},
			wantErr:        true,
			wantErrContain: "devcontainer",
			wantCalls:      2,
		},
		{
			name: "missing git url argument",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			sendKey: &mockSendKeyForProject{},
			remote:  &projectMockRemote{},
			owner:   "alice",
			args:    []string{"project", "add"},
			wantErr: true,
		},
		{
			name: "non-default vm name",
			describe: &mockDescribeForProject{
				output: makeRunningInstanceForProject("i-dev456", "dev", "alice", "10.0.0.1", "us-west-2a"),
			},
			sendKey: &mockSendKeyForProject{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true},
			},
			remote:    &projectMockRemote{outputs: [][]byte{nil, nil, nil}, errors: []error{nil, nil, nil}},
			owner:     "alice",
			args:      []string{"--vm", "dev", "project", "add", "https://github.com/org/repo.git"},
			wantCalls: 3,
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
			name: "describe API error propagates",
			describe: &mockDescribeForProject{
				err: fmt.Errorf("throttled"),
			},
			sendKey:        &mockSendKeyForProject{},
			remote:         &projectMockRemote{},
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
			remote:    &projectMockRemote{outputs: [][]byte{nil, nil, nil}, errors: []error{nil, nil, nil}},
			owner:     "alice",
			args:      []string{"project", "add", "https://github.com/org/repo.git"},
			wantCalls: 3,
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := new(bytes.Buffer)

			deps := &projectAddDeps{
				describe: tt.describe,
				sendKey:  tt.sendKey,
				owner:    tt.owner,
				remote:   tt.remote.run,
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
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantCalls > 0 && len(tt.remote.calls) != tt.wantCalls {
				t.Errorf("expected %d remote calls, got %d", tt.wantCalls, len(tt.remote.calls))
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

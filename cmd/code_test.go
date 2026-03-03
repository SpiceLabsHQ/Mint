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
	mintaws "github.com/SpiceLabsHQ/Mint/internal/aws"
	"github.com/SpiceLabsHQ/Mint/internal/cli"
	"github.com/spf13/cobra"
)

// mockRemoteRunnerForCode returns a RemoteCommandRunner that captures invocations
// and returns the configured output. It allows test assertions on the command
// that was sent to the remote host.
func mockRemoteRunnerForCode(stdout string, err error) (RemoteCommandRunner, *[][]string) {
	var calls [][]string
	runner := func(
		ctx context.Context,
		sendKey mintaws.SendSSHPublicKeyAPI,
		instanceID, az, host string,
		port int,
		user string,
		command []string,
	) ([]byte, error) {
		calls = append(calls, command)
		return []byte(stdout), err
	}
	return runner, &calls
}

func TestCodeCommand(t *testing.T) {
	tests := []struct {
		name              string
		describe          *mockDescribeForSSH
		owner             string
		vmName            string
		projectArg        string // positional arg: mint code <project>
		pathFlag          string
		sshConfigApproved bool
		remoteOutput      string // mock output from ls -1 /mint/projects/
		remoteErr         error  // mock error from remote command
		wantErr           bool
		wantErrContain    string
		wantExec          bool
		wantOutput        []string // strings expected in command stdout
		checkCmd          func(t *testing.T, captured capturedCommand)
	}{
		{
			name: "custom path flag bypasses project discovery",
			describe: &mockDescribeForSSH{
				output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			owner:             "alice",
			sshConfigApproved: true,
			pathFlag:          "/home/ubuntu/myproject",
			wantExec:          true,
			checkCmd: func(t *testing.T, captured capturedCommand) {
				t.Helper()
				argsStr := strings.Join(captured.args, " ")
				if !strings.Contains(argsStr, "/home/ubuntu/myproject") {
					t.Errorf("custom path not used, args: %v", captured.args)
				}
			},
		},
		{
			name: "non-default vm name in remote",
			describe: &mockDescribeForSSH{
				output: makeRunningInstanceWithAZ("i-dev456", "dev", "alice", "10.0.0.1", "us-west-2a"),
			},
			owner:             "alice",
			sshConfigApproved: true,
			vmName:            "dev",
			remoteOutput:      "myproject\n",
			wantExec:          true,
			checkCmd: func(t *testing.T, captured capturedCommand) {
				t.Helper()
				argsStr := strings.Join(captured.args, " ")
				if !strings.Contains(argsStr, "ssh-remote+mint-dev") {
					t.Errorf("wrong remote name, args: %v", captured.args)
				}
			},
		},
		{
			name: "vm not found returns actionable error",
			describe: &mockDescribeForSSH{
				output: &ec2.DescribeInstancesOutput{},
			},
			owner:             "alice",
			sshConfigApproved: true,
			wantErr:           true,
			wantErrContain:    "mint up",
			wantExec:          false,
		},
		{
			name: "stopped vm returns actionable error",
			describe: &mockDescribeForSSH{
				output: makeStoppedInstanceForSSH("i-abc123", "default", "alice"),
			},
			owner:             "alice",
			sshConfigApproved: true,
			wantErr:           true,
			wantErrContain:    "not running",
			wantExec:          false,
		},
		{
			name: "describe API error propagates",
			describe: &mockDescribeForSSH{
				err: fmt.Errorf("connection timeout"),
			},
			owner:             "alice",
			sshConfigApproved: true,
			wantErr:           true,
			wantErrContain:    "connection timeout",
			wantExec:          false,
		},
		{
			name: "skips ssh config when not approved",
			describe: &mockDescribeForSSH{
				output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			owner:             "alice",
			sshConfigApproved: false,
			wantErr:           true,
			wantErrContain:    "ssh_config_approved",
			wantExec:          false,
		},
		// --- Tests for positional project argument (#202) ---
		{
			name: "project arg resolves to /mint/projects/<name>",
			describe: &mockDescribeForSSH{
				output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			owner:             "alice",
			projectArg:        "myproject",
			sshConfigApproved: true,
			wantExec:          true,
			checkCmd: func(t *testing.T, captured capturedCommand) {
				t.Helper()
				argsStr := strings.Join(captured.args, " ")
				if !strings.Contains(argsStr, "/mint/projects/myproject") {
					t.Errorf("expected path /mint/projects/myproject, args: %v", captured.args)
				}
			},
		},
		{
			name: "project arg with dots and hyphens resolves correctly",
			describe: &mockDescribeForSSH{
				output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			owner:             "alice",
			projectArg:        "my-project.v2",
			sshConfigApproved: true,
			wantExec:          true,
			checkCmd: func(t *testing.T, captured capturedCommand) {
				t.Helper()
				argsStr := strings.Join(captured.args, " ")
				if !strings.Contains(argsStr, "/mint/projects/my-project.v2") {
					t.Errorf("expected path /mint/projects/my-project.v2, args: %v", captured.args)
				}
			},
		},
		{
			name: "project arg and --path flag conflict errors",
			describe: &mockDescribeForSSH{
				output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			owner:             "alice",
			projectArg:        "myproject",
			pathFlag:          "/custom/path",
			sshConfigApproved: true,
			wantErr:           true,
			wantErrContain:    "cannot use both",
			wantExec:          false,
		},
		{
			name: "invalid project name with shell metacharacters rejected",
			describe: &mockDescribeForSSH{
				output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			owner:             "alice",
			projectArg:        "../evil",
			sshConfigApproved: true,
			wantErr:           true,
			wantErrContain:    "invalid project name",
			wantExec:          false,
		},
		{
			name: "invalid project name with spaces rejected",
			describe: &mockDescribeForSSH{
				output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			owner:             "alice",
			projectArg:        "rm -rf /",
			sshConfigApproved: true,
			wantErr:           true,
			// cobra.MaximumNArgs(1) catches this as too many args
			wantExec: false,
		},
		{
			name: "invalid project name with semicolon rejected",
			describe: &mockDescribeForSSH{
				output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			owner:             "alice",
			projectArg:        "foo;bar",
			sshConfigApproved: true,
			wantErr:           true,
			wantErrContain:    "invalid project name",
			wantExec:          false,
		},
		// --- Tests for no-arg project discovery (#203) ---
		{
			name: "no args with zero projects shows hint",
			describe: &mockDescribeForSSH{
				output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			owner:             "alice",
			sshConfigApproved: true,
			remoteOutput:      "",
			wantErr:           false,
			wantExec:          false,
			wantOutput:        []string{"No projects yet", "mint project add"},
		},
		{
			name: "no args with only lost+found shows hint",
			describe: &mockDescribeForSSH{
				output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			owner:             "alice",
			sshConfigApproved: true,
			remoteOutput:      "lost+found\n",
			wantErr:           false,
			wantExec:          false,
			wantOutput:        []string{"No projects yet", "mint project add"},
		},
		{
			name: "no args with one project auto-opens VS Code",
			describe: &mockDescribeForSSH{
				output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			owner:             "alice",
			sshConfigApproved: true,
			remoteOutput:      "myproject\n",
			wantExec:          true,
			checkCmd: func(t *testing.T, captured capturedCommand) {
				t.Helper()
				argsStr := strings.Join(captured.args, " ")
				if !strings.Contains(argsStr, "/mint/projects/myproject") {
					t.Errorf("expected auto-open at /mint/projects/myproject, args: %v", captured.args)
				}
				if !strings.Contains(argsStr, "--remote ssh-remote+mint-default") {
					t.Errorf("missing --remote flag, args: %v", captured.args)
				}
			},
		},
		{
			name: "no args with multiple projects lists them",
			describe: &mockDescribeForSSH{
				output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			owner:             "alice",
			sshConfigApproved: true,
			remoteOutput:      "alpha\nbeta\ngamma\n",
			wantErr:           false,
			wantExec:          false,
			wantOutput:        []string{"mint code alpha", "mint code beta", "mint code gamma"},
		},
		{
			name: "no args with multiple projects filters lost+found",
			describe: &mockDescribeForSSH{
				output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			owner:             "alice",
			sshConfigApproved: true,
			remoteOutput:      "alpha\nlost+found\nbeta\n",
			wantErr:           false,
			wantExec:          false,
			wantOutput:        []string{"mint code alpha", "mint code beta"},
		},
		{
			name: "no args with remote command error propagates",
			describe: &mockDescribeForSSH{
				output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			owner:             "alice",
			sshConfigApproved: true,
			remoteErr:         fmt.Errorf("ssh: Connection refused"),
			wantErr:           true,
			wantErrContain:    "listing projects",
			wantExec:          false,
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

			// Use temp dir for SSH config to avoid polluting real config
			sshConfigDir := t.TempDir()
			configDir := t.TempDir()
			t.Setenv("MINT_CONFIG_DIR", configDir)

			remoteRunner, _ := mockRemoteRunnerForCode(tt.remoteOutput, tt.remoteErr)

			deps := &codeDeps{
				describe:          tt.describe,
				owner:             tt.owner,
				runner:            runner,
				sendKey:           &mockSendSSHPublicKey{},
				runRemoteCommand:  remoteRunner,
				sshConfigPath:     sshConfigDir + "/config",
				sshConfigApproved: tt.sshConfigApproved,
			}

			cmd := newCodeCommandWithDeps(deps)
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
			root.AddCommand(cmd)
			root.SetOut(buf)
			root.SetErr(buf)

			args := []string{"code"}
			if tt.vmName != "" && tt.vmName != "default" {
				args = append([]string{"--vm", tt.vmName}, args...)
			}
			if tt.projectArg != "" {
				args = append(args, tt.projectArg)
			}
			if tt.pathFlag != "" {
				args = append(args, "--path", tt.pathFlag)
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

			// Check expected output strings.
			output := buf.String()
			for _, want := range tt.wantOutput {
				if !strings.Contains(output, want) {
					t.Errorf("output missing %q, got:\n%s", want, output)
				}
			}
		})
	}
}

func TestCodeCommandTooManyArgs(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", configDir)

	deps := &codeDeps{
		describe: &mockDescribeForSSH{
			output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
		},
		owner:             "alice",
		sendKey:           &mockSendSSHPublicKey{},
		sshConfigApproved: true,
	}

	cmd := newCodeCommandWithDeps(deps)
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
	root.PersistentFlags().Bool("verbose", false, "")
	root.PersistentFlags().Bool("debug", false, "")
	root.PersistentFlags().Bool("json", false, "")
	root.PersistentFlags().Bool("yes", false, "")
	root.PersistentFlags().String("vm", "default", "")
	root.AddCommand(cmd)
	root.SetArgs([]string{"code", "arg1", "arg2"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for two positional args, got nil")
	}
}

func TestCodeCommandSkipsSSHConfigWhenNotApproved(t *testing.T) {
	sshConfigDir := t.TempDir()
	configDir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", configDir)

	var captured *capturedCommand
	runner := func(name string, args ...string) error {
		captured = &capturedCommand{name: name, args: args}
		return nil
	}

	remoteRunner, _ := mockRemoteRunnerForCode("", nil)

	deps := &codeDeps{
		describe: &mockDescribeForSSH{
			output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
		},
		owner:             "alice",
		runner:            runner,
		sendKey:           &mockSendSSHPublicKey{},
		runRemoteCommand:  remoteRunner,
		sshConfigPath:     sshConfigDir + "/config",
		sshConfigApproved: false,
	}

	cmd := newCodeCommandWithDeps(deps)
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
	root.PersistentFlags().Bool("verbose", false, "")
	root.PersistentFlags().Bool("debug", false, "")
	root.PersistentFlags().Bool("json", false, "")
	root.PersistentFlags().Bool("yes", false, "")
	root.PersistentFlags().String("vm", "default", "")
	root.AddCommand(cmd)
	root.SetArgs([]string{"code"})

	err := root.Execute()

	if err == nil {
		t.Fatal("expected error when ssh_config_approved is false, got nil")
	}
	if !strings.Contains(err.Error(), "ssh_config_approved") {
		t.Errorf("error %q should mention ssh_config_approved", err.Error())
	}
	if captured != nil {
		t.Error("VS Code should not have been launched when SSH config not approved")
	}

	// Verify no SSH config file was written.
	sshConfigFile := sshConfigDir + "/config"
	if _, statErr := os.Stat(sshConfigFile); statErr == nil {
		t.Error("SSH config file should not have been written when not approved")
	}
}

func TestCodeCommandWritesSSHConfigWhenApproved(t *testing.T) {
	sshConfigDir := t.TempDir()
	configDir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", configDir)

	var captured *capturedCommand
	runner := func(name string, args ...string) error {
		captured = &capturedCommand{name: name, args: args}
		return nil
	}

	// Single project so auto-open triggers VS Code launch.
	remoteRunner, _ := mockRemoteRunnerForCode("myproject\n", nil)

	deps := &codeDeps{
		describe: &mockDescribeForSSH{
			output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
		},
		owner:             "alice",
		runner:            runner,
		sendKey:           &mockSendSSHPublicKey{},
		runRemoteCommand:  remoteRunner,
		sshConfigPath:     sshConfigDir + "/config",
		sshConfigApproved: true,
	}

	cmd := newCodeCommandWithDeps(deps)
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
	root.PersistentFlags().Bool("verbose", false, "")
	root.PersistentFlags().Bool("debug", false, "")
	root.PersistentFlags().Bool("json", false, "")
	root.PersistentFlags().Bool("yes", false, "")
	root.PersistentFlags().String("vm", "default", "")
	root.AddCommand(cmd)
	root.SetArgs([]string{"code"})

	err := root.Execute()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured == nil {
		t.Fatal("expected VS Code to be launched")
	}
	if captured.name != "code" {
		t.Errorf("expected command %q, got %q", "code", captured.name)
	}

	// Verify SSH config file was written.
	sshConfigFile := sshConfigDir + "/config"
	data, readErr := os.ReadFile(sshConfigFile)
	if readErr != nil {
		t.Fatalf("SSH config file should have been written: %v", readErr)
	}
	if !strings.Contains(string(data), "mint-default") {
		t.Errorf("SSH config should contain mint-default host entry, got: %s", string(data))
	}
}

// ---------------------------------------------------------------------------
// Multi-VM auto-resolution tests (#204)
// ---------------------------------------------------------------------------

// multiVMDescribeForCode implements DescribeInstancesAPI and returns different
// results for ListVMs (2 tag filters: mint + owner) vs FindVM (3 tag filters:
// mint + owner + vm). This allows tests to simulate multiple VMs existing while
// still resolving a single VM by name.
type multiVMDescribeForCode struct {
	// listOutput is returned when the call has 2 tag filters (ListVMs path).
	listOutput *ec2.DescribeInstancesOutput
	listErr    error
	// findOutputs maps VM name -> output for FindVM calls (3 tag filters).
	findOutputs map[string]*ec2.DescribeInstancesOutput
	findErr     error
}

func (m *multiVMDescribeForCode) DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	// Count tag filters to distinguish ListVMs from FindVM.
	tagFilterCount := 0
	vmFilterValue := ""
	for _, f := range params.Filters {
		if f.Name != nil && strings.HasPrefix(*f.Name, "tag:") {
			tagFilterCount++
			if *f.Name == "tag:mint:vm" && len(f.Values) > 0 {
				vmFilterValue = f.Values[0]
			}
		}
	}

	// 2 tag filters = ListVMs; 3 tag filters = FindVM.
	if tagFilterCount == 2 {
		return m.listOutput, m.listErr
	}
	if tagFilterCount == 3 && vmFilterValue != "" {
		if m.findOutputs != nil {
			if out, ok := m.findOutputs[vmFilterValue]; ok {
				return out, m.findErr
			}
		}
		return &ec2.DescribeInstancesOutput{}, m.findErr
	}
	return &ec2.DescribeInstancesOutput{}, nil
}

// makeMultiVMOutput builds a DescribeInstancesOutput containing multiple VMs
// in a single reservation (as AWS returns them for owner-only filters).
func makeMultiVMOutput(vms ...struct {
	id, name, owner, ip, az, state string
}) *ec2.DescribeInstancesOutput {
	var instances []ec2types.Instance
	for _, v := range vms {
		stateName := ec2types.InstanceStateName(v.state)
		inst := ec2types.Instance{
			InstanceId:      aws.String(v.id),
			InstanceType:    ec2types.InstanceTypeT3Medium,
			PublicIpAddress: aws.String(v.ip),
			State:           &ec2types.InstanceState{Name: stateName},
			Placement:       &ec2types.Placement{AvailabilityZone: aws.String(v.az)},
			Tags: []ec2types.Tag{
				{Key: aws.String("mint:vm"), Value: aws.String(v.name)},
				{Key: aws.String("mint:owner"), Value: aws.String(v.owner)},
			},
		}
		instances = append(instances, inst)
	}
	return &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{Instances: instances}},
	}
}

// perVMRemoteRunner returns a RemoteCommandRunner that returns different output
// depending on which instance it is called against (keyed by instance ID).
// It also records all calls for assertion.
func perVMRemoteRunner(outputs map[string]string, defaultErr error) (RemoteCommandRunner, *[]string) {
	var calledIDs []string
	runner := func(
		ctx context.Context,
		sendKey mintaws.SendSSHPublicKeyAPI,
		instanceID, az, host string,
		port int,
		user string,
		command []string,
	) ([]byte, error) {
		calledIDs = append(calledIDs, instanceID)
		if out, ok := outputs[instanceID]; ok {
			return []byte(out), nil
		}
		if defaultErr != nil {
			return nil, defaultErr
		}
		return nil, fmt.Errorf("no mock output for instance %s", instanceID)
	}
	return runner, &calledIDs
}

// newCodeTestRoot creates a root command with standard persistent flags for
// code command tests. Using --vm flag or not is controlled by the caller.
func newCodeTestRoot() *cobra.Command {
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

func TestCodeMultiVMAutoResolution(t *testing.T) {
	t.Run("single VM skips scanning and uses FindVM fast path", func(t *testing.T) {
		// When only one VM exists, the code should use the existing FindVM path
		// without calling ListVMs or SSH probing.
		buf := new(bytes.Buffer)
		var captured *capturedCommand
		runner := func(name string, args ...string) error {
			captured = &capturedCommand{name: name, args: args}
			return nil
		}

		sshConfigDir := t.TempDir()
		configDir := t.TempDir()
		t.Setenv("MINT_CONFIG_DIR", configDir)

		// ListVMs returns one VM. FindVM returns same VM.
		describe := &multiVMDescribeForCode{
			listOutput: makeMultiVMOutput(struct{ id, name, owner, ip, az, state string }{
				"i-abc123", "default", "alice", "1.2.3.4", "us-east-1a", "running",
			}),
			findOutputs: map[string]*ec2.DescribeInstancesOutput{
				"default": makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
		}

		// The remote runner should NOT be called for project probing in single-VM case.
		remoteRunner, calledIDs := perVMRemoteRunner(nil, nil)

		deps := &codeDeps{
			describe:          describe,
			owner:             "alice",
			runner:            runner,
			sendKey:           &mockSendSSHPublicKey{},
			runRemoteCommand:  remoteRunner,
			sshConfigPath:     sshConfigDir + "/config",
			sshConfigApproved: true,
		}

		cmd := newCodeCommandWithDeps(deps)
		root := newCodeTestRoot()
		root.AddCommand(cmd)
		root.SetOut(buf)
		root.SetErr(buf)
		// No --vm flag: defaults to "default"
		root.SetArgs([]string{"code", "myproject"})

		err := root.Execute()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should launch VS Code directly without probing VMs.
		if captured == nil {
			t.Fatal("expected VS Code launch, got none")
		}
		argsStr := strings.Join(captured.args, " ")
		if !strings.Contains(argsStr, "/mint/projects/myproject") {
			t.Errorf("expected /mint/projects/myproject, args: %v", captured.args)
		}

		// No SSH probes should have been made (single VM fast path).
		if len(*calledIDs) != 0 {
			t.Errorf("expected no SSH probes for single VM, got calls to: %v", *calledIDs)
		}
	})

	t.Run("multi-VM project on exactly one auto-selects correct VM", func(t *testing.T) {
		buf := new(bytes.Buffer)
		var captured *capturedCommand
		runner := func(name string, args ...string) error {
			captured = &capturedCommand{name: name, args: args}
			return nil
		}

		sshConfigDir := t.TempDir()
		configDir := t.TempDir()
		t.Setenv("MINT_CONFIG_DIR", configDir)

		// Two running VMs: "default" and "dev"
		describe := &multiVMDescribeForCode{
			listOutput: makeMultiVMOutput(
				struct{ id, name, owner, ip, az, state string }{
					"i-default", "default", "alice", "1.2.3.4", "us-east-1a", "running",
				},
				struct{ id, name, owner, ip, az, state string }{
					"i-dev", "dev", "alice", "5.6.7.8", "us-east-1b", "running",
				},
			),
		}

		// Project "myproject" exists only on "dev" VM (i-dev).
		// The probe checks test -d /mint/projects/<name>; exit code 0 = exists.
		remoteOutputs := map[string]string{
			"i-default": "", // empty = directory does not exist (test -d fails)
			"i-dev":     "", // empty = directory exists (test -d succeeds)
		}
		remoteRunner := func(
			ctx context.Context,
			sendKey mintaws.SendSSHPublicKeyAPI,
			instanceID, az, host string,
			port int,
			user string,
			command []string,
		) ([]byte, error) {
			if instanceID == "i-default" {
				// Project NOT found on this VM — return error (non-zero exit).
				return nil, fmt.Errorf("exit status 1")
			}
			// Project found on dev VM — return success.
			return []byte(remoteOutputs[instanceID]), nil
		}

		deps := &codeDeps{
			describe:          describe,
			owner:             "alice",
			runner:            runner,
			sendKey:           &mockSendSSHPublicKey{},
			runRemoteCommand:  remoteRunner,
			sshConfigPath:     sshConfigDir + "/config",
			sshConfigApproved: true,
		}

		cmd := newCodeCommandWithDeps(deps)
		root := newCodeTestRoot()
		root.AddCommand(cmd)
		root.SetOut(buf)
		root.SetErr(buf)
		// No --vm flag
		root.SetArgs([]string{"code", "myproject"})

		err := root.Execute()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should auto-select the "dev" VM and launch VS Code.
		if captured == nil {
			t.Fatal("expected VS Code launch, got none")
		}
		argsStr := strings.Join(captured.args, " ")
		if !strings.Contains(argsStr, "ssh-remote+mint-dev") {
			t.Errorf("expected ssh-remote+mint-dev, args: %v", captured.args)
		}
		if !strings.Contains(argsStr, "/mint/projects/myproject") {
			t.Errorf("expected /mint/projects/myproject, args: %v", captured.args)
		}
	})

	t.Run("multi-VM project on multiple VMs errors with VM names", func(t *testing.T) {
		buf := new(bytes.Buffer)
		var captured *capturedCommand
		runner := func(name string, args ...string) error {
			captured = &capturedCommand{name: name, args: args}
			return nil
		}

		sshConfigDir := t.TempDir()
		configDir := t.TempDir()
		t.Setenv("MINT_CONFIG_DIR", configDir)

		describe := &multiVMDescribeForCode{
			listOutput: makeMultiVMOutput(
				struct{ id, name, owner, ip, az, state string }{
					"i-default", "default", "alice", "1.2.3.4", "us-east-1a", "running",
				},
				struct{ id, name, owner, ip, az, state string }{
					"i-dev", "dev", "alice", "5.6.7.8", "us-east-1b", "running",
				},
			),
		}

		// Project found on BOTH VMs.
		remoteRunner := func(
			ctx context.Context,
			sendKey mintaws.SendSSHPublicKeyAPI,
			instanceID, az, host string,
			port int,
			user string,
			command []string,
		) ([]byte, error) {
			return []byte(""), nil // success = project exists on both
		}

		deps := &codeDeps{
			describe:          describe,
			owner:             "alice",
			runner:            runner,
			sendKey:           &mockSendSSHPublicKey{},
			runRemoteCommand:  remoteRunner,
			sshConfigPath:     sshConfigDir + "/config",
			sshConfigApproved: true,
		}

		cmd := newCodeCommandWithDeps(deps)
		root := newCodeTestRoot()
		root.AddCommand(cmd)
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"code", "myproject"})

		err := root.Execute()
		if err == nil {
			t.Fatal("expected error when project on multiple VMs, got nil")
		}
		errMsg := err.Error()
		if !strings.Contains(errMsg, "default") {
			t.Errorf("error should list VM name 'default', got: %s", errMsg)
		}
		if !strings.Contains(errMsg, "dev") {
			t.Errorf("error should list VM name 'dev', got: %s", errMsg)
		}
		if !strings.Contains(errMsg, "--vm") {
			t.Errorf("error should suggest --vm flag, got: %s", errMsg)
		}
		if captured != nil {
			t.Errorf("VS Code should not have been launched, but got: %s %v", captured.name, captured.args)
		}
	})

	t.Run("multi-VM project on none errors with suggestion", func(t *testing.T) {
		buf := new(bytes.Buffer)
		var captured *capturedCommand
		runner := func(name string, args ...string) error {
			captured = &capturedCommand{name: name, args: args}
			return nil
		}

		sshConfigDir := t.TempDir()
		configDir := t.TempDir()
		t.Setenv("MINT_CONFIG_DIR", configDir)

		describe := &multiVMDescribeForCode{
			listOutput: makeMultiVMOutput(
				struct{ id, name, owner, ip, az, state string }{
					"i-default", "default", "alice", "1.2.3.4", "us-east-1a", "running",
				},
				struct{ id, name, owner, ip, az, state string }{
					"i-dev", "dev", "alice", "5.6.7.8", "us-east-1b", "running",
				},
			),
		}

		// Project found on NEITHER VM.
		remoteRunner := func(
			ctx context.Context,
			sendKey mintaws.SendSSHPublicKeyAPI,
			instanceID, az, host string,
			port int,
			user string,
			command []string,
		) ([]byte, error) {
			return nil, fmt.Errorf("exit status 1") // directory not found
		}

		deps := &codeDeps{
			describe:          describe,
			owner:             "alice",
			runner:            runner,
			sendKey:           &mockSendSSHPublicKey{},
			runRemoteCommand:  remoteRunner,
			sshConfigPath:     sshConfigDir + "/config",
			sshConfigApproved: true,
		}

		cmd := newCodeCommandWithDeps(deps)
		root := newCodeTestRoot()
		root.AddCommand(cmd)
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"code", "myproject"})

		err := root.Execute()
		if err == nil {
			t.Fatal("expected error when project on no VMs, got nil")
		}
		errMsg := err.Error()
		if !strings.Contains(errMsg, "not found") {
			t.Errorf("error should say project not found, got: %s", errMsg)
		}
		if captured != nil {
			t.Errorf("VS Code should not have been launched, but got: %s %v", captured.name, captured.args)
		}
	})

	t.Run("non-running VMs skipped during scan", func(t *testing.T) {
		buf := new(bytes.Buffer)
		var captured *capturedCommand
		runner := func(name string, args ...string) error {
			captured = &capturedCommand{name: name, args: args}
			return nil
		}

		sshConfigDir := t.TempDir()
		configDir := t.TempDir()
		t.Setenv("MINT_CONFIG_DIR", configDir)

		// Three VMs: "default" (running), "dev" (running), "staging" (stopped).
		// stopped VMs are filtered out by vm.ListVMs (excluded states),
		// but let's also include a non-running "pending" VM.
		describe := &multiVMDescribeForCode{
			listOutput: makeMultiVMOutput(
				struct{ id, name, owner, ip, az, state string }{
					"i-default", "default", "alice", "1.2.3.4", "us-east-1a", "running",
				},
				struct{ id, name, owner, ip, az, state string }{
					"i-dev", "dev", "alice", "5.6.7.8", "us-east-1b", "running",
				},
				struct{ id, name, owner, ip, az, state string }{
					"i-staging", "staging", "alice", "9.10.11.12", "us-east-1c", "stopped",
				},
			),
		}

		// Project exists only on "dev".
		var probedIDs []string
		remoteRunner := func(
			ctx context.Context,
			sendKey mintaws.SendSSHPublicKeyAPI,
			instanceID, az, host string,
			port int,
			user string,
			command []string,
		) ([]byte, error) {
			probedIDs = append(probedIDs, instanceID)
			if instanceID == "i-dev" {
				return []byte(""), nil // success
			}
			return nil, fmt.Errorf("exit status 1")
		}

		deps := &codeDeps{
			describe:          describe,
			owner:             "alice",
			runner:            runner,
			sendKey:           &mockSendSSHPublicKey{},
			runRemoteCommand:  remoteRunner,
			sshConfigPath:     sshConfigDir + "/config",
			sshConfigApproved: true,
		}

		cmd := newCodeCommandWithDeps(deps)
		root := newCodeTestRoot()
		root.AddCommand(cmd)
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"code", "myproject"})

		err := root.Execute()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Stopped VM should NOT have been probed.
		for _, id := range probedIDs {
			if id == "i-staging" {
				t.Error("stopped VM i-staging should not have been probed")
			}
		}

		// Should auto-select "dev".
		if captured == nil {
			t.Fatal("expected VS Code launch, got none")
		}
		argsStr := strings.Join(captured.args, " ")
		if !strings.Contains(argsStr, "ssh-remote+mint-dev") {
			t.Errorf("expected ssh-remote+mint-dev, args: %v", captured.args)
		}
	})

	t.Run("explicit --vm flag bypasses auto-resolution", func(t *testing.T) {
		buf := new(bytes.Buffer)
		var captured *capturedCommand
		runner := func(name string, args ...string) error {
			captured = &capturedCommand{name: name, args: args}
			return nil
		}

		sshConfigDir := t.TempDir()
		configDir := t.TempDir()
		t.Setenv("MINT_CONFIG_DIR", configDir)

		// Multiple VMs exist, but --vm dev is explicit.
		describe := &multiVMDescribeForCode{
			listOutput: makeMultiVMOutput(
				struct{ id, name, owner, ip, az, state string }{
					"i-default", "default", "alice", "1.2.3.4", "us-east-1a", "running",
				},
				struct{ id, name, owner, ip, az, state string }{
					"i-dev", "dev", "alice", "5.6.7.8", "us-east-1b", "running",
				},
			),
			findOutputs: map[string]*ec2.DescribeInstancesOutput{
				"dev": makeRunningInstanceWithAZ("i-dev", "dev", "alice", "5.6.7.8", "us-east-1b"),
			},
		}

		// Remote runner should NOT be called for probing (--vm bypasses it).
		remoteRunner, calledIDs := perVMRemoteRunner(nil, nil)

		deps := &codeDeps{
			describe:          describe,
			owner:             "alice",
			runner:            runner,
			sendKey:           &mockSendSSHPublicKey{},
			runRemoteCommand:  remoteRunner,
			sshConfigPath:     sshConfigDir + "/config",
			sshConfigApproved: true,
		}

		cmd := newCodeCommandWithDeps(deps)
		root := newCodeTestRoot()
		root.AddCommand(cmd)
		root.SetOut(buf)
		root.SetErr(buf)
		// Explicit --vm dev
		root.SetArgs([]string{"--vm", "dev", "code", "myproject"})

		err := root.Execute()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if captured == nil {
			t.Fatal("expected VS Code launch, got none")
		}
		argsStr := strings.Join(captured.args, " ")
		if !strings.Contains(argsStr, "ssh-remote+mint-dev") {
			t.Errorf("expected ssh-remote+mint-dev, args: %v", captured.args)
		}
		if !strings.Contains(argsStr, "/mint/projects/myproject") {
			t.Errorf("expected /mint/projects/myproject, args: %v", captured.args)
		}

		// No SSH probes should have been made (--vm bypasses auto-resolution).
		if len(*calledIDs) != 0 {
			t.Errorf("expected no SSH probes with --vm flag, got calls to: %v", *calledIDs)
		}
	})

	t.Run("ListVMs error propagates", func(t *testing.T) {
		buf := new(bytes.Buffer)
		var captured *capturedCommand
		runner := func(name string, args ...string) error {
			captured = &capturedCommand{name: name, args: args}
			return nil
		}

		sshConfigDir := t.TempDir()
		configDir := t.TempDir()
		t.Setenv("MINT_CONFIG_DIR", configDir)

		describe := &multiVMDescribeForCode{
			listErr: fmt.Errorf("API rate limit"),
		}

		remoteRunner, _ := perVMRemoteRunner(nil, nil)

		deps := &codeDeps{
			describe:          describe,
			owner:             "alice",
			runner:            runner,
			sendKey:           &mockSendSSHPublicKey{},
			runRemoteCommand:  remoteRunner,
			sshConfigPath:     sshConfigDir + "/config",
			sshConfigApproved: true,
		}

		cmd := newCodeCommandWithDeps(deps)
		root := newCodeTestRoot()
		root.AddCommand(cmd)
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"code", "myproject"})

		err := root.Execute()
		if err == nil {
			t.Fatal("expected error when ListVMs fails, got nil")
		}
		if !strings.Contains(err.Error(), "API rate limit") {
			t.Errorf("error should contain API rate limit, got: %s", err.Error())
		}
		if captured != nil {
			t.Errorf("VS Code should not have been launched")
		}
	})
}

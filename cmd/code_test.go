package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/nicholasgasior/mint/internal/cli"
	"github.com/spf13/cobra"
)

func TestCodeCommand(t *testing.T) {
	tests := []struct {
		name              string
		describe          *mockDescribeForSSH
		owner             string
		vmName            string
		pathFlag          string
		sshConfigApproved bool
		wantErr           bool
		wantErrContain    string
		wantExec          bool
		checkCmd          func(t *testing.T, captured capturedCommand)
	}{
		{
			name: "successful code open with default path",
			describe: &mockDescribeForSSH{
				output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			owner:             "alice",
			sshConfigApproved: true,
			wantExec:          true,
			checkCmd: func(t *testing.T, captured capturedCommand) {
				t.Helper()
				if captured.name != "code" {
					t.Errorf("expected code command, got %q", captured.name)
				}
				argsStr := strings.Join(captured.args, " ")
				if !strings.Contains(argsStr, "--remote ssh-remote+mint-default") {
					t.Errorf("missing --remote flag, args: %v", captured.args)
				}
				if !strings.Contains(argsStr, "/home/ubuntu") {
					t.Errorf("missing default path, args: %v", captured.args)
				}
			},
		},
		{
			name: "custom path flag",
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
			name: "ssh config written when approved",
			describe: &mockDescribeForSSH{
				output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			owner:             "alice",
			sshConfigApproved: true,
			wantExec:          true,
			checkCmd: func(t *testing.T, captured capturedCommand) {
				t.Helper()
				if captured.name != "code" {
					t.Errorf("expected code command, got %q", captured.name)
				}
			},
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

			deps := &codeDeps{
				describe:          tt.describe,
				owner:             tt.owner,
				runner:            runner,
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
		})
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

	deps := &codeDeps{
		describe: &mockDescribeForSSH{
			output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
		},
		owner:             "alice",
		runner:            runner,
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

	deps := &codeDeps{
		describe: &mockDescribeForSSH{
			output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
		},
		owner:             "alice",
		runner:            runner,
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

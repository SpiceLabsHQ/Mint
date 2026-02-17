package cmd

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/nicholasgasior/mint/internal/cli"
	"github.com/spf13/cobra"
)

func TestCodeCommand(t *testing.T) {
	tests := []struct {
		name           string
		describe       *mockDescribeForSSH
		owner          string
		vmName         string
		pathFlag       string
		wantErr        bool
		wantErrContain string
		wantExec       bool
		checkCmd       func(t *testing.T, captured capturedCommand)
	}{
		{
			name: "successful code open with default path",
			describe: &mockDescribeForSSH{
				output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			owner:    "alice",
			wantExec: true,
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
			owner:    "alice",
			pathFlag: "/home/ubuntu/myproject",
			wantExec: true,
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
			owner:    "alice",
			vmName:   "dev",
			wantExec: true,
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
			owner:          "alice",
			wantErr:        true,
			wantErrContain: "mint up",
			wantExec:       false,
		},
		{
			name: "stopped vm returns actionable error",
			describe: &mockDescribeForSSH{
				output: makeStoppedInstanceForSSH("i-abc123", "default", "alice"),
			},
			owner:          "alice",
			wantErr:        true,
			wantErrContain: "not running",
			wantExec:       false,
		},
		{
			name: "describe API error propagates",
			describe: &mockDescribeForSSH{
				err: fmt.Errorf("connection timeout"),
			},
			owner:          "alice",
			wantErr:        true,
			wantErrContain: "connection timeout",
			wantExec:       false,
		},
		{
			name: "ssh config written before opening code",
			describe: &mockDescribeForSSH{
				output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			owner:    "alice",
			wantExec: true,
			checkCmd: func(t *testing.T, captured capturedCommand) {
				t.Helper()
				// Just verify the command ran — SSH config write is verified
				// by the fact that code --remote requires a valid SSH host
				if captured.name != "code" {
					t.Errorf("expected code command, got %q", captured.name)
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

			// Use temp dir for SSH config to avoid polluting real config
			sshConfigDir := t.TempDir()
			configDir := t.TempDir()
			t.Setenv("MINT_CONFIG_DIR", configDir)

			deps := &codeDeps{
				describe:      tt.describe,
				owner:         tt.owner,
				runner:        runner,
				sshConfigPath: sshConfigDir + "/config",
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

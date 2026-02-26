package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	mintaws "github.com/nicholasgasior/mint/internal/aws"
	"github.com/nicholasgasior/mint/internal/cli"
	"github.com/spf13/cobra"
)

// mockRemoteCommandRunner returns a RemoteCommandRunner that yields fixed output.
func mockRemoteCommandRunner(stdout []byte, err error) RemoteCommandRunner {
	return func(
		ctx context.Context,
		sendKey mintaws.SendSSHPublicKeyAPI,
		instanceID, az, host string,
		port int,
		user string,
		command []string,
	) ([]byte, error) {
		return stdout, err
	}
}

// mockDescribeForSessions implements mintaws.DescribeInstancesAPI for sessions tests.
type mockDescribeForSessions struct {
	output *ec2.DescribeInstancesOutput
	err    error
}

func (m *mockDescribeForSessions) DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return m.output, m.err
}

// makeRunningInstanceForSessions creates a running instance for sessions tests.
func makeRunningInstanceForSessions(id, vmName, owner, ip, az string) *ec2.DescribeInstancesOutput {
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

// newTestRootForSessions creates a minimal root command for sessions tests.
func newTestRootForSessions() *cobra.Command {
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

func TestSessionsCommand(t *testing.T) {
	tests := []struct {
		name           string
		describe       *mockDescribeForSessions
		remoteOutput   []byte
		remoteErr      error
		owner          string
		vmName         string
		jsonOutput     bool
		wantErr        bool
		wantErrContain string
		wantOutput     []string
		wantNotOutput  []string
	}{
		{
			name: "parses tmux sessions and displays table",
			describe: &mockDescribeForSessions{
				output: makeRunningInstanceForSessions("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			remoteOutput: []byte("main 3 1 1700000000\ndev 2 0 1700001000\n"),
			owner:        "alice",
			wantOutput:   []string{"main", "3", "attached", "dev", "2", "detached"},
		},
		{
			name: "json output returns array of session objects",
			describe: &mockDescribeForSessions{
				output: makeRunningInstanceForSessions("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			remoteOutput: []byte("main 3 1 1700000000\n"),
			owner:        "alice",
			jsonOutput:   true,
			wantOutput:   []string{`"name"`, `"main"`, `"windows"`, `"attached"`},
		},
		{
			name: "empty output shows no sessions message",
			describe: &mockDescribeForSessions{
				output: makeRunningInstanceForSessions("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			remoteOutput: []byte(""),
			owner:        "alice",
			wantOutput:   []string{"No active sessions"},
		},
		{
			name: "tmux no server running is not an error",
			describe: &mockDescribeForSessions{
				output: makeRunningInstanceForSessions("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			remoteOutput: nil,
			remoteErr:    fmt.Errorf("remote command failed: exit status 1 (stderr: no server running on /tmp/tmux-1000/default)"),
			owner:        "alice",
			wantOutput:   []string{"No active sessions"},
		},
		{
			name: "tmux no sessions is not an error",
			describe: &mockDescribeForSessions{
				output: makeRunningInstanceForSessions("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			remoteOutput: nil,
			remoteErr:    fmt.Errorf("remote command failed: exit status 1 (stderr: no sessions)"),
			owner:        "alice",
			wantOutput:   []string{"No active sessions"},
		},
		{
			// Bug #141: Ubuntu 24.04 tmux outputs "error connecting to <socket>"
			// instead of "no server running" when the server socket doesn't exist.
			// This must be treated as "no sessions", not an error.
			name: "ubuntu 24.04 tmux socket not found is not an error",
			describe: &mockDescribeForSessions{
				output: makeRunningInstanceForSessions("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			remoteOutput: nil,
			remoteErr:    fmt.Errorf("remote command failed: exit status 1 (stderr: error connecting to /tmp/tmux-1000/default (No such file or directory))"),
			owner:        "alice",
			wantOutput:   []string{"No active sessions"},
		},
		{
			name: "VM not found returns error",
			describe: &mockDescribeForSessions{
				output: &ec2.DescribeInstancesOutput{},
			},
			owner:          "alice",
			wantErr:        true,
			wantErrContain: "mint up",
		},
		{
			name: "stopped VM returns error",
			describe: &mockDescribeForSessions{
				output: &ec2.DescribeInstancesOutput{
					Reservations: []ec2types.Reservation{{
						Instances: []ec2types.Instance{{
							InstanceId:   aws.String("i-stopped"),
							InstanceType: ec2types.InstanceTypeT3Medium,
							State: &ec2types.InstanceState{
								Name: ec2types.InstanceStateNameStopped,
							},
							Tags: []ec2types.Tag{
								{Key: aws.String("mint:vm"), Value: aws.String("default")},
								{Key: aws.String("mint:owner"), Value: aws.String("alice")},
							},
						}},
					}},
				},
			},
			owner:          "alice",
			wantErr:        true,
			wantErrContain: "not running",
		},
		{
			name: "describe API error propagates",
			describe: &mockDescribeForSessions{
				err: fmt.Errorf("throttled"),
			},
			owner:          "alice",
			wantErr:        true,
			wantErrContain: "throttled",
		},
		{
			name: "real remote command error propagates",
			describe: &mockDescribeForSessions{
				output: makeRunningInstanceForSessions("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			remoteErr:      fmt.Errorf("remote command failed: connection refused"),
			owner:          "alice",
			wantErr:        true,
			wantErrContain: "connection refused",
		},
		{
			name: "SSH connection refused wrapped with bootstrap-aware context",
			describe: &mockDescribeForSessions{
				output: makeRunningInstanceForSessions("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			remoteErr:      fmt.Errorf("remote command failed: exit status 255 (stderr: ssh: connect to host 1.2.3.4 port 41122: Connection refused)"),
			owner:          "alice",
			wantErr:        true,
			wantErrContain: "port 41122 refused",
		},
		{
			name: "SSH connection refused bootstrap context mentions mint doctor",
			describe: &mockDescribeForSessions{
				output: makeRunningInstanceForSessions("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			remoteErr:      fmt.Errorf("remote command failed: exit status 255 (stderr: ssh: connect to host 1.2.3.4 port 41122: Connection refused)"),
			owner:          "alice",
			wantErr:        true,
			wantErrContain: "mint doctor",
		},
		{
			name: "non-default VM name",
			describe: &mockDescribeForSessions{
				output: makeRunningInstanceForSessions("i-dev456", "dev", "alice", "10.0.0.1", "us-west-2a"),
			},
			remoteOutput: []byte("work 1 0 1700000000\n"),
			owner:        "alice",
			vmName:       "dev",
			wantOutput:   []string{"work", "1", "detached"},
		},
		{
			name: "json output for no sessions returns empty array",
			describe: &mockDescribeForSessions{
				output: makeRunningInstanceForSessions("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			remoteOutput: []byte(""),
			owner:        "alice",
			jsonOutput:   true,
			wantOutput:   []string{"[]"},
		},
		{
			name: "json output for no server running returns empty array",
			describe: &mockDescribeForSessions{
				output: makeRunningInstanceForSessions("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			remoteErr:  fmt.Errorf("remote command failed: exit status 1 (stderr: no server running on /tmp/tmux-1000/default)"),
			owner:      "alice",
			jsonOutput: true,
			wantOutput: []string{"[]"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := new(bytes.Buffer)

			deps := &sessionsDeps{
				describe:  tt.describe,
				owner:     tt.owner,
				remoteRun: mockRemoteCommandRunner(tt.remoteOutput, tt.remoteErr),
			}

			cmd := newSessionsCommandWithDeps(deps)
			root := newTestRootForSessions()
			root.AddCommand(cmd)
			root.SetOut(buf)
			root.SetErr(buf)

			args := []string{"sessions"}
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

func TestParseTmuxSessions(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int
		check    func(t *testing.T, sessions []tmuxSession)
	}{
		{
			name:     "single session",
			input:    "main 3 1 1700000000\n",
			expected: 1,
			check: func(t *testing.T, sessions []tmuxSession) {
				s := sessions[0]
				if s.Name != "main" {
					t.Errorf("name = %q, want main", s.Name)
				}
				if s.Windows != 3 {
					t.Errorf("windows = %d, want 3", s.Windows)
				}
				if !s.Attached {
					t.Error("expected attached = true")
				}
				if s.CreatedEpoch != 1700000000 {
					t.Errorf("created_epoch = %d, want 1700000000", s.CreatedEpoch)
				}
			},
		},
		{
			name:     "multiple sessions",
			input:    "main 3 1 1700000000\ndev 2 0 1700001000\ntest 1 0 1700002000\n",
			expected: 3,
		},
		{
			name:     "empty input",
			input:    "",
			expected: 0,
		},
		{
			name:     "whitespace only",
			input:    "  \n  \n",
			expected: 0,
		},
		{
			name:     "detached session",
			input:    "bg 1 0 1700000000\n",
			expected: 1,
			check: func(t *testing.T, sessions []tmuxSession) {
				if sessions[0].Attached {
					t.Error("expected attached = false")
				}
			},
		},
		{
			name:     "session name with colon",
			input:    "my:session 2 1 1700000000\n",
			expected: 1,
			check: func(t *testing.T, sessions []tmuxSession) {
				if sessions[0].Name != "my:session" {
					t.Errorf("name = %q, want my:session", sessions[0].Name)
				}
			},
		},
		{
			name:     "malformed line skipped",
			input:    "main 3 1 1700000000\nbadline\ndev 2 0 1700001000\n",
			expected: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessions := parseTmuxSessions(tt.input)
			if len(sessions) != tt.expected {
				t.Fatalf("got %d sessions, want %d", len(sessions), tt.expected)
			}
			if tt.check != nil {
				tt.check(t, sessions)
			}
		})
	}
}

func TestIsTmuxNoSessionsError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "no server running",
			err:      fmt.Errorf("remote command failed: exit status 1 (stderr: no server running on /tmp/tmux-1000/default)"),
			expected: true,
		},
		{
			name:     "no sessions",
			err:      fmt.Errorf("remote command failed: exit status 1 (stderr: no sessions)"),
			expected: true,
		},
		{
			name:     "connection refused",
			err:      fmt.Errorf("remote command failed: connection refused"),
			expected: false,
		},
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "generic error",
			err:      fmt.Errorf("something went wrong"),
			expected: false,
		},
		{
			// Bug #141: Ubuntu 24.04 tmux outputs a different message when the
			// socket file doesn't exist — must be treated as "no sessions".
			name:     "ubuntu 24.04 tmux error connecting to socket",
			err:      fmt.Errorf("remote command failed: exit status 1 (stderr: error connecting to /tmp/tmux-1000/default (No such file or directory))"),
			expected: true,
		},
		{
			// Ubuntu 24.04 bare error message without remote wrapper.
			name:     "ubuntu 24.04 bare error connecting message",
			err:      fmt.Errorf("error connecting to /tmp/tmux-1000/default (No such file or directory)"),
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isTmuxNoSessionsError(tt.err)
			if got != tt.expected {
				t.Errorf("isTmuxNoSessionsError(%v) = %v, want %v", tt.err, got, tt.expected)
			}
		})
	}
}

// TestSessionsSpinnerWiring confirms that the spinner emits progress messages
// during session info fetch when --verbose is active.
// In non-interactive (test) mode the spinner writes plain timestamped lines,
// so we can assert on their presence in the output buffer.
func TestSessionsSpinnerWiring(t *testing.T) {
	t.Run("spinner emits Fetching session info during lookup", func(t *testing.T) {
		buf := new(bytes.Buffer)

		deps := &sessionsDeps{
			describe: &mockDescribeForSessions{
				output: makeRunningInstanceForSessions("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
			},
			owner:     "alice",
			remoteRun: mockRemoteCommandRunner([]byte("main 1 1 1700000000\n"), nil),
		}

		cmd := newSessionsCommandWithDeps(deps)
		root := newTestRootForSessions()
		root.AddCommand(cmd)
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"--verbose", "sessions"})

		if err := root.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		output := buf.String()
		if !strings.Contains(output, "Fetching session info") {
			t.Errorf("expected spinner message %q in output, got:\n%s", "Fetching session info", output)
		}
	})

	t.Run("spinner emits Fetching session info even when VM not found", func(t *testing.T) {
		buf := new(bytes.Buffer)

		deps := &sessionsDeps{
			describe: &mockDescribeForSessions{
				output: &ec2.DescribeInstancesOutput{},
			},
			owner:     "alice",
			remoteRun: mockRemoteCommandRunner(nil, nil),
		}

		cmd := newSessionsCommandWithDeps(deps)
		root := newTestRootForSessions()
		root.AddCommand(cmd)
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"--verbose", "sessions"})

		err := root.Execute()
		if err == nil {
			t.Fatal("expected error for missing VM")
		}

		output := buf.String()
		if !strings.Contains(output, "Fetching session info") {
			t.Errorf("expected spinner message %q in output, got:\n%s", "Fetching session info", output)
		}
	})
}

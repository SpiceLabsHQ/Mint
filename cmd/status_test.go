package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

func TestStatusCommand(t *testing.T) {
	recentLaunch := time.Now().Add(-30 * time.Minute)

	tests := []struct {
		name           string
		describe       *mockDescribeInstances
		owner          string
		vmName         string
		jsonOutput     bool
		wantErr        bool
		wantErrContain string
		wantOutput     []string
	}{
		{
			name: "happy path shows VM details",
			describe: &mockDescribeInstances{
				output: makeInstanceWithTime("i-abc123", "default", "alice", "running", "1.2.3.4", "m6i.xlarge", "complete", recentLaunch),
			},
			owner:      "alice",
			vmName:     "default",
			wantOutput: []string{"i-abc123", "running", "1.2.3.4", "m6i.xlarge", "complete"},
		},
		{
			name: "VM not found returns error",
			describe: &mockDescribeInstances{
				output: &ec2.DescribeInstancesOutput{},
			},
			owner:          "alice",
			vmName:         "default",
			wantErr:        true,
			wantErrContain: "not found",
		},
		{
			name: "json output format",
			describe: &mockDescribeInstances{
				output: makeInstanceWithTime("i-abc123", "default", "alice", "running", "1.2.3.4", "m6i.xlarge", "complete", recentLaunch),
			},
			owner:      "alice",
			vmName:     "default",
			jsonOutput: true,
			wantOutput: []string{`"id"`, `"i-abc123"`, `"state"`, `"running"`},
		},
		{
			name: "json output is valid JSON object",
			describe: &mockDescribeInstances{
				output: makeInstanceWithTime("i-abc123", "default", "alice", "running", "1.2.3.4", "m6i.xlarge", "complete", recentLaunch),
			},
			owner:      "alice",
			vmName:     "default",
			jsonOutput: true,
		},
		{
			name: "API error propagates",
			describe: &mockDescribeInstances{
				err: fmt.Errorf("access denied"),
			},
			owner:          "alice",
			vmName:         "default",
			wantErr:        true,
			wantErrContain: "access denied",
		},
		{
			name: "non-default VM name",
			describe: &mockDescribeInstances{
				output: makeInstanceWithTime("i-dev456", "dev-box", "bob", "stopped", "", "t3.medium", "complete", recentLaunch),
			},
			owner:      "bob",
			vmName:     "dev-box",
			wantOutput: []string{"i-dev456", "stopped", "t3.medium", "dev-box"},
		},
		{
			name: "stopped VM shows no IP",
			describe: &mockDescribeInstances{
				output: makeInstanceWithTime("i-stopped", "default", "alice", "stopped", "", "m6i.xlarge", "complete", recentLaunch),
			},
			owner:      "alice",
			vmName:     "default",
			wantOutput: []string{"stopped", "-"},
		},
		{
			name: "bootstrap failed shown",
			describe: &mockDescribeInstances{
				output: makeInstanceWithTime("i-fail", "default", "alice", "running", "1.2.3.4", "m6i.xlarge", "failed", recentLaunch),
			},
			owner:      "alice",
			vmName:     "default",
			wantOutput: []string{"FAILED"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := new(bytes.Buffer)

			deps := &statusDeps{
				describe: tt.describe,
				owner:    tt.owner,
			}

			cmd := newStatusCommandWithDeps(deps)
			root := newTestRoot()
			root.AddCommand(cmd)
			root.SetOut(buf)
			root.SetErr(buf)

			args := []string{"status"}
			if tt.vmName != "" && tt.vmName != "default" {
				args = append(args, "--vm", tt.vmName)
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

			// Validate JSON output is parseable as an object.
			if tt.jsonOutput {
				var result map[string]interface{}
				trimmed := strings.TrimSpace(output)
				if err := json.Unmarshal([]byte(trimmed), &result); err != nil {
					t.Errorf("JSON output is not a valid object: %v\nOutput: %s", err, output)
				}
			}
		})
	}
}

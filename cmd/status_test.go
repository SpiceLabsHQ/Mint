package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
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

// makeInstanceWithVolumeTags creates a DescribeInstancesOutput with volume size tags.
func makeInstanceWithVolumeTags(id, vmName, owner, state, ip, instanceType, bootstrap string, launchTime time.Time, rootGB, projectGB string) *ec2.DescribeInstancesOutput {
	out := makeInstanceWithTime(id, vmName, owner, state, ip, instanceType, bootstrap, launchTime)
	inst := &out.Reservations[0].Instances[0]
	if rootGB != "" {
		inst.Tags = append(inst.Tags, ec2types.Tag{
			Key: aws.String("mint:root-volume-gb"), Value: aws.String(rootGB),
		})
	}
	if projectGB != "" {
		inst.Tags = append(inst.Tags, ec2types.Tag{
			Key: aws.String("mint:project-volume-gb"), Value: aws.String(projectGB),
		})
	}
	return out
}

func TestStatusShowsVolumeInfo(t *testing.T) {
	recentLaunch := time.Now().Add(-30 * time.Minute)
	buf := new(bytes.Buffer)

	deps := &statusDeps{
		describe: &mockDescribeInstances{
			output: makeInstanceWithVolumeTags("i-vol1", "default", "alice", "running", "1.2.3.4", "m6i.xlarge", "complete", recentLaunch, "200", "50"),
		},
		owner: "alice",
	}

	cmd := newStatusCommandWithDeps(deps)
	root := newTestRoot()
	root.AddCommand(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"status"})

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Root Vol:  200 GB") {
		t.Errorf("output missing root volume info, got:\n%s", output)
	}
	if !strings.Contains(output, "Proj Vol:  50 GB") {
		t.Errorf("output missing project volume info, got:\n%s", output)
	}
}

func TestStatusHidesVolumesWhenZero(t *testing.T) {
	recentLaunch := time.Now().Add(-30 * time.Minute)
	buf := new(bytes.Buffer)

	deps := &statusDeps{
		describe: &mockDescribeInstances{
			output: makeInstanceWithTime("i-novol", "default", "alice", "running", "1.2.3.4", "m6i.xlarge", "complete", recentLaunch),
		},
		owner: "alice",
	}

	cmd := newStatusCommandWithDeps(deps)
	root := newTestRoot()
	root.AddCommand(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"status"})

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if strings.Contains(output, "Root Vol:") {
		t.Errorf("output should NOT contain Root Vol when zero, got:\n%s", output)
	}
	if strings.Contains(output, "Proj Vol:") {
		t.Errorf("output should NOT contain Proj Vol when zero, got:\n%s", output)
	}
}

func TestStatusJSONIncludesVolumes(t *testing.T) {
	recentLaunch := time.Now().Add(-30 * time.Minute)
	buf := new(bytes.Buffer)

	deps := &statusDeps{
		describe: &mockDescribeInstances{
			output: makeInstanceWithVolumeTags("i-vol2", "default", "alice", "running", "1.2.3.4", "m6i.xlarge", "complete", recentLaunch, "200", "50"),
		},
		owner: "alice",
	}

	cmd := newStatusCommandWithDeps(deps)
	root := newTestRoot()
	root.AddCommand(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"status", "--json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if v, ok := result["root_volume_gb"]; !ok {
		t.Error("JSON output missing root_volume_gb field")
	} else if v.(float64) != 200 {
		t.Errorf("root_volume_gb = %v, want 200", v)
	}

	if v, ok := result["project_volume_gb"]; !ok {
		t.Error("JSON output missing project_volume_gb field")
	} else if v.(float64) != 50 {
		t.Errorf("project_volume_gb = %v, want 50", v)
	}
}

func TestStatusShowsVersionNotice(t *testing.T) {
	recentLaunch := time.Now().Add(-30 * time.Minute)
	buf := new(bytes.Buffer)

	deps := &statusDeps{
		describe: &mockDescribeInstances{
			output: makeInstanceWithTime("i-ver1", "default", "alice", "running", "1.2.3.4", "m6i.xlarge", "complete", recentLaunch),
		},
		owner: "alice",
	}

	cmd := newStatusCommandWithDeps(deps)
	root := newTestRoot()
	root.AddCommand(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"status"})

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	// Default version in tests is "dev" (set in cmd/version.go)
	if !strings.Contains(output, "mint dev") {
		t.Errorf("output missing version notice, got:\n%s", output)
	}
}

func TestStatusAppendsVersionNoticeInHumanMode(t *testing.T) {
	// Seed a version cache file with a newer version so appendVersionNotice
	// will produce output. This test verifies that runStatus calls
	// appendVersionNotice after writeStatusHuman in human output mode.
	//
	// appendVersionNotice uses isUpdateAvailable which returns false for the
	// "dev" build-time default. Set version to a real semver for this test
	// so the update banner is triggered, then restore the original value.
	origVersion := version
	version = "v1.0.0"
	t.Cleanup(func() { version = origVersion })

	tmpDir := t.TempDir()
	cacheJSON := `{"latest_version":"v99.0.0","checked_at":"` +
		time.Now().UTC().Format(time.RFC3339) + `"}`
	if err := os.WriteFile(
		tmpDir+"/version-cache.json",
		[]byte(cacheJSON),
		0o644,
	); err != nil {
		t.Fatalf("writing version cache: %v", err)
	}
	t.Setenv("MINT_CONFIG_DIR", tmpDir)

	recentLaunch := time.Now().Add(-30 * time.Minute)
	buf := new(bytes.Buffer)

	deps := &statusDeps{
		describe: &mockDescribeInstances{
			output: makeInstanceWithTime("i-vn1", "default", "alice", "running", "1.2.3.4", "m6i.xlarge", "complete", recentLaunch),
		},
		owner: "alice",
	}

	cmd := newStatusCommandWithDeps(deps)
	root := newTestRoot()
	root.AddCommand(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"status"})

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "v99.0.0") {
		t.Errorf("version notice missing from human output; got:\n%s", output)
	}
}

func TestStatusDoesNotAppendVersionNoticeInJSONMode(t *testing.T) {
	// Seed a version cache file with a newer version. In JSON mode,
	// appendVersionNotice must NOT be called, so the output stays valid JSON.
	tmpDir := t.TempDir()
	cacheJSON := `{"latest_version":"v99.0.0","checked_at":"` +
		time.Now().UTC().Format(time.RFC3339) + `"}`
	if err := os.WriteFile(
		tmpDir+"/version-cache.json",
		[]byte(cacheJSON),
		0o644,
	); err != nil {
		t.Fatalf("writing version cache: %v", err)
	}
	t.Setenv("MINT_CONFIG_DIR", tmpDir)

	recentLaunch := time.Now().Add(-30 * time.Minute)
	buf := new(bytes.Buffer)

	deps := &statusDeps{
		describe: &mockDescribeInstances{
			output: makeInstanceWithTime("i-vn2", "default", "alice", "running", "1.2.3.4", "m6i.xlarge", "complete", recentLaunch),
		},
		owner: "alice",
	}

	cmd := newStatusCommandWithDeps(deps)
	root := newTestRoot()
	root.AddCommand(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"status", "--json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	// JSON output must remain a valid object — no version banner appended.
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &result); err != nil {
		t.Errorf("JSON output is not valid (version notice may have been appended): %v\nOutput: %s", err, output)
	}
	if strings.Contains(output, "v99.0.0") {
		t.Errorf("version notice must NOT appear in JSON output; got:\n%s", output)
	}
}

func TestStatusJSONIncludesVersion(t *testing.T) {
	recentLaunch := time.Now().Add(-30 * time.Minute)
	buf := new(bytes.Buffer)

	deps := &statusDeps{
		describe: &mockDescribeInstances{
			output: makeInstanceWithTime("i-ver2", "default", "alice", "running", "1.2.3.4", "m6i.xlarge", "complete", recentLaunch),
		},
		owner: "alice",
	}

	cmd := newStatusCommandWithDeps(deps)
	root := newTestRoot()
	root.AddCommand(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"status", "--json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if v, ok := result["mint_version"]; !ok {
		t.Error("JSON output missing mint_version field")
	} else if v.(string) != "dev" {
		t.Errorf("mint_version = %q, want %q", v, "dev")
	}
}

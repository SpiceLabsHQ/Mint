package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2instanceconnect"
	mintaws "github.com/nicholasgasior/mint/internal/aws"
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

func TestStatusDoesNotAppendVersionBannerInJSONMode(t *testing.T) {
	// Seed a version cache file with a newer version. In JSON mode,
	// the human-readable version banner must NOT be appended after the JSON
	// object — version info is instead embedded in the JSON fields.
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
	// JSON output must remain a valid object — the human banner must NOT be appended.
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &result); err != nil {
		t.Errorf("JSON output is not valid (banner may have been appended): %v\nOutput: %s", err, output)
	}
	// The human-readable banner separator must not appear.
	if strings.Contains(output, "A new version of mint is available") {
		t.Errorf("human-readable version banner must NOT appear in JSON output; got:\n%s", output)
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

func TestStatusShowsDiskUsage(t *testing.T) {
	buf := new(bytes.Buffer)

	deps := &statusDeps{
		describe: &mockDescribeInstances{
			output: makeRunningInstanceWithAZ("i-disk1", "default", "alice", "1.2.3.4", "us-east-1a"),
		},
		sendKey:   &mockSendSSHPublicKey{output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true}},
		owner:     "alice",
		remoteRun: mockRemoteCommandRunner([]byte("Use%\n 42%\n"), nil),
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
	if !strings.Contains(output, "Disk:      42%") {
		t.Errorf("output missing disk usage, got:\n%s", output)
	}
	if strings.Contains(output, "[WARN]") {
		t.Errorf("output should NOT contain [WARN] for 42%%, got:\n%s", output)
	}
}

func TestStatusDiskUsageWarning(t *testing.T) {
	buf := new(bytes.Buffer)

	deps := &statusDeps{
		describe: &mockDescribeInstances{
			output: makeRunningInstanceWithAZ("i-disk2", "default", "alice", "1.2.3.4", "us-east-1a"),
		},
		sendKey:   &mockSendSSHPublicKey{output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true}},
		owner:     "alice",
		remoteRun: mockRemoteCommandRunner([]byte("Use%\n 85%\n"), nil),
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
	if !strings.Contains(output, "Disk:      85% [WARN]") {
		t.Errorf("output missing disk usage warning, got:\n%s", output)
	}
}

func TestStatusDiskUsageWarningAt80(t *testing.T) {
	buf := new(bytes.Buffer)

	deps := &statusDeps{
		describe: &mockDescribeInstances{
			output: makeRunningInstanceWithAZ("i-disk80", "default", "alice", "1.2.3.4", "us-east-1a"),
		},
		sendKey:   &mockSendSSHPublicKey{output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true}},
		owner:     "alice",
		remoteRun: mockRemoteCommandRunner([]byte("Use%\n 80%\n"), nil),
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
	if !strings.Contains(output, "80% [WARN]") {
		t.Errorf("expected [WARN] at exactly 80%%, got:\n%s", output)
	}
}

func TestStatusDiskUsageSSHFailure(t *testing.T) {
	buf := new(bytes.Buffer)

	deps := &statusDeps{
		describe: &mockDescribeInstances{
			output: makeRunningInstanceWithAZ("i-disk3", "default", "alice", "1.2.3.4", "us-east-1a"),
		},
		sendKey:   &mockSendSSHPublicKey{output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true}},
		owner:     "alice",
		remoteRun: mockRemoteCommandRunner(nil, fmt.Errorf("connection refused")),
	}

	cmd := newStatusCommandWithDeps(deps)
	root := newTestRoot()
	root.AddCommand(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"status"})

	// Should NOT return an error — graceful fallback.
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Disk:      unknown") {
		t.Errorf("expected 'unknown' disk usage on SSH failure, got:\n%s", output)
	}
}

func TestStatusDiskUsageJSON(t *testing.T) {
	buf := new(bytes.Buffer)

	deps := &statusDeps{
		describe: &mockDescribeInstances{
			output: makeRunningInstanceWithAZ("i-disk4", "default", "alice", "1.2.3.4", "us-east-1a"),
		},
		sendKey:   &mockSendSSHPublicKey{output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true}},
		owner:     "alice",
		remoteRun: mockRemoteCommandRunner([]byte("Use%\n 42%\n"), nil),
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

	v, ok := result["disk_usage_pct"]
	if !ok {
		t.Fatal("JSON output missing disk_usage_pct field")
	}
	if v.(float64) != 42 {
		t.Errorf("disk_usage_pct = %v, want 42", v)
	}
}

func TestStatusDiskUsageJSONSSHFailure(t *testing.T) {
	buf := new(bytes.Buffer)

	deps := &statusDeps{
		describe: &mockDescribeInstances{
			output: makeRunningInstanceWithAZ("i-disk5", "default", "alice", "1.2.3.4", "us-east-1a"),
		},
		sendKey:   &mockSendSSHPublicKey{output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true}},
		owner:     "alice",
		remoteRun: mockRemoteCommandRunner(nil, fmt.Errorf("connection refused")),
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

	// disk_usage_pct should be omitted when SSH fails.
	if _, ok := result["disk_usage_pct"]; ok {
		t.Error("disk_usage_pct should be omitted in JSON when SSH fails")
	}
}

func TestStatusNoDiskCheckWhenStopped(t *testing.T) {
	recentLaunch := time.Now().Add(-30 * time.Minute)
	buf := new(bytes.Buffer)

	remoteCallCount := 0
	trackingRunner := func(
		ctx context.Context,
		sendKey mintaws.SendSSHPublicKeyAPI,
		instanceID, az, host string,
		port int,
		user string,
		command []string,
	) ([]byte, error) {
		remoteCallCount++
		return []byte("Use%\n 50%\n"), nil
	}

	deps := &statusDeps{
		describe: &mockDescribeInstances{
			output: makeInstanceWithTime("i-stopped2", "default", "alice", "stopped", "", "m6i.xlarge", "complete", recentLaunch),
		},
		sendKey:   &mockSendSSHPublicKey{output: &ec2instanceconnect.SendSSHPublicKeyOutput{Success: true}},
		owner:     "alice",
		remoteRun: trackingRunner,
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

	if remoteCallCount != 0 {
		t.Errorf("remote command runner was called %d times for stopped VM, expected 0", remoteCallCount)
	}

	output := buf.String()
	if strings.Contains(output, "Disk:") {
		t.Errorf("stopped VM should NOT show Disk line, got:\n%s", output)
	}
}

func TestStatusJSONIncludesVersionCheckFields(t *testing.T) {
	recentLaunch := time.Now().Add(-30 * time.Minute)
	buf := new(bytes.Buffer)

	deps := &statusDeps{
		describe: &mockDescribeInstances{
			output: makeInstanceWithTime("i-vc1", "default", "alice", "running", "1.2.3.4", "m6i.xlarge", "complete", recentLaunch),
		},
		owner:          "alice",
		versionChecker: stubVersionChecker(true, strPtr("v2.0.0")),
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

	// update_available must be true.
	if v, ok := result["update_available"]; !ok {
		t.Error("JSON missing update_available field")
	} else if v.(bool) != true {
		t.Errorf("update_available = %v, want true", v)
	}

	// latest_version must be the string returned by the checker.
	if v, ok := result["latest_version"]; !ok {
		t.Error("JSON missing latest_version field")
	} else if v.(string) != "v2.0.0" {
		t.Errorf("latest_version = %q, want %q", v, "v2.0.0")
	}
}

func TestStatusJSONVersionFieldsFailOpen(t *testing.T) {
	// When the version checker fails, update_available must be false and
	// latest_version must be null. The command must still succeed.
	recentLaunch := time.Now().Add(-30 * time.Minute)
	buf := new(bytes.Buffer)

	deps := &statusDeps{
		describe: &mockDescribeInstances{
			output: makeInstanceWithTime("i-vc2", "default", "alice", "running", "1.2.3.4", "m6i.xlarge", "complete", recentLaunch),
		},
		owner:          "alice",
		versionChecker: stubVersionChecker(false, nil),
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

	if v, ok := result["update_available"]; !ok {
		t.Error("JSON missing update_available field")
	} else if v.(bool) != false {
		t.Errorf("update_available = %v, want false", v)
	}

	if v, ok := result["latest_version"]; !ok {
		t.Error("JSON missing latest_version field")
	} else if v != nil {
		t.Errorf("latest_version = %v, want null", v)
	}
}

func TestStatusJSONVersionFieldsOnLatestVersion(t *testing.T) {
	// When running on the latest version, update_available must be false
	// and latest_version must be the current version.
	recentLaunch := time.Now().Add(-30 * time.Minute)
	buf := new(bytes.Buffer)

	deps := &statusDeps{
		describe: &mockDescribeInstances{
			output: makeInstanceWithTime("i-vc3", "default", "alice", "running", "1.2.3.4", "m6i.xlarge", "complete", recentLaunch),
		},
		owner:          "alice",
		versionChecker: stubVersionChecker(false, strPtr("v1.0.0")),
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

	if v, ok := result["update_available"]; !ok {
		t.Error("JSON missing update_available field")
	} else if v.(bool) != false {
		t.Errorf("update_available = %v, want false (already on latest)", v)
	}

	if v, ok := result["latest_version"]; !ok {
		t.Error("JSON missing latest_version field")
	} else if v.(string) != "v1.0.0" {
		t.Errorf("latest_version = %q, want %q", v, "v1.0.0")
	}
}

// TestStatusJSONErrorRouting verifies that when --json is set, error conditions
// (VM not found, AWS error) write {"error":"..."} to stdout and nothing to
// stderr, instead of plaintext to stderr. This is the JSON contract for
// machine-readable consumers.
func TestStatusJSONErrorRouting(t *testing.T) {
	tests := []struct {
		name           string
		describe       *mockDescribeInstances
		owner          string
		wantErrKey     string // substring expected inside the JSON "error" value
	}{
		{
			name: "VM not found writes JSON error to stdout",
			describe: &mockDescribeInstances{
				output: &ec2.DescribeInstancesOutput{},
			},
			owner:      "alice",
			wantErrKey: "not found",
		},
		{
			name: "AWS error writes JSON error to stdout",
			describe: &mockDescribeInstances{
				err: fmt.Errorf("access denied"),
			},
			owner:      "alice",
			wantErrKey: "access denied",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout := new(bytes.Buffer)
			stderr := new(bytes.Buffer)

			deps := &statusDeps{
				describe: tt.describe,
				owner:    tt.owner,
			}

			cmd := newStatusCommandWithDeps(deps)
			root := newTestRoot()
			root.AddCommand(cmd)
			// Separate stdout from stderr so we can verify routing.
			root.SetOut(stdout)
			root.SetErr(stderr)
			root.SetArgs([]string{"status", "--json"})

			err := root.Execute()

			// Command must exit non-zero (return non-nil error or silentExitError).
			// silentExitError has an empty message — either way we expect failure.
			if err == nil {
				t.Fatal("expected non-nil error, got nil")
			}

			// Stderr must be empty — no plaintext error messages when --json is set.
			if got := stderr.String(); got != "" {
				t.Errorf("stderr must be empty in --json mode, got: %q", got)
			}

			// Stdout must contain valid JSON with an "error" key.
			outStr := strings.TrimSpace(stdout.String())
			if outStr == "" {
				t.Fatal("stdout must contain JSON error object, got empty string")
			}

			var result map[string]interface{}
			if err := json.Unmarshal([]byte(outStr), &result); err != nil {
				t.Fatalf("stdout is not valid JSON: %v\nGot: %s", err, outStr)
			}

			errVal, ok := result["error"]
			if !ok {
				t.Fatalf("JSON output missing \"error\" key; got: %s", outStr)
			}

			errStr, ok := errVal.(string)
			if !ok {
				t.Fatalf("JSON \"error\" value is not a string; got type %T", errVal)
			}

			if !strings.Contains(errStr, tt.wantErrKey) {
				t.Errorf("JSON error %q does not contain %q", errStr, tt.wantErrKey)
			}
		})
	}
}

func TestStatusJSONErrorRoutingHappyPath(t *testing.T) {
	// When VM is found and --json is set, stdout must contain the VM JSON
	// object (not an error), and stderr must be empty.
	recentLaunch := time.Now().Add(-30 * time.Minute)
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)

	deps := &statusDeps{
		describe: &mockDescribeInstances{
			output: makeInstanceWithTime("i-rte1", "default", "alice", "running", "1.2.3.4", "m6i.xlarge", "complete", recentLaunch),
		},
		owner: "alice",
	}

	cmd := newStatusCommandWithDeps(deps)
	root := newTestRoot()
	root.AddCommand(cmd)
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs([]string{"status", "--json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := stderr.String(); got != "" {
		t.Errorf("stderr must be empty on happy path, got: %q", got)
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &result); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\nGot: %s", err, stdout.String())
	}

	if _, hasErr := result["error"]; hasErr {
		t.Errorf("happy path JSON must not contain \"error\" key; got: %s", stdout.String())
	}
	if _, hasID := result["id"]; !hasID {
		t.Errorf("happy path JSON must contain \"id\" key; got: %s", stdout.String())
	}
}

// ---------------------------------------------------------------------------
// Tests: Spinner wiring
// ---------------------------------------------------------------------------

// TestStatusSpinnerShownInHumanMode verifies that a spinner message is emitted
// during AWS VM lookup when --json is NOT set. In non-interactive mode the
// Spinner writes a timestamped line; we verify the message appears in output.
func TestStatusSpinnerShownInHumanMode(t *testing.T) {
	recentLaunch := time.Now().Add(-30 * time.Minute)
	buf := new(bytes.Buffer)

	deps := &statusDeps{
		describe: &mockDescribeInstances{
			output: makeInstanceWithTime("i-spin3", "default", "alice", "running", "1.2.3.4", "m6i.xlarge", "complete", recentLaunch),
		},
		owner:          "alice",
		versionChecker: stubVersionChecker(false, nil),
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
	// In non-interactive mode the spinner writes "[HH:MM:SS] Checking VM status..."
	if !strings.Contains(output, "Checking VM status") {
		t.Errorf("expected spinner message %q in human output, got:\n%s", "Checking VM status", output)
	}
	// VM details must also be present — spinner must have stopped before output.
	if !strings.Contains(output, "i-spin3") {
		t.Errorf("VM details missing from output:\n%s", output)
	}
}

// TestStatusSpinnerSuppressedInJSONMode verifies that no spinner messages appear
// when --json is set. JSON consumers must receive a clean JSON object.
func TestStatusSpinnerSuppressedInJSONMode(t *testing.T) {
	recentLaunch := time.Now().Add(-30 * time.Minute)
	buf := new(bytes.Buffer)

	deps := &statusDeps{
		describe: &mockDescribeInstances{
			output: makeInstanceWithTime("i-spin4", "default", "alice", "running", "1.2.3.4", "m6i.xlarge", "complete", recentLaunch),
		},
		owner:          "alice",
		versionChecker: stubVersionChecker(false, nil),
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
	// Spinner message must NOT appear in JSON mode.
	if strings.Contains(output, "Checking VM status") {
		t.Errorf("spinner message must not appear in --json mode, got:\n%s", output)
	}
	// Output must be valid JSON.
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &result); err != nil {
		t.Errorf("JSON output invalid (spinner may have polluted it): %v\nOutput: %s", err, output)
	}
}

func TestParseDiskUsagePct(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{
			name:  "normal output",
			input: "Use%\n 42%\n",
			want:  42,
		},
		{
			name:  "high usage",
			input: "Use%\n 95%\n",
			want:  95,
		},
		{
			name:  "zero usage",
			input: "Use%\n  0%\n",
			want:  0,
		},
		{
			name:  "100 percent",
			input: "Use%\n100%\n",
			want:  100,
		},
		{
			name:    "empty output",
			input:   "",
			wantErr: true,
		},
		{
			name:    "single line only",
			input:   "Use%",
			wantErr: true,
		},
		{
			name:    "garbage data",
			input:   "Use%\n abc\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDiskUsagePct(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("parseDiskUsagePct() = %d, want %d", got, tt.want)
			}
		})
	}
}

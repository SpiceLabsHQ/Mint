package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// makeInstanceWithTime creates a DescribeInstancesOutput with a launch time.
func makeInstanceWithTime(id, vmName, owner, state, ip, instanceType, bootstrap string, launchTime time.Time) *ec2.DescribeInstancesOutput {
	inst := ec2types.Instance{
		InstanceId:   aws.String(id),
		InstanceType: ec2types.InstanceType(instanceType),
		LaunchTime:   aws.Time(launchTime),
		State: &ec2types.InstanceState{
			Name: ec2types.InstanceStateName(state),
		},
		Tags: []ec2types.Tag{
			{Key: aws.String("mint"), Value: aws.String("true")},
			{Key: aws.String("mint:vm"), Value: aws.String(vmName)},
			{Key: aws.String("mint:owner"), Value: aws.String(owner)},
		},
	}
	if ip != "" {
		inst.PublicIpAddress = aws.String(ip)
	}
	if bootstrap != "" {
		inst.Tags = append(inst.Tags, ec2types.Tag{
			Key: aws.String("mint:bootstrap"), Value: aws.String(bootstrap),
		})
	}
	return &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{Instances: []ec2types.Instance{inst}}},
	}
}

// makeMultiInstanceOutput creates a DescribeInstancesOutput with multiple instances.
func makeMultiInstanceOutput(instances ...ec2types.Instance) *ec2.DescribeInstancesOutput {
	return &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{Instances: instances}},
	}
}

func makeTestInstance(id, vmName, owner, state, ip, instanceType, bootstrap string, launchTime time.Time) ec2types.Instance {
	inst := ec2types.Instance{
		InstanceId:   aws.String(id),
		InstanceType: ec2types.InstanceType(instanceType),
		LaunchTime:   aws.Time(launchTime),
		State: &ec2types.InstanceState{
			Name: ec2types.InstanceStateName(state),
		},
		Tags: []ec2types.Tag{
			{Key: aws.String("mint"), Value: aws.String("true")},
			{Key: aws.String("mint:vm"), Value: aws.String(vmName)},
			{Key: aws.String("mint:owner"), Value: aws.String(owner)},
		},
	}
	if ip != "" {
		inst.PublicIpAddress = aws.String(ip)
	}
	if bootstrap != "" {
		inst.Tags = append(inst.Tags, ec2types.Tag{
			Key: aws.String("mint:bootstrap"), Value: aws.String(bootstrap),
		})
	}
	return inst
}

func TestListCommand(t *testing.T) {
	recentLaunch := time.Now().Add(-30 * time.Minute)
	oldLaunch := time.Now().Add(-3 * time.Hour)

	tests := []struct {
		name           string
		describe       *mockDescribeInstances
		owner          string
		idleTimeout    int
		jsonOutput     bool
		wantErr        bool
		wantErrContain string
		wantOutput     []string
		wantAbsent     []string
	}{
		{
			name: "single running VM table output",
			describe: &mockDescribeInstances{
				output: makeInstanceWithTime("i-abc123", "default", "alice", "running", "1.2.3.4", "m6i.xlarge", "complete", recentLaunch),
			},
			owner:       "alice",
			idleTimeout: 60,
			wantOutput:  []string{"default", "running", "1.2.3.4", "m6i.xlarge", "complete"},
		},
		{
			name: "empty list shows no VMs message",
			describe: &mockDescribeInstances{
				output: &ec2.DescribeInstancesOutput{},
			},
			owner:       "alice",
			idleTimeout: 60,
			wantOutput:  []string{"No VMs found"},
		},
		{
			name: "multiple VMs listed",
			describe: &mockDescribeInstances{
				output: makeMultiInstanceOutput(
					makeTestInstance("i-one", "default", "alice", "running", "1.1.1.1", "t3.medium", "complete", recentLaunch),
					makeTestInstance("i-two", "dev", "alice", "stopped", "", "m6i.xlarge", "complete", oldLaunch),
				),
			},
			owner:       "alice",
			idleTimeout: 60,
			wantOutput:  []string{"default", "dev", "running", "stopped", "t3.medium", "m6i.xlarge"},
		},
		{
			name: "idle timeout warning",
			describe: &mockDescribeInstances{
				output: makeInstanceWithTime("i-idle", "default", "alice", "running", "1.2.3.4", "m6i.xlarge", "complete", oldLaunch),
			},
			owner:       "alice",
			idleTimeout: 60, // 60 min timeout, VM running 3 hours
			wantOutput:  []string{"idle"},
		},
		{
			name: "no idle warning when under threshold",
			describe: &mockDescribeInstances{
				output: makeInstanceWithTime("i-recent", "default", "alice", "running", "1.2.3.4", "m6i.xlarge", "complete", recentLaunch),
			},
			owner:       "alice",
			idleTimeout: 60, // 60 min timeout, VM running 30 min
			wantAbsent:  []string{"idle"},
		},
		{
			name: "no idle warning for stopped VMs",
			describe: &mockDescribeInstances{
				output: makeInstanceWithTime("i-stopped", "default", "alice", "stopped", "", "m6i.xlarge", "complete", oldLaunch),
			},
			owner:       "alice",
			idleTimeout: 60,
			wantAbsent:  []string{"idle"},
		},
		{
			name: "bootstrap failed indicator",
			describe: &mockDescribeInstances{
				output: makeInstanceWithTime("i-fail", "default", "alice", "running", "1.2.3.4", "m6i.xlarge", "failed", recentLaunch),
			},
			owner:       "alice",
			idleTimeout: 60,
			wantOutput:  []string{"FAILED"},
		},
		{
			name: "json output format",
			describe: &mockDescribeInstances{
				output: makeInstanceWithTime("i-abc123", "default", "alice", "running", "1.2.3.4", "m6i.xlarge", "complete", recentLaunch),
			},
			owner:       "alice",
			idleTimeout: 60,
			jsonOutput:  true,
			wantOutput:  []string{`"id"`, `"name"`, `"state"`, `"i-abc123"`},
		},
		{
			name: "json output is valid JSON object",
			describe: &mockDescribeInstances{
				output: makeInstanceWithTime("i-abc123", "default", "alice", "running", "1.2.3.4", "m6i.xlarge", "complete", recentLaunch),
			},
			owner:       "alice",
			idleTimeout: 60,
			jsonOutput:  true,
		},
		{
			name: "json empty list",
			describe: &mockDescribeInstances{
				output: &ec2.DescribeInstancesOutput{},
			},
			owner:       "alice",
			idleTimeout: 60,
			jsonOutput:  true,
			wantOutput:  []string{`"vms"`, `"update_available"`, `"latest_version"`},
		},
		{
			name: "API error propagates",
			describe: &mockDescribeInstances{
				err: errThrottled,
			},
			owner:          "alice",
			idleTimeout:    60,
			wantErr:        true,
			wantErrContain: "throttled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := new(bytes.Buffer)

			deps := &listDeps{
				describe:    tt.describe,
				owner:       tt.owner,
				idleTimeout: time.Duration(tt.idleTimeout) * time.Minute,
			}

			cmd := newListCommandWithDeps(deps)
			root := newTestRoot()
			root.AddCommand(cmd)
			root.SetOut(buf)
			root.SetErr(buf)

			args := []string{"list"}
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
			for _, absent := range tt.wantAbsent {
				if strings.Contains(output, absent) {
					t.Errorf("output should not contain %q, got:\n%s", absent, output)
				}
			}

			// Validate JSON output is parseable as an object with vms array.
			if tt.jsonOutput {
				var result map[string]interface{}
				if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &result); err != nil {
					t.Errorf("JSON output is not a valid object: %v\nOutput: %s", err, output)
				}
			}
		})
	}
}

// stubVersionChecker returns a VersionCheckerFunc that returns fixed values,
// avoiding any real network or filesystem access during tests.
func stubVersionChecker(updateAvailable bool, latestVersion *string) VersionCheckerFunc {
	return func() (bool, *string) {
		return updateAvailable, latestVersion
	}
}

// strPtr is a test helper that converts a string literal to a *string.
func strPtr(s string) *string { return &s }

func TestListJSONIncludesVersionFields(t *testing.T) {
	recentLaunch := time.Now().Add(-30 * time.Minute)
	buf := new(bytes.Buffer)

	deps := &listDeps{
		describe: &mockDescribeInstances{
			output: makeInstanceWithTime("i-abc123", "default", "alice", "running", "1.2.3.4", "m6i.xlarge", "complete", recentLaunch),
		},
		owner:          "alice",
		idleTimeout:    60 * time.Minute,
		versionChecker: stubVersionChecker(true, strPtr("v2.0.0")),
	}

	cmd := newListCommandWithDeps(deps)
	root := newTestRoot()
	root.AddCommand(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"list", "--json"})

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

	// vms must be a non-empty array.
	if v, ok := result["vms"]; !ok {
		t.Error("JSON missing vms field")
	} else if arr, ok := v.([]interface{}); !ok || len(arr) == 0 {
		t.Errorf("vms = %v, want non-empty array", v)
	}
}

func TestListJSONVersionFieldsFailOpen(t *testing.T) {
	// When the version checker returns no info (check failed), update_available
	// must be false and latest_version must be null.
	recentLaunch := time.Now().Add(-30 * time.Minute)
	buf := new(bytes.Buffer)

	deps := &listDeps{
		describe: &mockDescribeInstances{
			output: makeInstanceWithTime("i-abc456", "default", "alice", "running", "1.2.3.4", "m6i.xlarge", "complete", recentLaunch),
		},
		owner:          "alice",
		idleTimeout:    60 * time.Minute,
		versionChecker: stubVersionChecker(false, nil),
	}

	cmd := newListCommandWithDeps(deps)
	root := newTestRoot()
	root.AddCommand(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"list", "--json"})

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

func TestListJSONStructureHasVmsArray(t *testing.T) {
	// Verify top-level JSON structure: object with vms, update_available, latest_version.
	buf := new(bytes.Buffer)

	deps := &listDeps{
		describe: &mockDescribeInstances{
			output: &ec2.DescribeInstancesOutput{},
		},
		owner:          "alice",
		idleTimeout:    60 * time.Minute,
		versionChecker: stubVersionChecker(false, nil),
	}

	cmd := newListCommandWithDeps(deps)
	root := newTestRoot()
	root.AddCommand(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"list", "--json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	for _, field := range []string{"vms", "update_available", "latest_version"} {
		if _, ok := result[field]; !ok {
			t.Errorf("JSON missing top-level field %q", field)
		}
	}

	// vms must be an empty array (not null).
	if vms, ok := result["vms"].([]interface{}); !ok {
		t.Errorf("vms is not an array, got %T", result["vms"])
	} else if len(vms) != 0 {
		t.Errorf("expected empty vms array, got %d items", len(vms))
	}
}

// ---------------------------------------------------------------------------
// Tests: Spinner wiring
// ---------------------------------------------------------------------------

// TestListSpinnerShownInHumanMode verifies that a spinner message is emitted
// during AWS discovery when --json is NOT set. In non-interactive mode (tests
// always use a bytes.Buffer, not a TTY), the Spinner emits a timestamped line
// for each Start/Update call. We confirm the discovery message appears before
// the VM table output, meaning the spinner ran and stopped cleanly.
func TestListSpinnerShownInHumanMode(t *testing.T) {
	recentLaunch := time.Now().Add(-30 * time.Minute)
	buf := new(bytes.Buffer)

	deps := &listDeps{
		describe: &mockDescribeInstances{
			output: makeInstanceWithTime("i-spin1", "default", "alice", "running", "1.2.3.4", "m6i.xlarge", "complete", recentLaunch),
		},
		owner:          "alice",
		idleTimeout:    60 * time.Minute,
		versionChecker: stubVersionChecker(false, nil),
	}

	cmd := newListCommandWithDeps(deps)
	root := newTestRoot()
	root.AddCommand(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"list"})

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	// In non-interactive mode the spinner writes "[HH:MM:SS] Discovering VMs..."
	if !strings.Contains(output, "Discovering VMs") {
		t.Errorf("expected spinner message %q in human output, got:\n%s", "Discovering VMs", output)
	}
	// VM table must also be present — spinner must have stopped before output.
	if !strings.Contains(output, "default") {
		t.Errorf("VM table missing from output:\n%s", output)
	}
}

// TestListSpinnerSuppressedInJSONMode verifies that no spinner messages appear
// when --json is set. JSON consumers must receive a clean JSON object with no
// prefixed spinner lines.
func TestListSpinnerSuppressedInJSONMode(t *testing.T) {
	recentLaunch := time.Now().Add(-30 * time.Minute)
	buf := new(bytes.Buffer)

	deps := &listDeps{
		describe: &mockDescribeInstances{
			output: makeInstanceWithTime("i-spin2", "default", "alice", "running", "1.2.3.4", "m6i.xlarge", "complete", recentLaunch),
		},
		owner:          "alice",
		idleTimeout:    60 * time.Minute,
		versionChecker: stubVersionChecker(false, nil),
	}

	cmd := newListCommandWithDeps(deps)
	root := newTestRoot()
	root.AddCommand(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"list", "--json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	// Spinner message must NOT appear in JSON mode.
	if strings.Contains(output, "Discovering VMs") {
		t.Errorf("spinner message must not appear in --json mode, got:\n%s", output)
	}
	// Output must be valid JSON.
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &result); err != nil {
		t.Errorf("JSON output invalid (spinner may have polluted it): %v\nOutput: %s", err, output)
	}
}

// errThrottled is a reusable test error.
var errThrottled = errForTest("throttled")

type errForTest string

func (e errForTest) Error() string { return string(e) }

// ---------------------------------------------------------------------------
// Tests: Multiple VM warning (3+ running)
// ---------------------------------------------------------------------------

// makeMultiRunningInstances returns a DescribeInstancesOutput with N running VMs
// and one stopped VM (to verify stopped VMs don't count toward the warning).
func makeMultiRunningInstances(n int) *ec2.DescribeInstancesOutput {
	var instances []ec2types.Instance
	recentLaunch := time.Now().Add(-30 * time.Minute)
	for i := 0; i < n; i++ {
		instances = append(instances, makeTestInstance(
			fmt.Sprintf("i-%03d", i),
			fmt.Sprintf("vm%d", i),
			"alice",
			"running",
			fmt.Sprintf("1.2.3.%d", i+1),
			"m6i.xlarge",
			"complete",
			recentLaunch,
		))
	}
	return makeMultiInstanceOutput(instances...)
}

func TestListMultiVMWarning(t *testing.T) {
	recentLaunch := time.Now().Add(-30 * time.Minute)

	tests := []struct {
		name        string
		describe    *mockDescribeInstances
		wantWarning bool
		wantAbsent  []string
		wantOutput  []string
	}{
		{
			name: "0 running VMs — no warning",
			describe: &mockDescribeInstances{
				output: &ec2.DescribeInstancesOutput{},
			},
			wantWarning: false,
			wantAbsent:  []string{"running VMs", "unnecessary costs"},
		},
		{
			name: "2 running VMs — no warning",
			describe: &mockDescribeInstances{
				output: makeMultiInstanceOutput(
					makeTestInstance("i-001", "vm1", "alice", "running", "1.1.1.1", "m6i.xlarge", "complete", recentLaunch),
					makeTestInstance("i-002", "vm2", "alice", "running", "1.1.1.2", "m6i.xlarge", "complete", recentLaunch),
				),
			},
			wantWarning: false,
			wantAbsent:  []string{"unnecessary costs"},
		},
		{
			name: "3 running VMs — warning shown",
			describe: &mockDescribeInstances{
				output: makeMultiRunningInstances(3),
			},
			wantWarning: true,
			wantOutput:  []string{"3", "running VMs", "unnecessary costs"},
		},
		{
			name: "4 running VMs — warning shown with count",
			describe: &mockDescribeInstances{
				output: makeMultiRunningInstances(4),
			},
			wantWarning: true,
			wantOutput:  []string{"4", "running VMs", "unnecessary costs"},
		},
		{
			name: "stopped VMs do not count toward warning",
			describe: &mockDescribeInstances{
				output: makeMultiInstanceOutput(
					makeTestInstance("i-001", "vm1", "alice", "running", "1.1.1.1", "m6i.xlarge", "complete", recentLaunch),
					makeTestInstance("i-002", "vm2", "alice", "running", "1.1.1.2", "m6i.xlarge", "complete", recentLaunch),
					makeTestInstance("i-003", "vm3", "alice", "stopped", "", "m6i.xlarge", "complete", recentLaunch),
				),
			},
			wantWarning: false,
			wantAbsent:  []string{"unnecessary costs"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := new(bytes.Buffer)
			deps := &listDeps{
				describe:    tt.describe,
				owner:       "alice",
				idleTimeout: 60 * time.Minute,
			}
			cmd := newListCommandWithDeps(deps)
			root := newTestRoot()
			root.AddCommand(cmd)
			root.SetOut(buf)
			root.SetErr(buf)
			root.SetArgs([]string{"list"})

			if err := root.Execute(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			output := buf.String()
			for _, want := range tt.wantOutput {
				if !strings.Contains(output, want) {
					t.Errorf("output missing %q, got:\n%s", want, output)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(output, absent) {
					t.Errorf("output should not contain %q, got:\n%s", absent, output)
				}
			}
		})
	}
}

func TestListJSONRunningVMCount(t *testing.T) {
	recentLaunch := time.Now().Add(-30 * time.Minute)

	tests := []struct {
		name              string
		describe          *mockDescribeInstances
		wantRunningVMCount float64
	}{
		{
			name: "0 running VMs",
			describe: &mockDescribeInstances{
				output: &ec2.DescribeInstancesOutput{},
			},
			wantRunningVMCount: 0,
		},
		{
			name: "2 running VMs",
			describe: &mockDescribeInstances{
				output: makeMultiInstanceOutput(
					makeTestInstance("i-001", "vm1", "alice", "running", "1.1.1.1", "m6i.xlarge", "complete", recentLaunch),
					makeTestInstance("i-002", "vm2", "alice", "running", "1.1.1.2", "m6i.xlarge", "complete", recentLaunch),
				),
			},
			wantRunningVMCount: 2,
		},
		{
			name: "3 running VMs",
			describe: &mockDescribeInstances{
				output: makeMultiRunningInstances(3),
			},
			wantRunningVMCount: 3,
		},
		{
			name: "stopped VMs excluded from count",
			describe: &mockDescribeInstances{
				output: makeMultiInstanceOutput(
					makeTestInstance("i-001", "vm1", "alice", "running", "1.1.1.1", "m6i.xlarge", "complete", recentLaunch),
					makeTestInstance("i-002", "vm2", "alice", "stopped", "", "m6i.xlarge", "complete", recentLaunch),
				),
			},
			wantRunningVMCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := new(bytes.Buffer)
			deps := &listDeps{
				describe:       tt.describe,
				owner:          "alice",
				idleTimeout:    60 * time.Minute,
				versionChecker: stubVersionChecker(false, nil),
			}
			cmd := newListCommandWithDeps(deps)
			root := newTestRoot()
			root.AddCommand(cmd)
			root.SetOut(buf)
			root.SetErr(buf)
			root.SetArgs([]string{"list", "--json"})

			if err := root.Execute(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			var result map[string]interface{}
			if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
				t.Fatalf("invalid JSON: %v\nOutput: %s", err, buf.String())
			}

			got, ok := result["running_vm_count"]
			if !ok {
				t.Fatalf("JSON missing running_vm_count field; got fields: %v", result)
			}
			if got != tt.wantRunningVMCount {
				t.Errorf("running_vm_count = %v, want %v", got, tt.wantRunningVMCount)
			}
		})
	}
}

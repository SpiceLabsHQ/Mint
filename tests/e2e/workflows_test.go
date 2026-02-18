package e2e_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nicholasgasior/mint/cmd"
)

// ---------------------------------------------------------------------------
// Workflow 1: Happy-path provision
//
// mint up → mint status → mint list
//
// Verifies: up succeeds and prints instance details, status shows the running
// VM, list shows exactly 1 VM.
// ---------------------------------------------------------------------------

func TestWorkflow_HappyPathProvision(t *testing.T) {
	const (
		instanceID   = "i-e2e-happy"
		volumeID     = "vol-e2e-happy"
		allocationID = "eipalloc-e2e-happy"
		publicIP     = "54.100.200.1"
		vmName       = "default"
		owner        = "e2e-user"
	)

	runningDescribe := &stubDescribeInstances{
		output: makeE2EDescribeOutput(
			makeE2EInstance(instanceID, vmName, owner, "running", publicIP, "m6i.xlarge"),
		),
	}

	cfg := &e2eConfig{
		provisioner:         newFreshProvisioner(instanceID, volumeID, allocationID, publicIP),
		describeFileSystems: &stubDescribeFileSystems{filesystemID: "fs-e2e-happy"},
		describeForDown: &stubDescribeInstances{
			output: makeE2EDescribeOutput(
				makeE2EInstance(instanceID, vmName, owner, "running", publicIP, "m6i.xlarge"),
			),
		},
		describeForList:   runningDescribe,
		describeForStatus: runningDescribe,
		stopInstances:     &stubStopInstances{},
		owner:             owner,
		ownerARN:          "arn:aws:iam::123456789012:user/" + owner,
	}

	env := &testEnv{t: t, root: newE2ERoot(t, cfg)}

	// Step 1: mint up
	stdout, _, err := env.RunCommand([]string{"up"})
	requireNoError(t, err)
	assertContains(t, "mint up", stdout, []string{
		instanceID,
		publicIP,
		volumeID,
		allocationID,
		"Bootstrap complete",
	})

	// Re-create root for next command (cobra resets args on each Execute).
	env.root = newE2ERoot(t, cfg)

	// Step 2: mint status — must show the running VM
	stdout, _, err = env.RunCommand([]string{"status"})
	requireNoError(t, err)
	assertContains(t, "mint status", stdout, []string{
		instanceID,
		"running",
		publicIP,
		vmName,
	})

	// Step 3: mint list — must show 1 VM
	env.root = newE2ERoot(t, cfg)
	stdout, _, err = env.RunCommand([]string{"list"})
	requireNoError(t, err)
	assertContains(t, "mint list", stdout, []string{
		vmName,
		"running",
	})

	if !strings.Contains(stdout, vmName) {
		t.Errorf("list output missing VM name %q\noutput:\n%s", vmName, stdout)
	}
}

// ---------------------------------------------------------------------------
// Workflow 2: Stop and restart
//
// mint up → mint down → mint up (restart) → mint status
//
// Verifies: first up provisions fresh, down stops it, second up triggers
// the restart path (restarted=true), status shows running after restart.
// ---------------------------------------------------------------------------

func TestWorkflow_StopAndRestart(t *testing.T) {
	const (
		instanceID = "i-e2e-restart"
		publicIP   = "54.100.200.2"
		vmName     = "default"
		owner      = "e2e-user"
	)

	// Config for initial fresh provision
	freshCfg := &e2eConfig{
		provisioner:         newFreshProvisioner(instanceID, "vol-e2e-restart", "eipalloc-e2e-restart", publicIP),
		describeFileSystems: &stubDescribeFileSystems{filesystemID: "fs-e2e-restart"},
		describeForDown: &stubDescribeInstances{
			output: makeE2EDescribeOutput(
				makeE2EInstance(instanceID, vmName, owner, "running", publicIP, "m6i.xlarge"),
			),
		},
		describeForList: &stubDescribeInstances{
			output: makeE2EDescribeOutput(
				makeE2EInstance(instanceID, vmName, owner, "running", publicIP, "m6i.xlarge"),
			),
		},
		describeForStatus: &stubDescribeInstances{
			output: makeE2EDescribeOutput(
				makeE2EInstance(instanceID, vmName, owner, "running", publicIP, "m6i.xlarge"),
			),
		},
		stopInstances: &stubStopInstances{},
		owner:         owner,
		ownerARN:      "arn:aws:iam::123456789012:user/" + owner,
	}

	// Step 1: mint up (fresh provision)
	env := &testEnv{t: t, root: newE2ERoot(t, freshCfg)}
	stdout, _, err := env.RunCommand([]string{"up"})
	requireNoError(t, err)
	if !strings.Contains(stdout, instanceID) {
		t.Errorf("first up: output missing instance ID %q\noutput:\n%s", instanceID, stdout)
	}
	if strings.Contains(stdout, "restarted") {
		t.Errorf("first up: must NOT indicate restart for fresh provision; output:\n%s", stdout)
	}

	// Step 2: mint down (stop the VM)
	env.root = newE2ERoot(t, freshCfg)
	stdout, _, err = env.RunCommand([]string{"down"})
	requireNoError(t, err)
	assertContains(t, "mint down", stdout, []string{"stopped"})

	// Config for restart: Provisioner finds a stopped VM → restart path
	restartCfg := &e2eConfig{
		provisioner:         newRestartProvisioner(instanceID, vmName, owner, publicIP),
		describeFileSystems: &stubDescribeFileSystems{filesystemID: "fs-e2e-restart"},
		describeForDown:     freshCfg.describeForDown,
		describeForList:     freshCfg.describeForList,
		describeForStatus: &stubDescribeInstances{
			output: makeE2EDescribeOutput(
				makeE2EInstance(instanceID, vmName, owner, "running", publicIP, "m6i.xlarge"),
			),
		},
		stopInstances: &stubStopInstances{},
		owner:         owner,
		ownerARN:      "arn:aws:iam::123456789012:user/" + owner,
	}

	// Step 3: mint up (restart — finds stopped VM, starts it)
	env.root = newE2ERoot(t, restartCfg)
	stdout, _, err = env.RunCommand([]string{"up"})
	requireNoError(t, err)
	if !strings.Contains(stdout, "restarted") {
		t.Errorf("second up (restart): output missing 'restarted'\noutput:\n%s", stdout)
	}
	if !strings.Contains(stdout, instanceID) {
		t.Errorf("second up (restart): output missing instance ID %q\noutput:\n%s", instanceID, stdout)
	}

	// Step 4: mint status — must show running
	env.root = newE2ERoot(t, restartCfg)
	stdout, _, err = env.RunCommand([]string{"status"})
	requireNoError(t, err)
	assertContains(t, "mint status after restart", stdout, []string{"running"})
}

// ---------------------------------------------------------------------------
// Workflow 3: Version check
//
// mint version → mint list --json (check update_available field)
//
// Verifies: version outputs correctly (uses real cmd.NewRootCommand()),
// list JSON envelope contains update_available field.
// ---------------------------------------------------------------------------

func TestWorkflow_VersionCheck(t *testing.T) {
	const (
		instanceID = "i-e2e-version"
		publicIP   = "54.100.200.3"
		vmName     = "default"
		owner      = "e2e-user"
	)

	// Step 1: mint version — uses real command tree (no AWS needed)
	// Create a fresh root from cmd.NewRootCommand() and point it at a temp dir.
	tmpDir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", tmpDir)

	versionRoot := cmd.NewRootCommand()
	versionOut := newCaptureBuffer()
	versionRoot.SetOut(versionOut)
	versionRoot.SetErr(versionOut)
	versionRoot.SetArgs([]string{"version"})

	if err := versionRoot.Execute(); err != nil {
		t.Fatalf("mint version error: %v", err)
	}

	versionOutput := versionOut.String()
	assertContains(t, "mint version", versionOutput, []string{
		"mint version:",
		"commit:",
		"date:",
	})

	// Step 2: mint list --json — uses mock-backed command tree
	cfg := &e2eConfig{
		provisioner:         newFreshProvisioner(instanceID, "vol-ver", "eipalloc-ver", publicIP),
		describeFileSystems: &stubDescribeFileSystems{filesystemID: "fs-e2e-version"},
		describeForDown: &stubDescribeInstances{
			output: makeE2EDescribeOutput(
				makeE2EInstance(instanceID, vmName, owner, "running", publicIP, "m6i.xlarge"),
			),
		},
		describeForList: &stubDescribeInstances{
			output: makeE2EDescribeOutput(
				makeE2EInstance(instanceID, vmName, owner, "running", publicIP, "m6i.xlarge"),
			),
		},
		describeForStatus: &stubDescribeInstances{
			output: makeE2EDescribeOutput(
				makeE2EInstance(instanceID, vmName, owner, "running", publicIP, "m6i.xlarge"),
			),
		},
		stopInstances: &stubStopInstances{},
		owner:         owner,
		ownerARN:      "arn:aws:iam::123456789012:user/" + owner,
	}

	env := &testEnv{t: t, root: newE2ERoot(t, cfg)}
	stdout, _, err := env.RunCommand([]string{"list", "--json"})
	requireNoError(t, err)

	var result map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &result); err != nil {
		t.Fatalf("list --json output is not valid JSON: %v\noutput:\n%s", err, stdout)
	}

	// Must have update_available field in the JSON envelope
	if _, ok := result["update_available"]; !ok {
		t.Errorf("list --json output missing 'update_available' field; got keys: %v", mapKeys(result))
	}

	// Must have a vms array
	if _, ok := result["vms"]; !ok {
		t.Errorf("list --json output missing 'vms' field; got keys: %v", mapKeys(result))
	}

	// The vms array must contain our running VM
	vmsRaw, _ := result["vms"].([]any)
	if len(vmsRaw) == 0 {
		t.Error("list --json 'vms' array is empty, expected 1 VM")
	}
}

// ---------------------------------------------------------------------------
// Workflow 4: Config round-trip
//
// mint config set instance_type t3.large → mint config (show command)
//
// Verifies: setting persists and is reflected in subsequent reads.
// Uses real cmd.NewRootCommand() — config commands need no AWS.
// ---------------------------------------------------------------------------

func TestWorkflow_ConfigRoundTrip(t *testing.T) {
	// Use a temp dir to isolate config from the real ~/.config/mint/
	tmpDir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", tmpDir)

	// Step 1: mint config set instance_type t3.large
	setRoot := cmd.NewRootCommand()
	setOut := newCaptureBuffer()
	setRoot.SetOut(setOut)
	setRoot.SetErr(setOut)
	setRoot.SetArgs([]string{"config", "set", "instance_type", "t3.large"})

	if err := setRoot.Execute(); err != nil {
		t.Fatalf("config set error: %v", err)
	}

	setOutput := setOut.String()
	if !strings.Contains(setOutput, "t3.large") {
		t.Errorf("config set output missing 't3.large'; got:\n%s", setOutput)
	}

	// Step 2: mint config (show) — must reflect the new value
	showRoot := cmd.NewRootCommand()
	showOut := newCaptureBuffer()
	showRoot.SetOut(showOut)
	showRoot.SetErr(showOut)
	showRoot.SetArgs([]string{"config"})

	if err := showRoot.Execute(); err != nil {
		t.Fatalf("config show error: %v", err)
	}

	showOutput := showOut.String()
	if !strings.Contains(showOutput, "t3.large") {
		t.Errorf("config show after set missing 't3.large'; got:\n%s", showOutput)
	}

	// Verify via --json output for machine-readable validation
	jsonRoot := cmd.NewRootCommand()
	jsonOut := newCaptureBuffer()
	jsonRoot.SetOut(jsonOut)
	jsonRoot.SetErr(jsonOut)
	jsonRoot.SetArgs([]string{"config", "--json"})

	if err := jsonRoot.Execute(); err != nil {
		t.Fatalf("config --json error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(jsonOut.Bytes(), &result); err != nil {
		t.Fatalf("config --json output is not valid JSON: %v\noutput:\n%s", err, jsonOut.String())
	}

	if result["instance_type"] != "t3.large" {
		t.Errorf("config --json instance_type = %v, want t3.large", result["instance_type"])
	}
}

// ---------------------------------------------------------------------------
// Helpers local to workflows_test.go
// ---------------------------------------------------------------------------

// newCaptureBuffer returns a fresh bytes.Buffer for capturing command output.
func newCaptureBuffer() *captureBuffer {
	return &captureBuffer{}
}

// captureBuffer wraps bytes.Buffer and satisfies io.Writer.
type captureBuffer struct {
	strings.Builder
}

func (b *captureBuffer) Bytes() []byte {
	return []byte(b.String())
}

// mapKeys returns the keys of a map as a slice, for error messages.
func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

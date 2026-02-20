package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2instanceconnect"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	mintaws "github.com/nicholasgasior/mint/internal/aws"
	"github.com/nicholasgasior/mint/internal/cli"
	"github.com/nicholasgasior/mint/internal/identity"
	"github.com/nicholasgasior/mint/internal/sshconfig"
	"github.com/spf13/cobra"
)

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

type mockDoctorSTS struct {
	output *sts.GetCallerIdentityOutput
	err    error
}

func (m *mockDoctorSTS) GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	return m.output, m.err
}

type mockDoctorDescribeAddresses struct {
	output *ec2.DescribeAddressesOutput
	err    error
}

func (m *mockDoctorDescribeAddresses) DescribeAddresses(ctx context.Context, params *ec2.DescribeAddressesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeAddressesOutput, error) {
	return m.output, m.err
}

// mockDoctorDescribeInstances implements mintaws.DescribeInstancesAPI for
// doctor VM checks.
type mockDoctorDescribeInstances struct {
	output *ec2.DescribeInstancesOutput
	err    error
}

func (m *mockDoctorDescribeInstances) DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return m.output, m.err
}

// mockDoctorSendSSHPublicKey implements mintaws.SendSSHPublicKeyAPI for
// doctor VM checks.
type mockDoctorSendSSHPublicKey struct {
	output *ec2instanceconnect.SendSSHPublicKeyOutput
	err    error
}

func (m *mockDoctorSendSSHPublicKey) SendSSHPublicKey(ctx context.Context, params *ec2instanceconnect.SendSSHPublicKeyInput, optFns ...func(*ec2instanceconnect.Options)) (*ec2instanceconnect.SendSSHPublicKeyOutput, error) {
	return m.output, m.err
}

// mockDoctorRemoteRunner records remote commands and returns configured
// output per command. It matches on the first element of the command slice.
type mockDoctorRemoteRunner struct {
	// responses maps a command keyword to its output/error.
	responses map[string]mockRemoteResponse
	// calls records each invocation.
	calls []mockRemoteCall
}

type mockRemoteResponse struct {
	output []byte
	err    error
}

type mockRemoteCall struct {
	instanceID string
	command    []string
}

func (m *mockDoctorRemoteRunner) run(
	ctx context.Context,
	sendKey mintaws.SendSSHPublicKeyAPI,
	instanceID string,
	az string,
	host string,
	port int,
	user string,
	command []string,
) ([]byte, error) {
	m.calls = append(m.calls, mockRemoteCall{instanceID: instanceID, command: command})

	if len(command) > 0 {
		// Try matching the full first command element.
		key := command[0]
		if resp, ok := m.responses[key]; ok {
			return resp.output, resp.err
		}
	}

	return nil, fmt.Errorf("unexpected command: %v", command)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newDoctorTestRoot(sub *cobra.Command) *cobra.Command {
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
	root.AddCommand(sub)
	return root
}

func happySTS() *mockDoctorSTS {
	return &mockDoctorSTS{
		output: &sts.GetCallerIdentityOutput{
			Arn:     aws.String("arn:aws:iam::123456789012:user/alice"),
			Account: aws.String("123456789012"),
		},
	}
}

func happyDescribeAddresses(count int) *mockDoctorDescribeAddresses {
	addrs := make([]ec2types.Address, count)
	for i := range addrs {
		addrs[i] = ec2types.Address{AllocationId: aws.String(fmt.Sprintf("eipalloc-%d", i))}
	}
	return &mockDoctorDescribeAddresses{
		output: &ec2.DescribeAddressesOutput{Addresses: addrs},
	}
}

// writeValidConfig writes a minimal valid TOML config to the given dir.
func writeValidConfig(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	content := `region = "us-west-2"
instance_type = "m6i.xlarge"
volume_size_gb = 50
idle_timeout_minutes = 60
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeSSHConfigWithBlock writes a valid managed block to ~/.ssh/config in the test dir.
func writeSSHConfigWithBlock(t *testing.T, sshDir, vmName string) {
	t.Helper()
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	block := sshconfig.GenerateBlock(vmName, "1.2.3.4", "ec2-user", 2222, "i-abc123", "us-west-2a")
	if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte(block), 0o600); err != nil {
		t.Fatal(err)
	}
}

func newHappyDoctorDeps(t *testing.T) *doctorDeps {
	t.Helper()
	configDir := t.TempDir()
	writeValidConfig(t, configDir)
	sshDir := filepath.Join(t.TempDir(), ".ssh")
	writeSSHConfigWithBlock(t, sshDir, "default")

	return &doctorDeps{
		identityResolver:  identity.NewResolver(happySTS()),
		describeAddresses: happyDescribeAddresses(2),
		configDir:         configDir,
		sshConfigPath:     filepath.Join(sshDir, "config"),
		owner:             "alice",
	}
}

// makeDoctorInstance creates a DescribeInstancesOutput for doctor VM checks.
func makeDoctorInstance(id, vmName, owner, state, ip string, extraTags ...ec2types.Tag) *ec2.DescribeInstancesOutput {
	inst := ec2types.Instance{
		InstanceId:   aws.String(id),
		InstanceType: ec2types.InstanceTypeM6iXlarge,
		LaunchTime:   aws.Time(time.Now().Add(-1 * time.Hour)),
		State: &ec2types.InstanceState{
			Name: ec2types.InstanceStateName(state),
		},
		Placement: &ec2types.Placement{
			AvailabilityZone: aws.String("us-west-2a"),
		},
		Tags: []ec2types.Tag{
			{Key: aws.String("mint"), Value: aws.String("true")},
			{Key: aws.String("mint:vm"), Value: aws.String(vmName)},
			{Key: aws.String("mint:owner"), Value: aws.String(owner)},
			{Key: aws.String("mint:bootstrap"), Value: aws.String("complete")},
		},
	}
	if ip != "" {
		inst.PublicIpAddress = aws.String(ip)
	}
	inst.Tags = append(inst.Tags, extraTags...)
	return &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{Instances: []ec2types.Instance{inst}}},
	}
}

// happyRemoteRunner returns a mock runner where all component checks succeed.
func happyRemoteRunner() *mockDoctorRemoteRunner {
	return &mockDoctorRemoteRunner{
		responses: map[string]mockRemoteResponse{
			"df":           {output: []byte("Use%\n 42%\n")},
			"docker":       {output: []byte("Docker version 24.0.7\n")},
			"devcontainer": {output: []byte("0.52.1\n")},
			"tmux":         {output: []byte("tmux 3.3a\n")},
			"mosh-server":  {output: []byte("mosh 1.4.0\n")},
		},
	}
}

// newHappyDoctorDepsWithVM returns doctor deps that include VM discovery
// and SSH deps for VM health checks.
func newHappyDoctorDepsWithVM(t *testing.T) (*doctorDeps, *mockDoctorRemoteRunner) {
	t.Helper()
	deps := newHappyDoctorDeps(t)
	runner := happyRemoteRunner()
	deps.describe = &mockDoctorDescribeInstances{
		output: makeDoctorInstance("i-vm1", "default", "alice", "running", "1.2.3.4",
			ec2types.Tag{Key: aws.String("mint:health"), Value: aws.String("healthy")},
		),
	}
	deps.sendKey = &mockDoctorSendSSHPublicKey{
		output: &ec2instanceconnect.SendSSHPublicKeyOutput{},
	}
	deps.remoteRun = runner.run
	return deps, runner
}

// ---------------------------------------------------------------------------
// Local check tests (existing, preserved)
// ---------------------------------------------------------------------------

func TestDoctorAllPass(t *testing.T) {
	deps := newHappyDoctorDeps(t)
	buf := new(bytes.Buffer)
	cmd := newDoctorCommandWithDeps(deps)
	root := newDoctorTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"doctor"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	// Should contain PASS for each check
	if !strings.Contains(output, "[PASS]") {
		t.Error("expected [PASS] labels in output")
	}
	// Should have no FAIL
	if strings.Contains(output, "[FAIL]") {
		t.Errorf("expected no [FAIL] labels, got: %s", output)
	}
}

func TestDoctorCredentialCheckFail(t *testing.T) {
	deps := newHappyDoctorDeps(t)
	deps.identityResolver = identity.NewResolver(&mockDoctorSTS{
		err: fmt.Errorf("expired credentials"),
	})

	buf := new(bytes.Buffer)
	cmd := newDoctorCommandWithDeps(deps)
	root := newDoctorTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"doctor"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error from failed credential check")
	}

	output := buf.String()
	if !strings.Contains(output, "[FAIL]") {
		t.Errorf("expected [FAIL] label for credentials, got: %s", output)
	}
}

func TestDoctorCredentialCheckPass(t *testing.T) {
	deps := newHappyDoctorDeps(t)
	buf := new(bytes.Buffer)
	cmd := newDoctorCommandWithDeps(deps)
	root := newDoctorTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"doctor"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "[PASS]") || !strings.Contains(output, "AWS credentials") {
		t.Errorf("expected [PASS] AWS credentials, got: %s", output)
	}
}

func TestDoctorRegionNotSet(t *testing.T) {
	deps := newHappyDoctorDeps(t)
	// Overwrite config with no region
	dir := deps.configDir
	content := `instance_type = "m6i.xlarge"
volume_size_gb = 50
idle_timeout_minutes = 60
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	buf := new(bytes.Buffer)
	cmd := newDoctorCommandWithDeps(deps)
	root := newDoctorTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"doctor"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error from missing region")
	}

	output := buf.String()
	if !strings.Contains(output, "[FAIL]") || !strings.Contains(output, "region") {
		t.Errorf("expected [FAIL] region check, got: %s", output)
	}
}

func TestDoctorRegionInvalidFormat(t *testing.T) {
	deps := newHappyDoctorDeps(t)
	dir := deps.configDir
	content := `region = "invalid"
volume_size_gb = 50
idle_timeout_minutes = 60
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	buf := new(bytes.Buffer)
	cmd := newDoctorCommandWithDeps(deps)
	root := newDoctorTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"doctor"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error from invalid region format")
	}

	output := buf.String()
	if !strings.Contains(output, "[FAIL]") {
		t.Errorf("expected [FAIL] for invalid region, got: %s", output)
	}
}

func TestDoctorVolumeTooSmall(t *testing.T) {
	deps := newHappyDoctorDeps(t)
	dir := deps.configDir
	content := `region = "us-west-2"
volume_size_gb = 10
idle_timeout_minutes = 60
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	buf := new(bytes.Buffer)
	cmd := newDoctorCommandWithDeps(deps)
	root := newDoctorTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"doctor"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error from small volume_size_gb")
	}

	output := buf.String()
	if !strings.Contains(output, "[FAIL]") || !strings.Contains(output, "volume_size_gb") {
		t.Errorf("expected [FAIL] volume_size_gb, got: %s", output)
	}
}

func TestDoctorIdleTimeoutTooLow(t *testing.T) {
	deps := newHappyDoctorDeps(t)
	dir := deps.configDir
	content := `region = "us-west-2"
volume_size_gb = 50
idle_timeout_minutes = 5
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	buf := new(bytes.Buffer)
	cmd := newDoctorCommandWithDeps(deps)
	root := newDoctorTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"doctor"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error from low idle_timeout_minutes")
	}

	output := buf.String()
	if !strings.Contains(output, "[FAIL]") || !strings.Contains(output, "idle_timeout") {
		t.Errorf("expected [FAIL] idle_timeout, got: %s", output)
	}
}

func TestDoctorSSHConfigMissing(t *testing.T) {
	deps := newHappyDoctorDeps(t)
	deps.sshConfigPath = "/nonexistent/path/.ssh/config"

	buf := new(bytes.Buffer)
	cmd := newDoctorCommandWithDeps(deps)
	root := newDoctorTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"doctor"})

	err := root.Execute()
	// SSH config missing is a warning, not a failure
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "[WARN]") {
		t.Errorf("expected [WARN] for missing SSH config block, got: %s", output)
	}
}

func TestDoctorSSHConfigPresent(t *testing.T) {
	deps := newHappyDoctorDeps(t)

	buf := new(bytes.Buffer)
	cmd := newDoctorCommandWithDeps(deps)
	root := newDoctorTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"doctor"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "[PASS]") || !strings.Contains(output, "SSH config") {
		t.Errorf("expected [PASS] SSH config, got: %s", output)
	}
}

func TestDoctorEIPQuotaOK(t *testing.T) {
	deps := newHappyDoctorDeps(t)
	deps.describeAddresses = happyDescribeAddresses(2) // 2 of 5, plenty of headroom

	buf := new(bytes.Buffer)
	cmd := newDoctorCommandWithDeps(deps)
	root := newDoctorTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"doctor"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "[PASS]") || !strings.Contains(output, "EIP") {
		t.Errorf("expected [PASS] EIP quota, got: %s", output)
	}
}

func TestDoctorEIPQuotaWarn(t *testing.T) {
	deps := newHappyDoctorDeps(t)
	deps.describeAddresses = happyDescribeAddresses(4) // 4 of 5 = warn threshold

	buf := new(bytes.Buffer)
	cmd := newDoctorCommandWithDeps(deps)
	root := newDoctorTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"doctor"})

	err := root.Execute()
	// EIP quota warning is not a failure
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "[WARN]") || !strings.Contains(output, "EIP") {
		t.Errorf("expected [WARN] EIP quota, got: %s", output)
	}
}

func TestDoctorEIPQuotaFull(t *testing.T) {
	deps := newHappyDoctorDeps(t)
	deps.describeAddresses = happyDescribeAddresses(5) // 5 of 5 = at limit

	buf := new(bytes.Buffer)
	cmd := newDoctorCommandWithDeps(deps)
	root := newDoctorTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"doctor"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "[WARN]") || !strings.Contains(output, "EIP") {
		t.Errorf("expected [WARN] EIP quota at limit, got: %s", output)
	}
}

func TestDoctorEIPDescribeError(t *testing.T) {
	deps := newHappyDoctorDeps(t)
	deps.describeAddresses = &mockDoctorDescribeAddresses{
		err: fmt.Errorf("access denied"),
	}

	buf := new(bytes.Buffer)
	cmd := newDoctorCommandWithDeps(deps)
	root := newDoctorTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"doctor"})

	err := root.Execute()
	// API error on EIP is a warning, not hard failure
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "[WARN]") || !strings.Contains(output, "EIP") {
		t.Errorf("expected [WARN] for EIP API error, got: %s", output)
	}
}

func TestDoctorOutputFormat(t *testing.T) {
	deps := newHappyDoctorDeps(t)

	buf := new(bytes.Buffer)
	cmd := newDoctorCommandWithDeps(deps)
	root := newDoctorTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"doctor"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	// Check that output uses bracket labels, not emojis
	for _, label := range []string{"[PASS]", "[WARN]", "[FAIL]"} {
		_ = label // just confirming our format constants
	}
	// Should not contain emoji
	if strings.ContainsAny(output, "\U0001F44D\u2705\u274C\u26A0") {
		t.Error("output contains emoji characters; should use [PASS]/[FAIL]/[WARN] labels")
	}
}

func TestDoctorReturnsErrorOnFail(t *testing.T) {
	// If any check FAILs, the command should return an error
	deps := newHappyDoctorDeps(t)
	deps.identityResolver = identity.NewResolver(&mockDoctorSTS{
		err: fmt.Errorf("no credentials"),
	})

	buf := new(bytes.Buffer)
	cmd := newDoctorCommandWithDeps(deps)
	root := newDoctorTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"doctor"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected non-nil error when a check fails")
	}
}

func TestDoctorNoErrorOnWarnOnly(t *testing.T) {
	// If only WARNs (no FAILs), the command should succeed
	deps := newHappyDoctorDeps(t)
	deps.describeAddresses = happyDescribeAddresses(4) // warn
	deps.sshConfigPath = "/nonexistent/.ssh/config"    // warn

	buf := new(bytes.Buffer)
	cmd := newDoctorCommandWithDeps(deps)
	root := newDoctorTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"doctor"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("expected no error with only warnings, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// VM-specific check tests
// ---------------------------------------------------------------------------

func TestDoctorVMHealthy(t *testing.T) {
	deps, _ := newHappyDoctorDepsWithVM(t)

	buf := new(bytes.Buffer)
	cmd := newDoctorCommandWithDeps(deps)
	root := newDoctorTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"doctor"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	// VM health check should pass
	if !strings.Contains(output, "vm/default/health") || !strings.Contains(output, "healthy") {
		t.Errorf("expected vm/default/health PASS with healthy, got: %s", output)
	}
	// Disk usage should be reported
	if !strings.Contains(output, "vm/default/disk") || !strings.Contains(output, "42%") {
		t.Errorf("expected vm/default/disk with 42%%, got: %s", output)
	}
	// Component checks should pass
	for _, comp := range []string{"docker", "devcontainer", "tmux", "mosh-server"} {
		if !strings.Contains(output, "vm/default/"+comp) {
			t.Errorf("expected vm/default/%s check in output, got: %s", comp, output)
		}
	}
}

func TestDoctorVMDriftDetected(t *testing.T) {
	deps, _ := newHappyDoctorDepsWithVM(t)
	// Set health tag to drift-detected.
	deps.describe = &mockDoctorDescribeInstances{
		output: makeDoctorInstance("i-vm1", "default", "alice", "running", "1.2.3.4",
			ec2types.Tag{Key: aws.String("mint:health"), Value: aws.String("drift-detected")},
		),
	}

	buf := new(bytes.Buffer)
	cmd := newDoctorCommandWithDeps(deps)
	root := newDoctorTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"doctor"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "[WARN]") || !strings.Contains(output, "drift-detected") {
		t.Errorf("expected WARN drift-detected, got: %s", output)
	}
}

func TestDoctorVMHealthTagMissing(t *testing.T) {
	deps, _ := newHappyDoctorDepsWithVM(t)
	// No health tag.
	deps.describe = &mockDoctorDescribeInstances{
		output: makeDoctorInstance("i-vm1", "default", "alice", "running", "1.2.3.4"),
	}

	buf := new(bytes.Buffer)
	cmd := newDoctorCommandWithDeps(deps)
	root := newDoctorTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"doctor"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "[WARN]") || !strings.Contains(output, "health tag missing") {
		t.Errorf("expected WARN for missing health tag, got: %s", output)
	}
}

func TestDoctorVMNotRunning(t *testing.T) {
	deps, _ := newHappyDoctorDepsWithVM(t)
	// VM is stopped.
	deps.describe = &mockDoctorDescribeInstances{
		output: makeDoctorInstance("i-vm1", "default", "alice", "stopped", "",
			ec2types.Tag{Key: aws.String("mint:health"), Value: aws.String("healthy")},
		),
	}

	buf := new(bytes.Buffer)
	cmd := newDoctorCommandWithDeps(deps)
	root := newDoctorTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"doctor"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	// Should warn about non-running VM, not attempt SSH checks.
	if !strings.Contains(output, "[WARN]") || !strings.Contains(output, "stopped") {
		t.Errorf("expected WARN for stopped VM, got: %s", output)
	}
	// Should NOT contain SSH-based checks.
	if strings.Contains(output, "vm/default/disk") {
		t.Error("should not run disk check on stopped VM")
	}
}

func TestDoctorVMNoVMs(t *testing.T) {
	deps := newHappyDoctorDeps(t)
	// Describe returns no VMs.
	deps.describe = &mockDoctorDescribeInstances{
		output: &ec2.DescribeInstancesOutput{},
	}

	buf := new(bytes.Buffer)
	cmd := newDoctorCommandWithDeps(deps)
	root := newDoctorTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"doctor"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	// Should not contain VM checks (no VMs found).
	if strings.Contains(output, "vm/") {
		t.Errorf("expected no VM checks, got: %s", output)
	}
}

func TestDoctorVMComponentFail(t *testing.T) {
	deps, _ := newHappyDoctorDepsWithVM(t)
	// Override remote runner: docker fails.
	runner := &mockDoctorRemoteRunner{
		responses: map[string]mockRemoteResponse{
			"df":           {output: []byte("Use%\n 42%\n")},
			"docker":       {err: fmt.Errorf("docker: command not found")},
			"devcontainer": {output: []byte("0.52.1\n")},
			"tmux":         {output: []byte("tmux 3.3a\n")},
			"mosh-server":  {output: []byte("mosh 1.4.0\n")},
		},
	}
	deps.remoteRun = runner.run

	buf := new(bytes.Buffer)
	cmd := newDoctorCommandWithDeps(deps)
	root := newDoctorTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"doctor"})

	err := root.Execute()
	// Docker check failure should cause overall failure.
	if err == nil {
		t.Fatal("expected error from failed docker check")
	}

	output := buf.String()
	if !strings.Contains(output, "[FAIL]") || !strings.Contains(output, "docker") {
		t.Errorf("expected [FAIL] docker, got: %s", output)
	}
	// Other components should still pass.
	if !strings.Contains(output, "[PASS]") {
		t.Errorf("expected some [PASS] for other components, got: %s", output)
	}
}

func TestDoctorFixMode(t *testing.T) {
	deps, _ := newHappyDoctorDepsWithVM(t)
	// Docker fails, fix mode will attempt reinstall.
	runner := &mockDoctorRemoteRunner{
		responses: map[string]mockRemoteResponse{
			"df":           {output: []byte("Use%\n 42%\n")},
			"docker":       {err: fmt.Errorf("docker: command not found")},
			"devcontainer": {output: []byte("0.52.1\n")},
			"tmux":         {output: []byte("tmux 3.3a\n")},
			"mosh-server":  {output: []byte("mosh 1.4.0\n")},
			"sudo":         {output: []byte("installed\n")}, // fix command uses sudo
		},
	}
	deps.remoteRun = runner.run

	buf := new(bytes.Buffer)
	cmd := newDoctorCommandWithDeps(deps)
	root := newDoctorTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"doctor", "--fix"})

	err := root.Execute()
	// Still fails because the original docker check failed, but fix was attempted.
	if err == nil {
		t.Fatal("expected error (original check still FAIL)")
	}

	output := buf.String()
	// Should contain the fix result.
	if !strings.Contains(output, "docker/fix") {
		t.Errorf("expected docker/fix result in output, got: %s", output)
	}
	if !strings.Contains(output, "reinstalled successfully") {
		t.Errorf("expected 'reinstalled successfully', got: %s", output)
	}

	// Verify fix command was called (sudo is the first arg).
	fixCalled := false
	for _, call := range runner.calls {
		if len(call.command) > 0 && call.command[0] == "sudo" {
			fixCalled = true
			break
		}
	}
	if !fixCalled {
		t.Error("expected fix command (sudo) to be called")
	}
}

func TestDoctorFixModeSkipsPassingComponents(t *testing.T) {
	deps, runner := newHappyDoctorDepsWithVM(t)
	// All components pass — fix should not attempt any reinstalls.
	// Add sudo response just in case (should not be called).
	runner.responses["sudo"] = mockRemoteResponse{output: []byte("ok\n")}

	buf := new(bytes.Buffer)
	cmd := newDoctorCommandWithDeps(deps)
	root := newDoctorTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"doctor", "--fix"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	// Should NOT contain any /fix results.
	if strings.Contains(output, "/fix") {
		t.Errorf("expected no fix attempts when all checks pass, got: %s", output)
	}
}

func TestDoctorJSONOutput(t *testing.T) {
	deps, _ := newHappyDoctorDepsWithVM(t)

	buf := new(bytes.Buffer)
	cmd := newDoctorCommandWithDeps(deps)
	root := newDoctorTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"doctor", "--json"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()

	// Parse as JSON array.
	var results []checkResultJSON
	if err := json.Unmarshal([]byte(output), &results); err != nil {
		t.Fatalf("JSON output is not valid: %v\noutput: %s", err, output)
	}

	// Should contain both local and VM checks.
	if len(results) < 6 {
		t.Errorf("expected at least 6 check results (local + VM), got %d", len(results))
	}

	// Verify structure of each result.
	for _, r := range results {
		if r.Name == "" {
			t.Error("check result has empty name")
		}
		if r.Status == "" {
			t.Error("check result has empty status")
		}
		if r.Status != "PASS" && r.Status != "FAIL" && r.Status != "WARN" {
			t.Errorf("unexpected status %q for check %q", r.Status, r.Name)
		}
	}

	// Verify VM checks are present.
	hasVMHealth := false
	hasVMDisk := false
	for _, r := range results {
		if r.Name == "vm/default/health" {
			hasVMHealth = true
		}
		if r.Name == "vm/default/disk" {
			hasVMDisk = true
		}
	}
	if !hasVMHealth {
		t.Error("JSON output missing vm/default/health check")
	}
	if !hasVMDisk {
		t.Error("JSON output missing vm/default/disk check")
	}
}

func TestDoctorJSONOutputLocalOnly(t *testing.T) {
	// When no VMs are available, JSON should still include local checks.
	deps := newHappyDoctorDeps(t)

	buf := new(bytes.Buffer)
	cmd := newDoctorCommandWithDeps(deps)
	root := newDoctorTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"doctor", "--json"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var results []checkResultJSON
	if err := json.Unmarshal(buf.Bytes(), &results); err != nil {
		t.Fatalf("JSON output is not valid: %v", err)
	}

	// Should have local checks.
	if len(results) < 5 {
		t.Errorf("expected at least 5 local checks, got %d", len(results))
	}
}

func TestDoctorVMSpecificVM(t *testing.T) {
	deps, _ := newHappyDoctorDepsWithVM(t)
	// Override describe to return a VM named "dev-box".
	deps.describe = &mockDoctorDescribeInstances{
		output: makeDoctorInstance("i-dev", "dev-box", "alice", "running", "10.0.0.1",
			ec2types.Tag{Key: aws.String("mint:health"), Value: aws.String("healthy")},
		),
	}

	buf := new(bytes.Buffer)
	cmd := newDoctorCommandWithDeps(deps)
	root := newDoctorTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"doctor", "--vm", "dev-box"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	// Should check the dev-box VM.
	if !strings.Contains(output, "vm/dev-box/health") {
		t.Errorf("expected vm/dev-box/health check, got: %s", output)
	}
}

func TestDoctorMultipleVMs(t *testing.T) {
	deps, _ := newHappyDoctorDepsWithVM(t)
	// Return two running VMs.
	inst1 := ec2types.Instance{
		InstanceId:   aws.String("i-vm1"),
		InstanceType: ec2types.InstanceTypeM6iXlarge,
		LaunchTime:   aws.Time(time.Now()),
		State:        &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning},
		Placement:    &ec2types.Placement{AvailabilityZone: aws.String("us-west-2a")},
		PublicIpAddress: aws.String("1.2.3.4"),
		Tags: []ec2types.Tag{
			{Key: aws.String("mint"), Value: aws.String("true")},
			{Key: aws.String("mint:vm"), Value: aws.String("default")},
			{Key: aws.String("mint:owner"), Value: aws.String("alice")},
			{Key: aws.String("mint:health"), Value: aws.String("healthy")},
		},
	}
	inst2 := ec2types.Instance{
		InstanceId:   aws.String("i-vm2"),
		InstanceType: ec2types.InstanceTypeM6iXlarge,
		LaunchTime:   aws.Time(time.Now()),
		State:        &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning},
		Placement:    &ec2types.Placement{AvailabilityZone: aws.String("us-west-2b")},
		PublicIpAddress: aws.String("5.6.7.8"),
		Tags: []ec2types.Tag{
			{Key: aws.String("mint"), Value: aws.String("true")},
			{Key: aws.String("mint:vm"), Value: aws.String("dev-box")},
			{Key: aws.String("mint:owner"), Value: aws.String("alice")},
			{Key: aws.String("mint:health"), Value: aws.String("healthy")},
		},
	}
	deps.describe = &mockDoctorDescribeInstances{
		output: &ec2.DescribeInstancesOutput{
			Reservations: []ec2types.Reservation{{
				Instances: []ec2types.Instance{inst1, inst2},
			}},
		},
	}

	buf := new(bytes.Buffer)
	cmd := newDoctorCommandWithDeps(deps)
	root := newDoctorTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"doctor"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	// Both VMs should be checked.
	if !strings.Contains(output, "vm/default/health") {
		t.Errorf("expected vm/default/health check, got: %s", output)
	}
	if !strings.Contains(output, "vm/dev-box/health") {
		t.Errorf("expected vm/dev-box/health check, got: %s", output)
	}
}

func TestDoctorDiskUsageHighWarn(t *testing.T) {
	deps, _ := newHappyDoctorDepsWithVM(t)
	// Disk at 85%.
	runner := &mockDoctorRemoteRunner{
		responses: map[string]mockRemoteResponse{
			"df":           {output: []byte("Use%\n 85%\n")},
			"docker":       {output: []byte("Docker version 24.0.7\n")},
			"devcontainer": {output: []byte("0.52.1\n")},
			"tmux":         {output: []byte("tmux 3.3a\n")},
			"mosh-server":  {output: []byte("mosh 1.4.0\n")},
		},
	}
	deps.remoteRun = runner.run

	buf := new(bytes.Buffer)
	cmd := newDoctorCommandWithDeps(deps)
	root := newDoctorTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"doctor"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "[WARN]") || !strings.Contains(output, "85%") {
		t.Errorf("expected WARN for 85%% disk usage, got: %s", output)
	}
}

func TestDoctorDiskUsageCriticalFail(t *testing.T) {
	deps, _ := newHappyDoctorDepsWithVM(t)
	// Disk at 95% — critical.
	runner := &mockDoctorRemoteRunner{
		responses: map[string]mockRemoteResponse{
			"df":           {output: []byte("Use%\n 95%\n")},
			"docker":       {output: []byte("Docker version 24.0.7\n")},
			"devcontainer": {output: []byte("0.52.1\n")},
			"tmux":         {output: []byte("tmux 3.3a\n")},
			"mosh-server":  {output: []byte("mosh 1.4.0\n")},
		},
	}
	deps.remoteRun = runner.run

	buf := new(bytes.Buffer)
	cmd := newDoctorCommandWithDeps(deps)
	root := newDoctorTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"doctor"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error from critical disk usage")
	}

	output := buf.String()
	if !strings.Contains(output, "[FAIL]") || !strings.Contains(output, "95%") {
		t.Errorf("expected FAIL for 95%% disk usage, got: %s", output)
	}
}

func TestDoctorSSHConnectionFail(t *testing.T) {
	deps, _ := newHappyDoctorDepsWithVM(t)
	// All SSH commands fail — should warn, not hard fail.
	runner := &mockDoctorRemoteRunner{
		responses: map[string]mockRemoteResponse{},
	}
	deps.remoteRun = runner.run

	buf := new(bytes.Buffer)
	cmd := newDoctorCommandWithDeps(deps)
	root := newDoctorTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"doctor"})

	err := root.Execute()
	// Component failures are FAIL, so the command should error.
	if err == nil {
		t.Fatal("expected error from failed component checks")
	}

	output := buf.String()
	// Should contain VM checks with WARN/FAIL (SSH failed).
	if !strings.Contains(output, "vm/default") {
		t.Errorf("expected vm/default checks in output, got: %s", output)
	}
}

func TestDoctorJSONWithFailures(t *testing.T) {
	deps, _ := newHappyDoctorDepsWithVM(t)
	// Docker fails.
	runner := &mockDoctorRemoteRunner{
		responses: map[string]mockRemoteResponse{
			"df":           {output: []byte("Use%\n 42%\n")},
			"docker":       {err: fmt.Errorf("not found")},
			"devcontainer": {output: []byte("0.52.1\n")},
			"tmux":         {output: []byte("tmux 3.3a\n")},
			"mosh-server":  {output: []byte("mosh 1.4.0\n")},
		},
	}
	deps.remoteRun = runner.run

	buf := new(bytes.Buffer)
	cmd := newDoctorCommandWithDeps(deps)
	root := newDoctorTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"doctor", "--json"})

	err := root.Execute()
	// Should return error because docker check failed.
	if err == nil {
		t.Fatal("expected error from failed docker check in JSON mode")
	}

	output := buf.String()
	var results []checkResultJSON
	if err := json.Unmarshal([]byte(output), &results); err != nil {
		t.Fatalf("JSON output is not valid despite error: %v\noutput: %s", err, output)
	}

	// Find the docker check.
	dockerFound := false
	for _, r := range results {
		if r.Name == "vm/default/docker" {
			dockerFound = true
			if r.Status != "FAIL" {
				t.Errorf("expected docker status FAIL, got %s", r.Status)
			}
		}
	}
	if !dockerFound {
		t.Error("docker check not found in JSON output")
	}
}

// TestDoctorNilClientsSkipsAWSChecks verifies that doctor gracefully reports
// AWS-dependent checks as SKIP when describeAddresses is nil (no credentials).
func TestDoctorNilClientsSkipsAWSChecks(t *testing.T) {
	configDir := t.TempDir()
	writeValidConfig(t, configDir)
	sshDir := filepath.Join(t.TempDir(), ".ssh")
	writeSSHConfigWithBlock(t, sshDir, "default")

	// Simulate credentials unavailable: nil AWS clients, error identity resolver.
	deps := &doctorDeps{
		identityResolver:  &errorIdentityResolver{err: fmt.Errorf("no credentials")},
		describeAddresses: nil, // no AWS clients
		describe:          nil,
		configDir:         configDir,
		sshConfigPath:     filepath.Join(sshDir, "config"),
	}

	buf := new(bytes.Buffer)
	cmd := newDoctorCommandWithDeps(deps)
	root := newDoctorTestRoot(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"doctor"})

	// doctor should run (not crash), but return an error because credentials FAIL.
	_ = root.Execute()

	output := buf.String()

	// Credential check must report FAIL.
	if !strings.Contains(output, "[FAIL]") || !strings.Contains(output, "AWS credentials") {
		t.Errorf("expected [FAIL] AWS credentials, got: %s", output)
	}

	// EIP quota check must be SKIP, not a panic or hard error.
	if !strings.Contains(output, "[SKIP]") || !strings.Contains(output, "EIP") {
		t.Errorf("expected [SKIP] EIP quota when no AWS clients, got: %s", output)
	}

	// Local checks (config, SSH config) must still run.
	if !strings.Contains(output, "SSH config") {
		t.Errorf("expected SSH config check in output, got: %s", output)
	}
}

// TestErrorIdentityResolverReturnsError verifies the new errorIdentityResolver
// implementation used by doctor in the no-credentials path.
func TestErrorIdentityResolverReturnsError(t *testing.T) {
	sentinel := fmt.Errorf("test credential error")
	r := &errorIdentityResolver{err: sentinel}
	_, err := r.Resolve(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err != sentinel {
		t.Errorf("expected sentinel error, got: %v", err)
	}
}

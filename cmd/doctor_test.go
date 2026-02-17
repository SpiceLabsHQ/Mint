package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
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

// ---------------------------------------------------------------------------
// Tests
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
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0o600)

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
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0o600)

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
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0o600)

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
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0o600)

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

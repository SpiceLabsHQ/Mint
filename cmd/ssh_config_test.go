package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

func TestSSHConfigCommand_WritesBlock(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", configDir)

	sshDir := t.TempDir()
	sshConfigPath := filepath.Join(sshDir, "config")

	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{
		"ssh-config",
		"--yes",
		"--ssh-config-path", sshConfigPath,
		"--hostname", "1.2.3.4",
		"--instance-id", "i-abc123",
		"--az", "us-east-1a",
	})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("ssh-config error: %v", err)
	}

	// Verify SSH config file was written.
	data, err := os.ReadFile(sshConfigPath)
	if err != nil {
		t.Fatalf("read ssh config: %v", err)
	}
	content := string(data)

	expectations := []string{
		"# mint:begin default",
		"Host mint-default",
		"HostName 1.2.3.4",
		"User ubuntu",
		"Port 41122",
		"IdentityFile",
		"IdentitiesOnly yes",
		"ProxyCommand",
		"i-abc123",
		"us-east-1a",
		"# mint:end default",
		"# mint:checksum:",
	}
	for _, exp := range expectations {
		if !strings.Contains(content, exp) {
			t.Errorf("ssh config missing %q, got:\n%s", exp, content)
		}
	}

	// Must NOT contain old insecure settings.
	if strings.Contains(content, "StrictHostKeyChecking no") {
		t.Errorf("should not contain StrictHostKeyChecking no, got:\n%s", content)
	}
	if strings.Contains(content, "UserKnownHostsFile /dev/null") {
		t.Errorf("should not contain UserKnownHostsFile /dev/null, got:\n%s", content)
	}
}

func TestSSHConfigCommand_CustomVM(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", configDir)

	sshDir := t.TempDir()
	sshConfigPath := filepath.Join(sshDir, "config")

	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{
		"--vm", "myvm",
		"ssh-config",
		"--yes",
		"--ssh-config-path", sshConfigPath,
		"--hostname", "10.0.0.5",
		"--instance-id", "i-xyz789",
		"--az", "us-west-2b",
	})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("ssh-config error: %v", err)
	}

	data, _ := os.ReadFile(sshConfigPath)
	content := string(data)
	if !strings.Contains(content, "Host mint-myvm") {
		t.Errorf("missing custom VM name in config:\n%s", content)
	}
}

// TestSSHConfigCommand_PartialFlags_MissingHostname verifies that providing
// some flags but not hostname triggers an error mentioning hostname.
// (Partial explicit-mode: user started typing flags but forgot one.)
func TestSSHConfigCommand_PartialFlags_MissingHostname(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", configDir)

	buf := new(bytes.Buffer)
	deps := &sshConfigDeps{
		describe: &mockDescribeInstances{
			output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
		},
		owner: "alice",
	}
	rootCmd := newTestRoot()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.AddCommand(newSSHConfigCommandWithDeps(deps))
	rootCmd.SetArgs([]string{
		"ssh-config",
		"--yes",
		"--instance-id", "i-abc123",
		"--az", "us-east-1a",
	})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("should fail without --hostname when other flags are provided")
	}
	if !strings.Contains(err.Error(), "hostname") {
		t.Errorf("error should mention hostname: %v", err)
	}
}

// TestSSHConfigCommand_PartialFlags_MissingInstanceID verifies that providing
// hostname and az but not instance-id triggers a clear error.
func TestSSHConfigCommand_PartialFlags_MissingInstanceID(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", configDir)

	buf := new(bytes.Buffer)
	deps := &sshConfigDeps{
		describe: &mockDescribeInstances{
			output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
		},
		owner: "alice",
	}
	rootCmd := newTestRoot()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.AddCommand(newSSHConfigCommandWithDeps(deps))
	rootCmd.SetArgs([]string{
		"ssh-config",
		"--yes",
		"--hostname", "1.2.3.4",
		"--az", "us-east-1a",
	})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("should fail without --instance-id when other flags are provided")
	}
	if !strings.Contains(err.Error(), "instance-id") {
		t.Errorf("error should mention instance-id: %v", err)
	}
}

// TestSSHConfigCommand_PartialFlags_MissingAZ verifies that providing
// hostname and instance-id but not az triggers a clear error.
func TestSSHConfigCommand_PartialFlags_MissingAZ(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", configDir)

	buf := new(bytes.Buffer)
	deps := &sshConfigDeps{
		describe: &mockDescribeInstances{
			output: makeRunningInstanceWithAZ("i-abc123", "default", "alice", "1.2.3.4", "us-east-1a"),
		},
		owner: "alice",
	}
	rootCmd := newTestRoot()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.AddCommand(newSSHConfigCommandWithDeps(deps))
	rootCmd.SetArgs([]string{
		"ssh-config",
		"--yes",
		"--hostname", "1.2.3.4",
		"--instance-id", "i-abc123",
	})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("should fail without --az when other flags are provided")
	}
	if !strings.Contains(err.Error(), "az") {
		t.Errorf("error should mention az: %v", err)
	}
}

func TestSSHConfigCommand_StoresApproval(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", configDir)

	sshDir := t.TempDir()
	sshConfigPath := filepath.Join(sshDir, "config")

	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{
		"ssh-config",
		"--yes",
		"--ssh-config-path", sshConfigPath,
		"--hostname", "1.2.3.4",
		"--instance-id", "i-abc123",
		"--az", "us-east-1a",
	})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("ssh-config error: %v", err)
	}

	// Check that approval was persisted in mint config.
	configPath := filepath.Join(configDir, "config.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read mint config: %v", err)
	}
	if !strings.Contains(string(data), "ssh_config_approved = true") {
		t.Errorf("approval not stored in config:\n%s", string(data))
	}
}

func TestSSHConfigCommand_WarnsOnHandEdits(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", configDir)

	sshDir := t.TempDir()
	sshConfigPath := filepath.Join(sshDir, "config")

	// Write a tampered block manually — inner content does not match the checksum.
	tampered := "# mint:begin default\nHost mint-default\n    HostName 1.2.3.4\n    User root\n    Port 41122\n    IdentityFile ~/.config/mint/ssh_key_default\n    IdentitiesOnly yes\n    ProxyCommand echo tampered\n# mint:end default\n# mint:checksum:0000000000000000000000000000000000000000000000000000000000000000\n"
	if err := os.WriteFile(sshConfigPath, []byte(tampered), 0o600); err != nil {
		t.Fatalf("writing ssh config: %v", err)
	}

	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{
		"ssh-config",
		"--yes",
		"--ssh-config-path", sshConfigPath,
		"--hostname", "5.6.7.8",
		"--instance-id", "i-def456",
		"--az", "us-west-2b",
	})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("ssh-config error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(strings.ToLower(output), "hand-edit") {
		t.Errorf("should warn about hand edits, got:\n%s", output)
	}
}

func TestSSHConfigCommand_RemoveFlag(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", configDir)

	sshDir := t.TempDir()
	sshConfigPath := filepath.Join(sshDir, "config")

	// First write a block.
	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{
		"ssh-config",
		"--yes",
		"--ssh-config-path", sshConfigPath,
		"--hostname", "1.2.3.4",
		"--instance-id", "i-abc123",
		"--az", "us-east-1a",
	})
	_ = rootCmd.Execute()

	// Now remove it.
	buf.Reset()
	rootCmd2 := NewRootCommand()
	rootCmd2.SetOut(buf)
	rootCmd2.SetErr(buf)
	rootCmd2.SetArgs([]string{
		"ssh-config",
		"--remove",
		"--ssh-config-path", sshConfigPath,
	})

	err := rootCmd2.Execute()
	if err != nil {
		t.Fatalf("ssh-config --remove error: %v", err)
	}

	data, _ := os.ReadFile(sshConfigPath)
	if strings.Contains(string(data), "mint:begin") {
		t.Error("block not removed")
	}

	// Must report success, not "not found".
	output := buf.String()
	if !strings.Contains(output, "SSH config block removed for VM") {
		t.Errorf("expected removal success message, got: %q", output)
	}
}

func TestSSHConfigCommand_RemoveFlag_BlockNotFound(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", configDir)

	// Point to a path that does not exist.
	sshConfigPath := filepath.Join(t.TempDir(), "nonexistent-config")

	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{
		"ssh-config",
		"--remove",
		"--ssh-config-path", sshConfigPath,
	})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("ssh-config --remove should not error when no block found: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "No SSH config block found for VM") {
		t.Errorf("expected 'No SSH config block found' message, got: %q", output)
	}
	if strings.Contains(output, "SSH config block removed for VM") {
		t.Errorf("should not print removal success when no block was present, got: %q", output)
	}
}

func TestSSHConfigCommand_RemoveFlag_FileExistsNoBlock(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", configDir)

	sshDir := t.TempDir()
	sshConfigPath := filepath.Join(sshDir, "config")

	// Write a file with unrelated content — no mint block.
	if err := os.WriteFile(sshConfigPath, []byte("Host other\n    HostName other.example.com\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{
		"ssh-config",
		"--remove",
		"--ssh-config-path", sshConfigPath,
	})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("ssh-config --remove should not error when block absent: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "No SSH config block found for VM") {
		t.Errorf("expected 'No SSH config block found' message, got: %q", output)
	}
}

// TestSSHConfigCommand_AutoDiscover_Success verifies that when no explicit
// --hostname/--instance-id/--az flags are provided, the command calls FindVM
// and uses the discovered VM's values to generate the SSH config block.
func TestSSHConfigCommand_AutoDiscover_Success(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", configDir)

	sshDir := t.TempDir()
	sshConfigPath := filepath.Join(sshDir, "config")

	launchTime := time.Now().Add(-10 * time.Minute)
	deps := &sshConfigDeps{
		describe: &mockDescribeInstances{
			output: makeInstanceWithTimeAndAZ("i-disco123", "default", "alice", "running", "5.6.7.8", "m6i.xlarge", "complete", launchTime, "us-west-2a"),
		},
		owner: "alice",
	}

	buf := new(bytes.Buffer)
	rootCmd := newTestRoot()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.AddCommand(newSSHConfigCommandWithDeps(deps))
	rootCmd.SetArgs([]string{
		"ssh-config",
		"--yes",
		"--ssh-config-path", sshConfigPath,
	})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("ssh-config auto-discover error: %v", err)
	}

	data, err := os.ReadFile(sshConfigPath)
	if err != nil {
		t.Fatalf("read ssh config: %v", err)
	}
	content := string(data)

	// The discovered VM's IP, instance ID, and AZ must appear in the block.
	expectations := []string{
		"Host mint-default",
		"HostName 5.6.7.8",
		"i-disco123",
		"us-west-2a",
	}
	for _, exp := range expectations {
		if !strings.Contains(content, exp) {
			t.Errorf("ssh config missing %q (from auto-discovery), got:\n%s", exp, content)
		}
	}
}

// TestSSHConfigCommand_AutoDiscover_NoVM verifies that when auto-discovery
// finds no running VM, the command returns a clear error pointing users to
// mint up.
func TestSSHConfigCommand_AutoDiscover_NoVM(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", configDir)

	deps := &sshConfigDeps{
		describe: &mockDescribeInstances{
			// Empty output — no VMs found.
			output: makeEmptyDescribeOutput(),
		},
		owner: "alice",
	}

	buf := new(bytes.Buffer)
	rootCmd := newTestRoot()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.AddCommand(newSSHConfigCommandWithDeps(deps))
	rootCmd.SetArgs([]string{
		"ssh-config",
		"--yes",
	})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("should fail when no VM found during auto-discovery")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "mint up") {
		t.Errorf("error should mention 'mint up', got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "No running VM") {
		t.Errorf("error should mention 'No running VM', got: %s", errMsg)
	}
}

// TestSSHConfigCommand_AutoDiscover_ExplicitFlagsStillWork verifies regression:
// when all three explicit flags are provided, they are used directly without
// calling FindVM (even if deps.describe would return something different).
func TestSSHConfigCommand_AutoDiscover_ExplicitFlagsStillWork(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", configDir)

	sshDir := t.TempDir()
	sshConfigPath := filepath.Join(sshDir, "config")

	deps := &sshConfigDeps{
		// describe would return a different IP — explicit flags must win.
		describe: &mockDescribeInstances{
			output: makeRunningInstanceWithAZ("i-other", "default", "alice", "9.9.9.9", "eu-west-1a"),
		},
		owner: "alice",
	}

	buf := new(bytes.Buffer)
	rootCmd := newTestRoot()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.AddCommand(newSSHConfigCommandWithDeps(deps))
	rootCmd.SetArgs([]string{
		"ssh-config",
		"--yes",
		"--ssh-config-path", sshConfigPath,
		"--hostname", "1.2.3.4",
		"--instance-id", "i-explicit",
		"--az", "us-east-1a",
	})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("ssh-config with explicit flags error: %v", err)
	}

	data, _ := os.ReadFile(sshConfigPath)
	content := string(data)

	// Explicit values must be used, not the discovered ones.
	if !strings.Contains(content, "HostName 1.2.3.4") {
		t.Errorf("explicit hostname not used, got:\n%s", content)
	}
	if !strings.Contains(content, "i-explicit") {
		t.Errorf("explicit instance-id not used, got:\n%s", content)
	}
	if strings.Contains(content, "9.9.9.9") {
		t.Errorf("discovered IP leaked into explicit-mode output, got:\n%s", content)
	}
}

func TestSSHConfigCommand_PreservesExistingContent(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", configDir)

	sshDir := t.TempDir()
	sshConfigPath := filepath.Join(sshDir, "config")

	existing := "Host myserver\n    HostName example.com\n    User admin\n"
	if err := os.WriteFile(sshConfigPath, []byte(existing), 0o600); err != nil {
		t.Fatalf("writing ssh config: %v", err)
	}

	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{
		"ssh-config",
		"--yes",
		"--ssh-config-path", sshConfigPath,
		"--hostname", "1.2.3.4",
		"--instance-id", "i-abc123",
		"--az", "us-east-1a",
	})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("ssh-config error: %v", err)
	}

	data, _ := os.ReadFile(sshConfigPath)
	content := string(data)

	if !strings.Contains(content, "Host myserver") {
		t.Error("existing content was lost")
	}
	if !strings.Contains(content, "Host mint-default") {
		t.Error("managed block was not added")
	}
}

// makeInstanceWithTimeAndAZ creates a DescribeInstancesOutput with placement AZ.
func makeInstanceWithTimeAndAZ(id, vmName, owner, state, ip, instanceType, bootstrap string, launchTime time.Time, az string) *ec2.DescribeInstancesOutput {
	out := makeInstanceWithTime(id, vmName, owner, state, ip, instanceType, bootstrap, launchTime)
	out.Reservations[0].Instances[0].Placement = &ec2types.Placement{
		AvailabilityZone: aws.String(az),
	}
	return out
}

// makeEmptyDescribeOutput returns a DescribeInstancesOutput with no instances.
func makeEmptyDescribeOutput() *ec2.DescribeInstancesOutput {
	return &ec2.DescribeInstancesOutput{}
}

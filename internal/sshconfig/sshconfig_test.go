package sshconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateBlock(t *testing.T) {
	block := GenerateBlock("myvm", "1.2.3.4", "ubuntu", 41122, "i-abc123", "us-east-1a")

	// Must contain begin/end markers with VM name.
	if !strings.Contains(block, "# mint:begin myvm") {
		t.Errorf("missing begin marker, got:\n%s", block)
	}
	if !strings.Contains(block, "# mint:end myvm") {
		t.Errorf("missing end marker, got:\n%s", block)
	}

	// Must contain Host directive.
	if !strings.Contains(block, "Host mint-myvm") {
		t.Errorf("missing Host directive, got:\n%s", block)
	}

	// Must contain connection details.
	expectations := []string{
		"HostName 1.2.3.4",
		"User ubuntu",
		"Port 41122",
	}
	for _, exp := range expectations {
		if !strings.Contains(block, exp) {
			t.Errorf("missing %q in block:\n%s", exp, block)
		}
	}

	// Must contain checksum line.
	if !strings.Contains(block, "# mint:checksum:") {
		t.Errorf("missing checksum line, got:\n%s", block)
	}

	// Must NOT contain the old insecure host key settings.
	if strings.Contains(block, "StrictHostKeyChecking no") {
		t.Errorf("should not contain StrictHostKeyChecking no (finding 6 handles TOFU), got:\n%s", block)
	}
	if strings.Contains(block, "UserKnownHostsFile /dev/null") {
		t.Errorf("should not contain UserKnownHostsFile /dev/null, got:\n%s", block)
	}
}

func TestGenerateBlockProxyCommand(t *testing.T) {
	block := GenerateBlock("myvm", "1.2.3.4", "ubuntu", 41122, "i-abc123", "us-east-1a")

	// Must contain ProxyCommand with EC2 Instance Connect key push and nc tunnel.
	if !strings.Contains(block, "ProxyCommand") {
		t.Fatalf("missing ProxyCommand, got:\n%s", block)
	}

	// ProxyCommand must reference the instance ID.
	if !strings.Contains(block, "i-abc123") {
		t.Errorf("ProxyCommand missing instance ID, got:\n%s", block)
	}

	// ProxyCommand must reference the AZ.
	if !strings.Contains(block, "us-east-1a") {
		t.Errorf("ProxyCommand missing availability zone, got:\n%s", block)
	}

	// ProxyCommand must use aws ec2-instance-connect send-ssh-public-key.
	if !strings.Contains(block, "aws ec2-instance-connect send-ssh-public-key") {
		t.Errorf("ProxyCommand missing send-ssh-public-key command, got:\n%s", block)
	}

	// ProxyCommand must use ssh-keygen to generate ephemeral key.
	if !strings.Contains(block, "ssh-keygen") {
		t.Errorf("ProxyCommand missing ssh-keygen, got:\n%s", block)
	}

	// ProxyCommand must use nc for the TCP tunnel (%%h and %%p become %h %p in SSH config).
	if !strings.Contains(block, "%h %p") {
		t.Errorf("ProxyCommand missing nc %%h %%p tunnel, got:\n%s", block)
	}
}

func TestGenerateBlockProxyCommandUsesMktemp(t *testing.T) {
	block := GenerateBlock("myvm", "1.2.3.4", "ubuntu", 41122, "i-abc123", "us-east-1a")

	// ProxyCommand must use mktemp -d for unique temp dir per invocation
	// to avoid concurrent connection race conditions.
	if !strings.Contains(block, "mktemp -d") {
		t.Errorf("ProxyCommand must use mktemp -d for unique temp key dir, got:\n%s", block)
	}
}

func TestGenerateBlockProxyCommandHasTrapCleanup(t *testing.T) {
	block := GenerateBlock("myvm", "1.2.3.4", "ubuntu", 41122, "i-abc123", "us-east-1a")

	// ProxyCommand must include a trap for cleanup to prevent key persistence.
	if !strings.Contains(block, "trap") {
		t.Errorf("ProxyCommand must include trap for cleanup, got:\n%s", block)
	}
}

func TestGenerateBlockProxyCommandUsesAtomicSymlink(t *testing.T) {
	block := GenerateBlock("myvm", "1.2.3.4", "ubuntu", 41122, "i-abc123", "us-east-1a")

	// ProxyCommand must use ln -sf for atomic symlink update to the fixed
	// IdentityFile path, preventing concurrent connection corruption.
	if !strings.Contains(block, "ln -sf") {
		t.Errorf("ProxyCommand must use ln -sf for atomic symlink update, got:\n%s", block)
	}
}

func TestGenerateBlockProxyCommandNoFixedKeyInKeygen(t *testing.T) {
	block := GenerateBlock("myvm", "1.2.3.4", "ubuntu", 41122, "i-abc123", "us-east-1a")

	// ssh-keygen must NOT write directly to the fixed key path.
	// It should write to the mktemp directory instead.
	if strings.Contains(block, "ssh-keygen -t ed25519 -f ~/.config/mint/ssh_key_") {
		t.Errorf("ssh-keygen must not write directly to fixed key path (use mktemp dir instead), got:\n%s", block)
	}
}

func TestGenerateBlockIdentityFile(t *testing.T) {
	block := GenerateBlock("myvm", "1.2.3.4", "ubuntu", 41122, "i-abc123", "us-east-1a")

	// Must contain IdentityFile pointing to mint-managed key.
	if !strings.Contains(block, "IdentityFile") {
		t.Fatalf("missing IdentityFile, got:\n%s", block)
	}

	// IdentityFile should use the mint config directory and include VM name.
	if !strings.Contains(block, "~/.config/mint/ssh_key_myvm") {
		t.Errorf("IdentityFile should use ~/.config/mint/ssh_key_<vmName>, got:\n%s", block)
	}

	// Must have IdentitiesOnly to prevent SSH from trying other keys.
	if !strings.Contains(block, "IdentitiesOnly yes") {
		t.Errorf("missing IdentitiesOnly yes, got:\n%s", block)
	}
}

func TestGenerateBlockProxyCommandUser(t *testing.T) {
	block := GenerateBlock("myvm", "1.2.3.4", "ubuntu", 41122, "i-abc123", "us-east-1a")

	// ProxyCommand should pass the OS user to send-ssh-public-key.
	if !strings.Contains(block, "--instance-os-user ubuntu") {
		t.Errorf("ProxyCommand missing --instance-os-user, got:\n%s", block)
	}
}

func TestGenerateBlockChecksumIsStable(t *testing.T) {
	b1 := GenerateBlock("vm1", "10.0.0.1", "ubuntu", 41122, "i-111", "us-east-1a")
	b2 := GenerateBlock("vm1", "10.0.0.1", "ubuntu", 41122, "i-111", "us-east-1a")
	if b1 != b2 {
		t.Error("same inputs should produce identical blocks")
	}
}

func TestGenerateBlockChecksumDiffers(t *testing.T) {
	b1 := GenerateBlock("vm1", "10.0.0.1", "ubuntu", 41122, "i-111", "us-east-1a")
	b2 := GenerateBlock("vm2", "10.0.0.2", "ubuntu", 41122, "i-222", "us-west-2b")
	if b1 == b2 {
		t.Error("different inputs should produce different blocks")
	}
}

func TestReadManagedBlock_Present(t *testing.T) {
	block := GenerateBlock("testvm", "1.2.3.4", "ubuntu", 41122, "i-abc123", "us-east-1a")
	content := "# other stuff\n" + block + "\n# more stuff\n"

	got, ok := ReadManagedBlock(content, "testvm")
	if !ok {
		t.Fatal("expected block to be found")
	}
	if !strings.Contains(got, "Host mint-testvm") {
		t.Errorf("extracted block missing Host directive:\n%s", got)
	}
}

func TestReadManagedBlock_Absent(t *testing.T) {
	content := "# some SSH config\nHost example\n    HostName example.com\n"
	_, ok := ReadManagedBlock(content, "testvm")
	if ok {
		t.Error("expected block not found for absent VM")
	}
}

func TestReadManagedBlock_DifferentVM(t *testing.T) {
	block := GenerateBlock("vm-a", "1.2.3.4", "ubuntu", 41122, "i-abc123", "us-east-1a")
	content := block

	_, ok := ReadManagedBlock(content, "vm-b")
	if ok {
		t.Error("should not find block for different VM name")
	}
}

func TestHasHandEdits_NoEdits(t *testing.T) {
	block := GenerateBlock("testvm", "1.2.3.4", "ubuntu", 41122, "i-abc123", "us-east-1a")
	if HasHandEdits(block, "testvm") {
		t.Error("freshly generated block should not report hand edits")
	}
}

func TestHasHandEdits_WithEdits(t *testing.T) {
	block := GenerateBlock("testvm", "1.2.3.4", "ubuntu", 41122, "i-abc123", "us-east-1a")
	// Tamper with the block content between markers.
	tampered := strings.Replace(block, "User ubuntu", "User root", 1)
	if !HasHandEdits(tampered, "testvm") {
		t.Error("tampered block should report hand edits")
	}
}

func TestHasHandEdits_NoBlock(t *testing.T) {
	content := "# nothing here\n"
	if HasHandEdits(content, "missing") {
		t.Error("missing block should not report hand edits")
	}
}

func TestWriteManagedBlock_NewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".ssh", "config")

	block := GenerateBlock("testvm", "1.2.3.4", "ubuntu", 41122, "i-abc123", "us-east-1a")
	if err := WriteManagedBlock(path, "testvm", block); err != nil {
		t.Fatalf("write to new file: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if !strings.Contains(string(data), "Host mint-testvm") {
		t.Errorf("file missing block content:\n%s", string(data))
	}

	// File permissions should be 0600.
	info, _ := os.Stat(path)
	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("permissions = %o, want 0600", perm)
	}
}

func TestWriteManagedBlock_ExistingFileWithoutBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	existing := "Host example\n    HostName example.com\n"
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	block := GenerateBlock("testvm", "1.2.3.4", "ubuntu", 41122, "i-abc123", "us-east-1a")
	if err := WriteManagedBlock(path, "testvm", block); err != nil {
		t.Fatalf("write: %v", err)
	}

	data, _ := os.ReadFile(path)
	content := string(data)

	// Original content preserved.
	if !strings.Contains(content, "Host example") {
		t.Error("original content lost")
	}
	// New block appended.
	if !strings.Contains(content, "Host mint-testvm") {
		t.Error("new block not appended")
	}
}

func TestWriteManagedBlock_ReplacesExistingBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")

	// Write initial block.
	block1 := GenerateBlock("testvm", "1.2.3.4", "ubuntu", 41122, "i-abc123", "us-east-1a")
	if err := WriteManagedBlock(path, "testvm", block1); err != nil {
		t.Fatalf("first write: %v", err)
	}

	// Replace with updated block.
	block2 := GenerateBlock("testvm", "5.6.7.8", "ubuntu", 41122, "i-def456", "us-west-2b")
	if err := WriteManagedBlock(path, "testvm", block2); err != nil {
		t.Fatalf("second write: %v", err)
	}

	data, _ := os.ReadFile(path)
	content := string(data)

	// Old hostname gone.
	if strings.Contains(content, "1.2.3.4") {
		t.Error("old hostname still present after replace")
	}
	// New hostname present.
	if !strings.Contains(content, "5.6.7.8") {
		t.Error("new hostname missing after replace")
	}
	// Only one begin marker.
	if strings.Count(content, "# mint:begin testvm") != 1 {
		t.Errorf("expected exactly one begin marker, got %d", strings.Count(content, "# mint:begin testvm"))
	}
}

func TestWriteManagedBlock_MultipleVMs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")

	block1 := GenerateBlock("vm-a", "1.1.1.1", "ubuntu", 41122, "i-aaa", "us-east-1a")
	block2 := GenerateBlock("vm-b", "2.2.2.2", "ubuntu", 41122, "i-bbb", "us-west-2b")

	if err := WriteManagedBlock(path, "vm-a", block1); err != nil {
		t.Fatalf("write vm-a: %v", err)
	}
	if err := WriteManagedBlock(path, "vm-b", block2); err != nil {
		t.Fatalf("write vm-b: %v", err)
	}

	data, _ := os.ReadFile(path)
	content := string(data)

	if !strings.Contains(content, "Host mint-vm-a") {
		t.Error("missing vm-a block")
	}
	if !strings.Contains(content, "Host mint-vm-b") {
		t.Error("missing vm-b block")
	}
}

func TestRemoveManagedBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")

	existing := "Host example\n    HostName example.com\n"
	block := GenerateBlock("testvm", "1.2.3.4", "ubuntu", 41122, "i-abc123", "us-east-1a")
	if err := os.WriteFile(path, []byte(existing+"\n"+block+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	found, err := RemoveManagedBlock(path, "testvm")
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !found {
		t.Error("expected found=true when block was present")
	}

	data, _ := os.ReadFile(path)
	content := string(data)

	if strings.Contains(content, "mint:begin testvm") {
		t.Error("block not removed")
	}
	if !strings.Contains(content, "Host example") {
		t.Error("non-managed content removed")
	}
}

func TestRemoveManagedBlock_NoBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte("Host example\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Should not error when block doesn't exist, and should report found=false.
	found, err := RemoveManagedBlock(path, "nonexistent")
	if err != nil {
		t.Fatalf("remove nonexistent block should not error: %v", err)
	}
	if found {
		t.Error("expected found=false when no block was present")
	}
}

func TestRemoveManagedBlock_FileNotExist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent")

	// Should not error when file doesn't exist, and should report found=false.
	found, err := RemoveManagedBlock(path, "testvm")
	if err != nil {
		t.Fatalf("remove from nonexistent file should not error: %v", err)
	}
	if found {
		t.Error("expected found=false when file does not exist")
	}
}

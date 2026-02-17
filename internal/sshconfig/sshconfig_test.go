package sshconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateBlock(t *testing.T) {
	block := GenerateBlock("myvm", "1.2.3.4", "ubuntu", 41122)

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
		"StrictHostKeyChecking no",
		"UserKnownHostsFile /dev/null",
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
}

func TestGenerateBlockChecksumIsStable(t *testing.T) {
	b1 := GenerateBlock("vm1", "10.0.0.1", "ubuntu", 41122)
	b2 := GenerateBlock("vm1", "10.0.0.1", "ubuntu", 41122)
	if b1 != b2 {
		t.Error("same inputs should produce identical blocks")
	}
}

func TestGenerateBlockChecksumDiffers(t *testing.T) {
	b1 := GenerateBlock("vm1", "10.0.0.1", "ubuntu", 41122)
	b2 := GenerateBlock("vm2", "10.0.0.2", "ubuntu", 41122)
	if b1 == b2 {
		t.Error("different inputs should produce different blocks")
	}
}

func TestReadManagedBlock_Present(t *testing.T) {
	block := GenerateBlock("testvm", "1.2.3.4", "ubuntu", 41122)
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
	block := GenerateBlock("vm-a", "1.2.3.4", "ubuntu", 41122)
	content := block

	_, ok := ReadManagedBlock(content, "vm-b")
	if ok {
		t.Error("should not find block for different VM name")
	}
}

func TestHasHandEdits_NoEdits(t *testing.T) {
	block := GenerateBlock("testvm", "1.2.3.4", "ubuntu", 41122)
	if HasHandEdits(block, "testvm") {
		t.Error("freshly generated block should not report hand edits")
	}
}

func TestHasHandEdits_WithEdits(t *testing.T) {
	block := GenerateBlock("testvm", "1.2.3.4", "ubuntu", 41122)
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

	block := GenerateBlock("testvm", "1.2.3.4", "ubuntu", 41122)
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
	os.WriteFile(path, []byte(existing), 0o600)

	block := GenerateBlock("testvm", "1.2.3.4", "ubuntu", 41122)
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
	block1 := GenerateBlock("testvm", "1.2.3.4", "ubuntu", 41122)
	if err := WriteManagedBlock(path, "testvm", block1); err != nil {
		t.Fatalf("first write: %v", err)
	}

	// Replace with updated block.
	block2 := GenerateBlock("testvm", "5.6.7.8", "ubuntu", 41122)
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

	block1 := GenerateBlock("vm-a", "1.1.1.1", "ubuntu", 41122)
	block2 := GenerateBlock("vm-b", "2.2.2.2", "ubuntu", 41122)

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
	block := GenerateBlock("testvm", "1.2.3.4", "ubuntu", 41122)
	os.WriteFile(path, []byte(existing+"\n"+block+"\n"), 0o600)

	if err := RemoveManagedBlock(path, "testvm"); err != nil {
		t.Fatalf("remove: %v", err)
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
	os.WriteFile(path, []byte("Host example\n"), 0o600)

	// Should not error when block doesn't exist.
	if err := RemoveManagedBlock(path, "nonexistent"); err != nil {
		t.Fatalf("remove nonexistent block should not error: %v", err)
	}
}

func TestRemoveManagedBlock_FileNotExist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent")

	// Should not error when file doesn't exist.
	if err := RemoveManagedBlock(path, "testvm"); err != nil {
		t.Fatalf("remove from nonexistent file should not error: %v", err)
	}
}

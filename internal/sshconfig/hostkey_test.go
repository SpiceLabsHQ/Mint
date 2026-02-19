package sshconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *HostKeyStore {
	t.Helper()
	dir := t.TempDir()
	return NewHostKeyStore(dir)
}

func TestRecordAndCheckKey_Match(t *testing.T) {
	store := newTestStore(t)

	if err := store.RecordKey("myvm", "SHA256:abc123"); err != nil {
		t.Fatalf("record: %v", err)
	}

	matched, existing, err := store.CheckKey("myvm", "SHA256:abc123")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !matched {
		t.Error("expected match")
	}
	if existing != "SHA256:abc123" {
		t.Errorf("existing = %q, want %q", existing, "SHA256:abc123")
	}
}

func TestCheckKey_NoExistingKey(t *testing.T) {
	store := newTestStore(t)

	matched, existing, err := store.CheckKey("unknown", "SHA256:abc123")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if matched {
		t.Error("should not match for unknown VM")
	}
	if existing != "" {
		t.Errorf("existing = %q, want empty", existing)
	}
}

func TestCheckKey_KeyChanged(t *testing.T) {
	store := newTestStore(t)

	if err := store.RecordKey("myvm", "SHA256:oldkey"); err != nil {
		t.Fatalf("record: %v", err)
	}

	matched, existing, err := store.CheckKey("myvm", "SHA256:newkey")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if matched {
		t.Error("should not match when key changed")
	}
	if existing != "SHA256:oldkey" {
		t.Errorf("existing = %q, want %q", existing, "SHA256:oldkey")
	}
}

func TestRecordKey_OverwritesExisting(t *testing.T) {
	store := newTestStore(t)

	if err := store.RecordKey("myvm", "SHA256:first"); err != nil {
		t.Fatalf("record first: %v", err)
	}
	if err := store.RecordKey("myvm", "SHA256:second"); err != nil {
		t.Fatalf("record second: %v", err)
	}

	matched, _, err := store.CheckKey("myvm", "SHA256:second")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !matched {
		t.Error("should match after overwrite")
	}
}

func TestRemoveKey(t *testing.T) {
	store := newTestStore(t)

	if err := store.RecordKey("myvm", "SHA256:abc123"); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := store.RemoveKey("myvm"); err != nil {
		t.Fatalf("remove: %v", err)
	}

	matched, existing, err := store.CheckKey("myvm", "SHA256:abc123")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if matched {
		t.Error("should not match after removal")
	}
	if existing != "" {
		t.Errorf("existing = %q, want empty after removal", existing)
	}
}

func TestRemoveKey_Nonexistent(t *testing.T) {
	store := newTestStore(t)

	// Should not error when removing a key that doesn't exist.
	if err := store.RemoveKey("nonexistent"); err != nil {
		t.Fatalf("remove nonexistent: %v", err)
	}
}

func TestMultipleVMs(t *testing.T) {
	store := newTestStore(t)

	store.RecordKey("vm-a", "SHA256:aaa")
	store.RecordKey("vm-b", "SHA256:bbb")

	matched, _, _ := store.CheckKey("vm-a", "SHA256:aaa")
	if !matched {
		t.Error("vm-a should match")
	}
	matched, _, _ = store.CheckKey("vm-b", "SHA256:bbb")
	if !matched {
		t.Error("vm-b should match")
	}

	// Remove one, other persists.
	if err := store.RemoveKey("vm-a"); err != nil {
		t.Fatalf("remove vm-a: %v", err)
	}
	matched, _, _ = store.CheckKey("vm-a", "SHA256:aaa")
	if matched {
		t.Error("vm-a should not match after removal")
	}
	matched, _, _ = store.CheckKey("vm-b", "SHA256:bbb")
	if !matched {
		t.Error("vm-b should still match")
	}
}

func TestHostKeyStoreFilePermissions(t *testing.T) {
	store := newTestStore(t)

	store.RecordKey("myvm", "SHA256:abc123")

	path := filepath.Join(store.dir, "known_hosts")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("permissions = %o, want 0600", perm)
	}
}

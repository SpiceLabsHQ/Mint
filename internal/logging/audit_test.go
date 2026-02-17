package logging

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestNewAuditLoggerCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")

	_, err := NewAuditLogger(path)
	if err != nil {
		t.Fatalf("NewAuditLogger() unexpected error: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("audit log file not created: %v", err)
	}
}

func TestNewAuditLoggerCreatesParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "audit.log")

	_, err := NewAuditLogger(path)
	if err != nil {
		t.Fatalf("NewAuditLogger() unexpected error: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("audit log file not created in nested dir: %v", err)
	}
}

func TestAuditLoggerWritesJSONLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	logger, err := NewAuditLogger(path)
	if err != nil {
		t.Fatalf("NewAuditLogger() unexpected error: %v", err)
	}

	if err := logger.LogCommand("config set", "default", "arn:aws:iam::123456789012:user/ryan"); err != nil {
		t.Fatalf("LogCommand() error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error: %v", err)
	}

	var entry AuditLogEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("audit entry is not valid JSON: %v", err)
	}

	if entry.Timestamp == "" {
		t.Error("Timestamp is empty")
	}
	if entry.Command != "config set" {
		t.Errorf("Command = %q, want %q", entry.Command, "config set")
	}
	if entry.VMName != "default" {
		t.Errorf("VMName = %q, want %q", entry.VMName, "default")
	}
	if entry.CallerARN != "arn:aws:iam::123456789012:user/ryan" {
		t.Errorf("CallerARN = %q, want full ARN", entry.CallerARN)
	}
}

func TestAuditLoggerAppendsEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	logger, err := NewAuditLogger(path)
	if err != nil {
		t.Fatalf("NewAuditLogger() unexpected error: %v", err)
	}

	commands := []struct {
		command   string
		vmName    string
		callerARN string
	}{
		{"up", "default", "arn:aws:iam::123456789012:user/alice"},
		{"status", "dev", "arn:aws:iam::123456789012:user/bob"},
		{"destroy", "default", "arn:aws:iam::123456789012:user/alice"},
	}

	for _, cmd := range commands {
		if err := logger.LogCommand(cmd.command, cmd.vmName, cmd.callerARN); err != nil {
			t.Fatalf("LogCommand(%q) error: %v", cmd.command, err)
		}
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineCount := 0
	for scanner.Scan() {
		lineCount++
		var entry AuditLogEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatalf("line %d is not valid JSON: %v", lineCount, err)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}

	if lineCount != 3 {
		t.Errorf("expected 3 lines, got %d", lineCount)
	}
}

func TestAuditLoggerAppendsToExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")

	// First logger writes one entry
	logger1, err := NewAuditLogger(path)
	if err != nil {
		t.Fatalf("NewAuditLogger() first: %v", err)
	}
	if err := logger1.LogCommand("up", "default", "arn:aws:iam::123456789012:user/alice"); err != nil {
		t.Fatalf("LogCommand() first: %v", err)
	}

	// Second logger instance opens same file and appends
	logger2, err := NewAuditLogger(path)
	if err != nil {
		t.Fatalf("NewAuditLogger() second: %v", err)
	}
	if err := logger2.LogCommand("status", "default", "arn:aws:iam::123456789012:user/alice"); err != nil {
		t.Fatalf("LogCommand() second: %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineCount := 0
	for scanner.Scan() {
		lineCount++
	}

	if lineCount != 2 {
		t.Errorf("expected 2 lines after two separate loggers, got %d", lineCount)
	}
}

func TestAuditLoggerImplementsInterface(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	logger, err := NewAuditLogger(path)
	if err != nil {
		t.Fatalf("NewAuditLogger() unexpected error: %v", err)
	}

	var _ Auditor = logger
}

func TestAuditLoggerCloses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	logger, err := NewAuditLogger(path)
	if err != nil {
		t.Fatalf("NewAuditLogger() unexpected error: %v", err)
	}

	if err := logger.LogCommand("up", "default", "arn:aws:iam::123456789012:user/alice"); err != nil {
		t.Fatalf("LogCommand() error: %v", err)
	}

	if err := logger.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
}

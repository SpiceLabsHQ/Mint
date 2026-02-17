package logging

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewStructuredLoggerCreatesLogDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")

	_, err := NewStructuredLogger(dir, false)
	if err != nil {
		t.Fatalf("NewStructuredLogger() unexpected error: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("log directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("log path is not a directory")
	}
}

func TestStructuredLoggerWritesJSONFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	logger, err := NewStructuredLogger(dir, false)
	if err != nil {
		t.Fatalf("NewStructuredLogger() unexpected error: %v", err)
	}

	logger.Log("ec2", "DescribeInstances", 42*time.Millisecond, nil)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir() error: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no log files created")
	}

	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile() error: %v", err)
	}

	var entry StructuredLogEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("log entry is not valid JSON: %v", err)
	}

	if entry.Service != "ec2" {
		t.Errorf("Service = %q, want %q", entry.Service, "ec2")
	}
	if entry.Operation != "DescribeInstances" {
		t.Errorf("Operation = %q, want %q", entry.Operation, "DescribeInstances")
	}
	if entry.DurationMs != 42 {
		t.Errorf("DurationMs = %d, want 42", entry.DurationMs)
	}
	if entry.Result != "success" {
		t.Errorf("Result = %q, want %q", entry.Result, "success")
	}
	if entry.Timestamp == "" {
		t.Error("Timestamp is empty")
	}
}

func TestStructuredLoggerRecordsErrorResult(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	logger, err := NewStructuredLogger(dir, false)
	if err != nil {
		t.Fatalf("NewStructuredLogger() unexpected error: %v", err)
	}

	logger.Log("sts", "GetCallerIdentity", 10*time.Millisecond, os.ErrPermission)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir() error: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no log files created")
	}

	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile() error: %v", err)
	}

	var entry StructuredLogEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("log entry is not valid JSON: %v", err)
	}

	if entry.Result != "error" {
		t.Errorf("Result = %q, want %q", entry.Result, "error")
	}
}

func TestStructuredLoggerDebugWritesToStderr(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	var buf bytes.Buffer

	logger, err := NewStructuredLogger(dir, true)
	if err != nil {
		t.Fatalf("NewStructuredLogger() unexpected error: %v", err)
	}
	logger.SetStderr(&buf)

	logger.Log("ec2", "RunInstances", 100*time.Millisecond, nil)

	if buf.Len() == 0 {
		t.Fatal("debug mode should write to stderr, but buffer is empty")
	}

	var entry StructuredLogEntry
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("stderr output is not valid JSON: %v", err)
	}

	if entry.Service != "ec2" {
		t.Errorf("Service = %q, want %q", entry.Service, "ec2")
	}
}

func TestStructuredLoggerNoDebugSuppressesStderr(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	var buf bytes.Buffer

	logger, err := NewStructuredLogger(dir, false)
	if err != nil {
		t.Fatalf("NewStructuredLogger() unexpected error: %v", err)
	}
	logger.SetStderr(&buf)

	logger.Log("ec2", "RunInstances", 100*time.Millisecond, nil)

	if buf.Len() != 0 {
		t.Errorf("non-debug mode should suppress stderr, got %d bytes", buf.Len())
	}
}

func TestStructuredLoggerMultipleEntries(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	logger, err := NewStructuredLogger(dir, false)
	if err != nil {
		t.Fatalf("NewStructuredLogger() unexpected error: %v", err)
	}

	logger.Log("ec2", "DescribeInstances", 10*time.Millisecond, nil)
	logger.Log("sts", "GetCallerIdentity", 20*time.Millisecond, nil)
	logger.Log("ec2", "RunInstances", 30*time.Millisecond, os.ErrNotExist)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir() error: %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("expected 3 log files, got %d", len(entries))
	}
}

func TestStructuredLoggerImplementsInterface(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	logger, err := NewStructuredLogger(dir, false)
	if err != nil {
		t.Fatalf("NewStructuredLogger() unexpected error: %v", err)
	}

	// Compile-time check that *structuredLogger satisfies Logger
	var _ Logger = logger
}

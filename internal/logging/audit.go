package logging

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Auditor defines the interface for command invocation audit logging.
// Each command execution is recorded with its context for traceability.
type Auditor interface {
	LogCommand(command, vmName, callerARN string) error
	Close() error
}

// AuditLogEntry represents a single command invocation audit record.
type AuditLogEntry struct {
	Timestamp string `json:"timestamp"`
	Command   string `json:"command"`
	VMName    string `json:"vm_name"`
	CallerARN string `json:"caller_arn"`
}

// auditLogger appends JSON Lines entries to a single audit log file.
type auditLogger struct {
	file *os.File
}

// NewAuditLogger creates an Auditor that appends entries to the file at path.
// The parent directory and file are created automatically if they do not exist.
func NewAuditLogger(path string) (Auditor, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create audit log dir: %w", err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open audit log: %w", err)
	}

	return &auditLogger{file: f}, nil
}

// LogCommand records a single command invocation as a JSON Lines entry.
func (a *auditLogger) LogCommand(command, vmName, callerARN string) error {
	entry := AuditLogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Command:   command,
		VMName:    vmName,
		CallerARN: callerARN,
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal audit entry: %w", err)
	}

	data = append(data, '\n')
	if _, err := a.file.Write(data); err != nil {
		return fmt.Errorf("write audit entry: %w", err)
	}

	return nil
}

// Close closes the underlying audit log file.
func (a *auditLogger) Close() error {
	return a.file.Close()
}

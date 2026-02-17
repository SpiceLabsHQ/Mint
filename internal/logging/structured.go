// Package logging provides structured logging for AWS API calls and audit
// logging for command invocations. Structured logs are written as individual
// JSON files to ~/.config/mint/logs/. Audit entries are appended as JSON
// Lines to ~/.config/mint/audit.log.
package logging

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Logger defines the interface for structured AWS API call logging.
// Implementations record service, operation, duration, and result for
// each AWS SDK call.
type Logger interface {
	Log(service, operation string, duration time.Duration, err error)
	SetStderr(w io.Writer)
}

// StructuredLogEntry represents a single AWS API call log entry.
type StructuredLogEntry struct {
	Timestamp  string `json:"timestamp"`
	Service    string `json:"service"`
	Operation  string `json:"operation"`
	DurationMs int64  `json:"duration_ms"`
	Result     string `json:"result"`
}

// structuredLogger writes per-call JSON log files to a directory
// and optionally mirrors entries to stderr when debug mode is enabled.
type structuredLogger struct {
	dir    string
	debug  bool
	stderr io.Writer
	seq    int
}

// NewStructuredLogger creates a Logger that writes JSON log files to dir.
// The directory is created automatically if it does not exist.
// When debug is true, each log entry is also written to stderr.
func NewStructuredLogger(dir string, debug bool) (Logger, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}
	return &structuredLogger{
		dir:    dir,
		debug:  debug,
		stderr: os.Stderr,
	}, nil
}

// SetStderr overrides the writer used for debug output.
// This is primarily useful for testing.
func (l *structuredLogger) SetStderr(w io.Writer) {
	l.stderr = w
}

// Log records a single AWS API call as a JSON file in the log directory.
// If debug mode is enabled, the entry is also written to stderr.
func (l *structuredLogger) Log(service, operation string, duration time.Duration, err error) {
	result := "success"
	if err != nil {
		result = "error"
	}

	entry := StructuredLogEntry{
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
		Service:    service,
		Operation:  operation,
		DurationMs: duration.Milliseconds(),
		Result:     result,
	}

	data, jsonErr := json.Marshal(entry)
	if jsonErr != nil {
		return
	}

	l.seq++
	filename := fmt.Sprintf("%s_%04d_%s_%s.json",
		time.Now().UTC().Format("20060102T150405Z"),
		l.seq,
		service,
		operation,
	)
	path := filepath.Join(l.dir, filename)
	// Best-effort write; logging failures should not crash the CLI.
	_ = os.WriteFile(path, data, 0o600)

	if l.debug && l.stderr != nil {
		data = append(data, '\n')
		_, _ = l.stderr.Write(data)
	}
}

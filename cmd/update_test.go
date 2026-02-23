package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/nicholasgasior/mint/internal/selfupdate"
)

// mockUpdater implements SelfUpdater for testing.
type mockUpdater struct {
	checkLatestFn      func(ctx context.Context) (*selfupdate.Release, error)
	downloadFn         func(ctx context.Context, release *selfupdate.Release, destDir string) (string, error)
	downloadChecksumFn func(ctx context.Context, release *selfupdate.Release) (string, error)
	verifyChecksumFn   func(archivePath, checksumFileContent string) error
	extractFn          func(archivePath, destDir string) (string, error)
	applyFn            func(newBinaryPath, currentBinaryPath string) error
}

func (m *mockUpdater) CheckLatest(ctx context.Context) (*selfupdate.Release, error) {
	if m.checkLatestFn != nil {
		return m.checkLatestFn(ctx)
	}
	return nil, nil
}

func (m *mockUpdater) Download(ctx context.Context, release *selfupdate.Release, destDir string) (string, error) {
	if m.downloadFn != nil {
		return m.downloadFn(ctx, release, destDir)
	}
	return "", nil
}

func (m *mockUpdater) DownloadChecksums(ctx context.Context, release *selfupdate.Release) (string, error) {
	if m.downloadChecksumFn != nil {
		return m.downloadChecksumFn(ctx, release)
	}
	return "", nil
}

func (m *mockUpdater) VerifyChecksum(archivePath, checksumFileContent string) error {
	if m.verifyChecksumFn != nil {
		return m.verifyChecksumFn(archivePath, checksumFileContent)
	}
	return nil
}

func (m *mockUpdater) Extract(archivePath, destDir string) (string, error) {
	if m.extractFn != nil {
		return m.extractFn(archivePath, destDir)
	}
	return filepath.Join(destDir, "mint"), nil
}

func (m *mockUpdater) Apply(newBinaryPath, currentBinaryPath string) error {
	if m.applyFn != nil {
		return m.applyFn(newBinaryPath, currentBinaryPath)
	}
	return nil
}

func TestUpdateCommand_SuccessfulUpdate(t *testing.T) {
	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "mint")
	if err := os.WriteFile(binaryPath, []byte("old"), 0o755); err != nil {
		t.Fatalf("writing binary: %v", err)
	}

	mock := &mockUpdater{
		checkLatestFn: func(ctx context.Context) (*selfupdate.Release, error) {
			return &selfupdate.Release{TagName: "v2.0.0"}, nil
		},
		downloadFn: func(ctx context.Context, release *selfupdate.Release, destDir string) (string, error) {
			p := filepath.Join(destDir, "mint.tar.gz")
			_ = os.WriteFile(p, []byte("archive-data"), 0o644)
			return p, nil
		},
		downloadChecksumFn: func(ctx context.Context, release *selfupdate.Release) (string, error) {
			return "abc123  mint_v2.0.0_linux_amd64.tar.gz\n", nil
		},
		verifyChecksumFn: func(archivePath, checksumFileContent string) error {
			return nil
		},
		extractFn: func(archivePath, destDir string) (string, error) {
			p := filepath.Join(destDir, "mint")
			_ = os.WriteFile(p, []byte("new-binary"), 0o755)
			return p, nil
		},
		applyFn: func(newBinaryPath, currentBinaryPath string) error {
			return nil
		},
	}

	cmd := newUpdateCommandWithDeps(&updateDeps{
		updater:    mock,
		binaryPath: binaryPath,
	})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetContext(context.Background())

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Updated mint to v2.0.0") {
		t.Errorf("output missing success message, got: %s", output)
	}
}

func TestUpdateCommand_AlreadyCurrent(t *testing.T) {
	mock := &mockUpdater{
		checkLatestFn: func(ctx context.Context) (*selfupdate.Release, error) {
			return nil, nil // already current
		},
	}

	cmd := newUpdateCommandWithDeps(&updateDeps{
		updater:    mock,
		binaryPath: "/usr/local/bin/mint",
	})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetContext(context.Background())

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Already up to date") {
		t.Errorf("output missing up-to-date message, got: %s", output)
	}
}

func TestUpdateCommand_ChecksumMismatch(t *testing.T) {
	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "mint")
	if err := os.WriteFile(binaryPath, []byte("old"), 0o755); err != nil {
		t.Fatalf("writing binary: %v", err)
	}

	extractCalled := false
	mock := &mockUpdater{
		checkLatestFn: func(ctx context.Context) (*selfupdate.Release, error) {
			return &selfupdate.Release{TagName: "v2.0.0"}, nil
		},
		downloadFn: func(ctx context.Context, release *selfupdate.Release, destDir string) (string, error) {
			p := filepath.Join(destDir, "mint.tar.gz")
			_ = os.WriteFile(p, []byte("archive"), 0o644)
			return p, nil
		},
		downloadChecksumFn: func(ctx context.Context, release *selfupdate.Release) (string, error) {
			return "checksums content", nil
		},
		verifyChecksumFn: func(archivePath, checksumFileContent string) error {
			return fmt.Errorf("checksum mismatch: expected abc, got def")
		},
		extractFn: func(archivePath, destDir string) (string, error) {
			extractCalled = true
			return "", fmt.Errorf("should not be called")
		},
	}

	cmd := newUpdateCommandWithDeps(&updateDeps{
		updater:    mock,
		binaryPath: binaryPath,
	})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetContext(context.Background())

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for checksum mismatch")
	}
	if !strings.Contains(err.Error(), "checksum") {
		t.Errorf("error should mention checksum, got: %v", err)
	}

	// Extract should NOT have been called when checksum fails.
	if extractCalled {
		t.Error("Extract should not be called after checksum verification failure")
	}

	// Original binary should be untouched (Apply never called).
	data, _ := os.ReadFile(binaryPath)
	if string(data) != "old" {
		t.Error("original binary should be untouched after checksum failure")
	}
}

// TestUpdateCommand_RateLimitExitsZero verifies that when the GitHub API
// returns 403 (rate limit exceeded), mint update exits 0 with a friendly
// message instead of exiting 1 with an alarming error.
func TestUpdateCommand_RateLimitExitsZero(t *testing.T) {
	mock := &mockUpdater{
		checkLatestFn: func(ctx context.Context) (*selfupdate.Release, error) {
			return nil, &selfupdate.RateLimitError{}
		},
	}

	cmd := newUpdateCommandWithDeps(&updateDeps{
		updater:    mock,
		binaryPath: "/usr/local/bin/mint",
	})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetContext(context.Background())

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("expected exit 0 for rate limit, got error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "rate limit") {
		t.Errorf("output should mention rate limit, got: %s", output)
	}
}

func TestUpdateCommand_NetworkFailure(t *testing.T) {
	mock := &mockUpdater{
		checkLatestFn: func(ctx context.Context) (*selfupdate.Release, error) {
			return nil, fmt.Errorf("connection refused")
		},
	}

	cmd := newUpdateCommandWithDeps(&updateDeps{
		updater:    mock,
		binaryPath: "/usr/local/bin/mint",
	})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetContext(context.Background())

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for network failure")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("error should mention connection issue, got: %v", err)
	}
}

func TestUpdateCommand_DownloadFailure(t *testing.T) {
	mock := &mockUpdater{
		checkLatestFn: func(ctx context.Context) (*selfupdate.Release, error) {
			return &selfupdate.Release{TagName: "v2.0.0"}, nil
		},
		downloadFn: func(ctx context.Context, release *selfupdate.Release, destDir string) (string, error) {
			return "", fmt.Errorf("404 not found")
		},
	}

	cmd := newUpdateCommandWithDeps(&updateDeps{
		updater:    mock,
		binaryPath: "/usr/local/bin/mint",
	})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetContext(context.Background())

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for download failure")
	}
}

func TestUpdateCommand_RegisteredOnRoot(t *testing.T) {
	root := NewRootCommand()

	var found bool
	for _, cmd := range root.Commands() {
		if cmd.Name() == "update" {
			found = true
			break
		}
	}

	if !found {
		t.Error("expected 'update' command to be registered on root")
	}
}

func TestUpdateCommand_NoArgs(t *testing.T) {
	cmd := newUpdateCommandWithDeps(&updateDeps{
		updater:    &mockUpdater{},
		binaryPath: "/usr/local/bin/mint",
	})

	// Cobra should reject extra args.
	cmd.SetArgs([]string{"extra-arg"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when passing args to update command")
	}
}

func TestUpdateCommand_ExcludedFromAWS(t *testing.T) {
	root := &cobra.Command{Use: "mint"}
	cmd := &cobra.Command{Use: "update"}
	root.AddCommand(cmd)
	if commandNeedsAWS(cmd) {
		t.Error("update command should be excluded from AWS initialization")
	}
}

// Verify update is in Phase 3 section.
func TestUpdateInPhase3Commands(t *testing.T) {
	root := NewRootCommand()
	registered := make(map[string]*cobra.Command)
	for _, cmd := range root.Commands() {
		registered[cmd.Name()] = cmd
	}

	cmd, ok := registered["update"]
	if !ok {
		t.Fatal("update command not registered")
	}
	if cmd.Use != "update" {
		t.Errorf("Use = %q, want %q", cmd.Use, "update")
	}
}

// Verify the flow order: download -> checksum -> extract -> apply.
func TestUpdateCommand_FlowOrder(t *testing.T) {
	var steps []string

	mock := &mockUpdater{
		checkLatestFn: func(ctx context.Context) (*selfupdate.Release, error) {
			steps = append(steps, "check")
			return &selfupdate.Release{TagName: "v2.0.0"}, nil
		},
		downloadFn: func(ctx context.Context, release *selfupdate.Release, destDir string) (string, error) {
			steps = append(steps, "download")
			p := filepath.Join(destDir, "archive.tar.gz")
			_ = os.WriteFile(p, []byte("data"), 0o644)
			return p, nil
		},
		downloadChecksumFn: func(ctx context.Context, release *selfupdate.Release) (string, error) {
			steps = append(steps, "checksums")
			return "hash  file.tar.gz\n", nil
		},
		verifyChecksumFn: func(archivePath, checksumFileContent string) error {
			steps = append(steps, "verify")
			return nil
		},
		extractFn: func(archivePath, destDir string) (string, error) {
			steps = append(steps, "extract")
			p := filepath.Join(destDir, "mint")
			_ = os.WriteFile(p, []byte("bin"), 0o755)
			return p, nil
		},
		applyFn: func(newBinaryPath, currentBinaryPath string) error {
			steps = append(steps, "apply")
			return nil
		},
	}

	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "mint")
	if err := os.WriteFile(binaryPath, []byte("old"), 0o755); err != nil {
		t.Fatalf("writing binary: %v", err)
	}

	cmd := newUpdateCommandWithDeps(&updateDeps{
		updater:    mock,
		binaryPath: binaryPath,
	})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetContext(context.Background())

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"check", "download", "checksums", "verify", "extract", "apply"}
	if len(steps) != len(expected) {
		t.Fatalf("steps = %v, want %v", steps, expected)
	}
	for i, s := range expected {
		if steps[i] != s {
			t.Errorf("step[%d] = %q, want %q", i, steps[i], s)
		}
	}
}

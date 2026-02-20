package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestJSONCredentialErrorOutput verifies Bug #67: when --json is set and AWS
// credentials are unavailable, the error output must be valid JSON on stdout,
// not plaintext on stderr.
func TestJSONCredentialErrorOutput(t *testing.T) {
	// We use the real root command with a command that needs AWS (list).
	// Without real credentials, PersistentPreRunE will fail with a credential error.
	// In JSON mode it should write JSON to stdout and return a silent error.
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)

	rootCmd := NewRootCommand()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs([]string{"--json", "list"})

	err := rootCmd.Execute()
	// Expect an error (no creds in test environment).
	if err == nil {
		// If we have real creds somehow, skip this check.
		t.Skip("skipping: real AWS credentials appear to be available")
	}

	// The error message must be empty (silentExitError) so main.go doesn't
	// double-print to stderr.
	if msg := err.Error(); msg != "" {
		t.Errorf("expected empty error message (silentExitError) in JSON mode, got: %q", msg)
	}

	// Stderr must be empty — no duplicate plaintext error.
	if stderrContent := stderr.String(); stderrContent != "" {
		t.Errorf("expected empty stderr in JSON mode, got: %q", stderrContent)
	}

	// Stdout must contain valid JSON with an "error" key.
	stdoutContent := strings.TrimSpace(stdout.String())
	if stdoutContent == "" {
		t.Fatal("expected JSON output on stdout, got empty")
	}
	var result map[string]any
	if err2 := json.Unmarshal([]byte(stdoutContent), &result); err2 != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", err2, stdoutContent)
	}
	if _, ok := result["error"]; !ok {
		t.Errorf("JSON output missing 'error' key, got: %s", stdoutContent)
	}
}

func TestPhase2CommandsRegistered(t *testing.T) {
	root := NewRootCommand()

	phase2Commands := []string{
		"mosh", "connect", "sessions", "key", "project", "extend",
	}

	registered := make(map[string]bool)
	for _, cmd := range root.Commands() {
		registered[cmd.Name()] = true
	}

	for _, name := range phase2Commands {
		if !registered[name] {
			t.Errorf("expected Phase 2 command %q to be registered on root", name)
		}
	}
}

func TestExistingCommandsStillRegistered(t *testing.T) {
	root := NewRootCommand()

	existingCommands := []string{
		"up", "destroy", "ssh", "code", "list", "status",
		"config", "init", "version", "down", "ssh-config",
	}

	registered := make(map[string]bool)
	for _, cmd := range root.Commands() {
		registered[cmd.Name()] = true
	}

	for _, name := range existingCommands {
		if !registered[name] {
			t.Errorf("expected existing command %q to still be registered on root", name)
		}
	}
}

func TestKeyHasAddSubcommand(t *testing.T) {
	root := NewRootCommand()

	var found bool
	for _, cmd := range root.Commands() {
		if cmd.Name() == "key" {
			for _, sub := range cmd.Commands() {
				if sub.Name() == "add" {
					found = true
				}
			}
		}
	}

	if !found {
		t.Error("expected 'key' command to have 'add' subcommand")
	}
}

func TestPhase3CommandsRegistered(t *testing.T) {
	root := NewRootCommand()

	phase3Commands := []string{
		"resize",
		"recreate",
		"doctor",
	}

	registered := make(map[string]bool)
	for _, cmd := range root.Commands() {
		registered[cmd.Name()] = true
	}

	for _, name := range phase3Commands {
		if !registered[name] {
			t.Errorf("expected Phase 3 command %q to be registered on root", name)
		}
	}
}

func TestProjectHasSubcommands(t *testing.T) {
	root := NewRootCommand()

	expectedSubs := []string{"add", "list", "rebuild"}

	var projectCmd *cobra.Command
	for _, cmd := range root.Commands() {
		if cmd.Name() == "project" {
			projectCmd = cmd
			break
		}
	}

	if projectCmd == nil {
		t.Fatal("expected 'project' command to be registered on root")
	}

	subNames := make(map[string]bool)
	for _, sub := range projectCmd.Commands() {
		subNames[sub.Name()] = true
	}

	for _, name := range expectedSubs {
		if !subNames[name] {
			t.Errorf("expected 'project' command to have %q subcommand", name)
		}
	}
}

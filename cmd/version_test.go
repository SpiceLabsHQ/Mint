package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestVersionCommandOutput(t *testing.T) {
	// Capture output from version command execution
	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"version"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("version command returned error: %v", err)
	}

	output := buf.String()

	// Version output must contain all three fields
	if !strings.Contains(output, "version:") {
		t.Errorf("version output missing 'version:' field, got: %s", output)
	}
	if !strings.Contains(output, "commit:") {
		t.Errorf("version output missing 'commit:' field, got: %s", output)
	}
	if !strings.Contains(output, "date:") {
		t.Errorf("version output missing 'date:' field, got: %s", output)
	}
}

func TestVersionCommandDevDefaults(t *testing.T) {
	// When no ldflags are injected, dev defaults should appear
	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"version"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("version command returned error: %v", err)
	}

	output := buf.String()

	if !strings.Contains(output, "dev") {
		t.Errorf("expected dev default version, got: %s", output)
	}
	if !strings.Contains(output, "none") {
		t.Errorf("expected 'none' default commit, got: %s", output)
	}
	if !strings.Contains(output, "unknown") {
		t.Errorf("expected 'unknown' default date, got: %s", output)
	}
}

func TestVersionCommandJSONOutput(t *testing.T) {
	// --json flag must produce valid JSON with version, commit, date fields
	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"--json", "version"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("version --json returned error: %v", err)
	}

	output := buf.String()

	// Output must be valid JSON
	var result map[string]string
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("version --json output is not valid JSON: %v\noutput: %s", err, output)
	}

	// Must contain version, commit, date keys
	if _, ok := result["version"]; !ok {
		t.Errorf("JSON output missing 'version' key, got: %s", output)
	}
	if _, ok := result["commit"]; !ok {
		t.Errorf("JSON output missing 'commit' key, got: %s", output)
	}
	if _, ok := result["date"]; !ok {
		t.Errorf("JSON output missing 'date' key, got: %s", output)
	}

	// Dev defaults must appear in values
	if result["version"] != "dev" {
		t.Errorf("expected version 'dev', got: %q", result["version"])
	}
	if result["commit"] != "none" {
		t.Errorf("expected commit 'none', got: %q", result["commit"])
	}
	if result["date"] != "unknown" {
		t.Errorf("expected date 'unknown', got: %q", result["date"])
	}
}

func TestVersionCommandJSONOutputHasTrailingNewline(t *testing.T) {
	// JSON output must end with a trailing newline (standard for CLI tools)
	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"--json", "version"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("version --json returned error: %v", err)
	}

	output := buf.String()
	if !strings.HasSuffix(output, "\n") {
		t.Errorf("version --json output missing trailing newline, got: %q", output)
	}
}

func TestVersionCommandPlainTextUnchangedWhenNoJSONFlag(t *testing.T) {
	// Without --json, output must remain plain text (not JSON)
	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"version"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("version command returned error: %v", err)
	}

	output := buf.String()

	// Must NOT be JSON — plain text uses "key: value" format, not curly braces
	if strings.HasPrefix(strings.TrimSpace(output), "{") {
		t.Errorf("version without --json should not produce JSON, got: %s", output)
	}

	// Must still contain the plain-text labels
	if !strings.Contains(output, "mint version:") {
		t.Errorf("plain text output missing 'mint version:' label, got: %s", output)
	}
}

func TestRootCommandExecutesWithoutError(t *testing.T) {
	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("root command returned error: %v", err)
	}
}

func TestRootCommandShowsHelp(t *testing.T) {
	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"--help"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("root --help returned error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "EC2-based development environments") {
		t.Errorf("help text missing description, got: %s", output)
	}
}

// TestVersionFlagOnRoot verifies Bug #64: mint --version should work.
// Cobra registers the --version flag automatically when rootCmd.Version is set.
func TestVersionFlagOnRoot(t *testing.T) {
	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"--version"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("mint --version returned error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "mint") {
		t.Errorf("mint --version output missing 'mint', got: %s", output)
	}
	if !strings.Contains(output, "dev") {
		t.Errorf("mint --version output missing version string, got: %s", output)
	}
}

func TestGlobalFlagsExist(t *testing.T) {
	rootCmd := NewRootCommand()

	flags := []struct {
		name         string
		defaultValue string
	}{
		{"verbose", "false"},
		{"debug", "false"},
		{"json", "false"},
		{"yes", "false"},
		{"vm", "default"},
	}

	for _, f := range flags {
		flag := rootCmd.PersistentFlags().Lookup(f.name)
		if flag == nil {
			t.Errorf("expected persistent flag --%s to be registered", f.name)
			continue
		}
		if flag.DefValue != f.defaultValue {
			t.Errorf("flag --%s: expected default %q, got %q", f.name, f.defaultValue, flag.DefValue)
		}
	}
}

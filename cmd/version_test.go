package cmd

import (
	"bytes"
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

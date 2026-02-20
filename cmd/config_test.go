package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestConfigCommandDisplaysValues(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", dir)

	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"config"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("config command error: %v", err)
	}

	output := buf.String()

	// Should display all config keys with their default values
	expectations := []string{
		"region",
		"instance_type",
		"volume_size_gb",
		"volume_iops",
		"idle_timeout_minutes",
		"ssh_config_approved",
		"m6i.xlarge",
		"50",
		"3000",
		"60",
		"false",
	}

	for _, exp := range expectations {
		if !strings.Contains(output, exp) {
			t.Errorf("config output missing %q, got:\n%s", exp, output)
		}
	}
}

func TestConfigCommandJSONOutput(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", dir)

	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"config", "--json"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("config --json error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("config --json output is not valid JSON: %v\nOutput: %s", err, buf.String())
	}

	expectedKeys := []string{"region", "instance_type", "volume_size_gb", "volume_iops", "idle_timeout_minutes", "ssh_config_approved"}
	for _, key := range expectedKeys {
		if _, ok := result[key]; !ok {
			t.Errorf("JSON output missing key %q", key)
		}
	}
}

func TestConfigSetCommand(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", dir)

	// Set a value
	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"config", "set", "region", "us-west-2"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("config set error: %v", err)
	}

	// Verify it was persisted by reading config
	buf.Reset()
	rootCmd2 := NewRootCommand()
	rootCmd2.SetOut(buf)
	rootCmd2.SetErr(buf)
	rootCmd2.SetArgs([]string{"config", "--json"})

	err = rootCmd2.Execute()
	if err != nil {
		t.Fatalf("config display error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("config output invalid JSON: %v", err)
	}

	if result["region"] != "us-west-2" {
		t.Errorf("region = %v, want us-west-2", result["region"])
	}
}

func TestConfigSetRejectsInvalidVolume(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", dir)

	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"config", "set", "volume_size_gb", "30"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("config set volume_size_gb 30 should fail")
	}
}

func TestConfigSetRejectsInvalidIdleTimeout(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", dir)

	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"config", "set", "idle_timeout_minutes", "5"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("config set idle_timeout_minutes 5 should fail")
	}
}

func TestConfigSetRejectsUnknownKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", dir)

	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"config", "set", "unknown_key", "foo"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("config set unknown_key should fail")
	}

	// Error message should list valid keys
	errMsg := err.Error()
	if !strings.Contains(errMsg, "region") {
		t.Errorf("error message should list valid keys, got: %s", errMsg)
	}
}

func TestConfigCommandUsesContextForJSON(t *testing.T) {
	// Verifies --json flows through CLIContext (PersistentPreRunE), not direct flag read.
	// If context plumbing breaks, this test catches it because config would fall back to
	// human output instead of JSON.
	dir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", dir)

	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"--json", "config"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("config with --json error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("expected JSON output when --json is a global flag before subcommand, got: %s", buf.String())
	}

	if _, ok := result["region"]; !ok {
		t.Error("JSON output missing 'region' key")
	}
}

func TestConfigSetRequiresArgs(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", dir)

	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"config", "set"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("config set without args should fail")
	}
}

func TestConfigFileCreatedOnSet(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", dir)

	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"config", "set", "instance_type", "t3.micro"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("config set error: %v", err)
	}

	// Config file should now exist
	configPath := dir + "/config.toml"
	if _, err := os.Stat(configPath); err != nil {
		t.Errorf("config.toml not created after set: %v", err)
	}
}

func TestConfigGetReturnsSetValue(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", dir)

	// First set a value
	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"config", "set", "region", "us-west-2"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("config set error: %v", err)
	}

	// Now get it
	buf.Reset()
	rootCmd2 := NewRootCommand()
	rootCmd2.SetOut(buf)
	rootCmd2.SetErr(buf)
	rootCmd2.SetArgs([]string{"config", "get", "region"})

	if err := rootCmd2.Execute(); err != nil {
		t.Fatalf("config get error: %v", err)
	}

	output := strings.TrimSpace(buf.String())
	if output != "us-west-2" {
		t.Errorf("config get region = %q, want %q", output, "us-west-2")
	}
}

func TestConfigGetDefaultValue(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", dir)

	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"config", "get", "instance_type"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("config get error: %v", err)
	}

	output := strings.TrimSpace(buf.String())
	if output != "m6i.xlarge" {
		t.Errorf("config get instance_type = %q, want %q", output, "m6i.xlarge")
	}
}

func TestConfigGetEmptyRegion(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", dir)

	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"config", "get", "region"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("config get error: %v", err)
	}

	// Unset region should output "(not set)" to match human display behavior.
	output := strings.TrimSpace(buf.String())
	if output != "(not set)" {
		t.Errorf("config get region (unset) = %q, want %q", output, "(not set)")
	}
}

func TestConfigGetUnknownKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", dir)

	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"config", "get", "unknown_key"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("config get unknown_key should fail")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "region") {
		t.Errorf("error message should list valid keys, got: %s", errMsg)
	}
}

func TestConfigGetRequiresArgs(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", dir)

	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"config", "get"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("config get without args should fail")
	}
}

// TestConfigGetNoArgsShowsValidKeys verifies Bug #68: when no key is given,
// the error message should list valid keys rather than the generic cobra
// "accepts 1 arg(s), received 0" message.
func TestConfigGetNoArgsShowsValidKeys(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", dir)

	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"config", "get"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("config get without args should fail")
	}

	errMsg := err.Error()
	// Must list valid keys (at minimum "region") so the user knows what to provide.
	if !strings.Contains(errMsg, "region") {
		t.Errorf("error should list valid keys, got: %s", errMsg)
	}
	// Must NOT expose the unhelpful generic cobra phrasing.
	if strings.Contains(errMsg, "accepts 1 arg(s)") {
		t.Errorf("error should not use generic cobra phrasing, got: %s", errMsg)
	}
}

func TestConfigGetJSONOutput(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", dir)

	// Set a value first
	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"config", "set", "region", "eu-west-1"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("config set error: %v", err)
	}

	// Get with --json
	buf.Reset()
	rootCmd2 := NewRootCommand()
	rootCmd2.SetOut(buf)
	rootCmd2.SetErr(buf)
	rootCmd2.SetArgs([]string{"--json", "config", "get", "region"})

	if err := rootCmd2.Execute(); err != nil {
		t.Fatalf("config get --json error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("config get --json output is not valid JSON: %v\nOutput: %s", err, buf.String())
	}

	if result["region"] != "eu-west-1" {
		t.Errorf("JSON region = %v, want eu-west-1", result["region"])
	}
}

// TestConfigGetVolumeIOPS verifies bug #50: config get volume_iops returns the
// default value (3000) rather than an empty string.
func TestConfigGetVolumeIOPS(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", dir)

	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"config", "get", "volume_iops"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("config get volume_iops error: %v", err)
	}

	output := strings.TrimSpace(buf.String())
	if output != "3000" {
		t.Errorf("config get volume_iops = %q, want %q", output, "3000")
	}
}

// TestConfigGetVolumeIOPSAfterSet verifies bug #50: config get volume_iops
// returns the updated value after config set volume_iops.
func TestConfigGetVolumeIOPSAfterSet(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", dir)

	// Set a non-default IOPS value.
	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"config", "set", "volume_iops", "6000"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("config set volume_iops error: %v", err)
	}

	// Retrieve it via config get.
	buf.Reset()
	rootCmd2 := NewRootCommand()
	rootCmd2.SetOut(buf)
	rootCmd2.SetErr(buf)
	rootCmd2.SetArgs([]string{"config", "get", "volume_iops"})

	if err := rootCmd2.Execute(); err != nil {
		t.Fatalf("config get volume_iops error: %v", err)
	}

	output := strings.TrimSpace(buf.String())
	if output != "6000" {
		t.Errorf("config get volume_iops = %q, want %q", output, "6000")
	}
}

// TestConfigGetRegionNotSetJSON verifies that config get region --json returns
// an empty string (not the human-readable sentinel "(not set)") when the region
// has never been configured, consistent with `config --json` output.
func TestConfigGetRegionNotSetJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", dir)

	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"--json", "config", "get", "region"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("config get region --json error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("config get region --json not valid JSON: %v\nOutput: %s", err, buf.String())
	}

	// JSON output must be consistent with `config --json`: empty string for unset
	// region, not the human-readable "(not set)" sentinel.
	if result["region"] != "" {
		t.Errorf("JSON region (unset) = %v, want %q (empty string, not sentinel)", result["region"], "")
	}
}

// TestConfigHumanDisplayIncludesVolumeIOPS verifies bug #49: the human-readable
// config display includes the volume_iops row.
func TestConfigHumanDisplayIncludesVolumeIOPS(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", dir)

	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"config"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("config error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "volume_iops") {
		t.Errorf("human config output missing %q row:\n%s", "volume_iops", output)
	}
	if !strings.Contains(output, "3000") {
		t.Errorf("human config output missing default IOPS value %q:\n%s", "3000", output)
	}
}

// TestConfigJSONIncludesVolumeIOPS verifies bug #49: the --json config output
// includes the volume_iops key.
func TestConfigJSONIncludesVolumeIOPS(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", dir)

	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"config", "--json"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("config --json error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("config --json not valid JSON: %v\nOutput: %s", err, buf.String())
	}

	if _, ok := result["volume_iops"]; !ok {
		t.Errorf("JSON config output missing key %q", "volume_iops")
	}
	// Default IOPS is 3000; JSON numbers unmarshal as float64.
	if result["volume_iops"] != float64(3000) {
		t.Errorf("JSON volume_iops = %v, want 3000", result["volume_iops"])
	}
}

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
		"idle_timeout_minutes",
		"ssh_config_approved",
		"m6i.xlarge",
		"50",
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

	expectedKeys := []string{"region", "instance_type", "volume_size_gb", "idle_timeout_minutes", "ssh_config_approved"}
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

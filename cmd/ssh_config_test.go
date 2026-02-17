package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSSHConfigCommand_WritesBlock(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", configDir)

	sshDir := t.TempDir()
	sshConfigPath := filepath.Join(sshDir, "config")

	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{
		"ssh-config",
		"--yes",
		"--ssh-config-path", sshConfigPath,
		"--hostname", "1.2.3.4",
	})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("ssh-config error: %v", err)
	}

	// Verify SSH config file was written.
	data, err := os.ReadFile(sshConfigPath)
	if err != nil {
		t.Fatalf("read ssh config: %v", err)
	}
	content := string(data)

	expectations := []string{
		"# mint:begin default",
		"Host mint-default",
		"HostName 1.2.3.4",
		"User ubuntu",
		"Port 41122",
		"# mint:end default",
		"# mint:checksum:",
	}
	for _, exp := range expectations {
		if !strings.Contains(content, exp) {
			t.Errorf("ssh config missing %q, got:\n%s", exp, content)
		}
	}
}

func TestSSHConfigCommand_CustomVM(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", configDir)

	sshDir := t.TempDir()
	sshConfigPath := filepath.Join(sshDir, "config")

	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{
		"--vm", "myvm",
		"ssh-config",
		"--yes",
		"--ssh-config-path", sshConfigPath,
		"--hostname", "10.0.0.5",
	})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("ssh-config error: %v", err)
	}

	data, _ := os.ReadFile(sshConfigPath)
	content := string(data)
	if !strings.Contains(content, "Host mint-myvm") {
		t.Errorf("missing custom VM name in config:\n%s", content)
	}
}

func TestSSHConfigCommand_RequiresHostname(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", configDir)

	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{
		"ssh-config",
		"--yes",
	})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("should fail without --hostname")
	}
	if !strings.Contains(err.Error(), "hostname") {
		t.Errorf("error should mention hostname: %v", err)
	}
}

func TestSSHConfigCommand_StoresApproval(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", configDir)

	sshDir := t.TempDir()
	sshConfigPath := filepath.Join(sshDir, "config")

	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{
		"ssh-config",
		"--yes",
		"--ssh-config-path", sshConfigPath,
		"--hostname", "1.2.3.4",
	})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("ssh-config error: %v", err)
	}

	// Check that approval was persisted in mint config.
	configPath := filepath.Join(configDir, "config.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read mint config: %v", err)
	}
	if !strings.Contains(string(data), "ssh_config_approved = true") {
		t.Errorf("approval not stored in config:\n%s", string(data))
	}
}

func TestSSHConfigCommand_WarnsOnHandEdits(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", configDir)

	sshDir := t.TempDir()
	sshConfigPath := filepath.Join(sshDir, "config")

	// Write a tampered block manually.
	tampered := "# mint:begin default\nHost mint-default\n    HostName 1.2.3.4\n    User root\n    Port 41122\n    StrictHostKeyChecking no\n    UserKnownHostsFile /dev/null\n# mint:end default\n# mint:checksum:0000000000000000000000000000000000000000000000000000000000000000\n"
	os.WriteFile(sshConfigPath, []byte(tampered), 0o600)

	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{
		"ssh-config",
		"--yes",
		"--ssh-config-path", sshConfigPath,
		"--hostname", "5.6.7.8",
	})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("ssh-config error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(strings.ToLower(output), "hand-edit") {
		t.Errorf("should warn about hand edits, got:\n%s", output)
	}
}

func TestSSHConfigCommand_RemoveFlag(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", configDir)

	sshDir := t.TempDir()
	sshConfigPath := filepath.Join(sshDir, "config")

	// First write a block.
	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{
		"ssh-config",
		"--yes",
		"--ssh-config-path", sshConfigPath,
		"--hostname", "1.2.3.4",
	})
	rootCmd.Execute()

	// Now remove it.
	buf.Reset()
	rootCmd2 := NewRootCommand()
	rootCmd2.SetOut(buf)
	rootCmd2.SetErr(buf)
	rootCmd2.SetArgs([]string{
		"ssh-config",
		"--remove",
		"--ssh-config-path", sshConfigPath,
	})

	err := rootCmd2.Execute()
	if err != nil {
		t.Fatalf("ssh-config --remove error: %v", err)
	}

	data, _ := os.ReadFile(sshConfigPath)
	if strings.Contains(string(data), "mint:begin") {
		t.Error("block not removed")
	}
}

func TestSSHConfigCommand_PreservesExistingContent(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MINT_CONFIG_DIR", configDir)

	sshDir := t.TempDir()
	sshConfigPath := filepath.Join(sshDir, "config")

	existing := "Host myserver\n    HostName example.com\n    User admin\n"
	os.WriteFile(sshConfigPath, []byte(existing), 0o600)

	buf := new(bytes.Buffer)
	rootCmd := NewRootCommand()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{
		"ssh-config",
		"--yes",
		"--ssh-config-path", sshConfigPath,
		"--hostname", "1.2.3.4",
	})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("ssh-config error: %v", err)
	}

	data, _ := os.ReadFile(sshConfigPath)
	content := string(data)

	if !strings.Contains(content, "Host myserver") {
		t.Error("existing content was lost")
	}
	if !strings.Contains(content, "Host mint-default") {
		t.Error("managed block was not added")
	}
}

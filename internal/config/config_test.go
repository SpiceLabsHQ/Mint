package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadReturnsDefaults(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	if cfg.Region != "" {
		t.Errorf("Region = %q, want empty string", cfg.Region)
	}
	if cfg.InstanceType != "m6i.xlarge" {
		t.Errorf("InstanceType = %q, want %q", cfg.InstanceType, "m6i.xlarge")
	}
	if cfg.VolumeSizeGB != 50 {
		t.Errorf("VolumeSizeGB = %d, want 50", cfg.VolumeSizeGB)
	}
	if cfg.IdleTimeoutMinutes != 60 {
		t.Errorf("IdleTimeoutMinutes = %d, want 60", cfg.IdleTimeoutMinutes)
	}
	if cfg.SSHConfigApproved != false {
		t.Errorf("SSHConfigApproved = %v, want false", cfg.SSHConfigApproved)
	}
}

func TestSaveAndLoad(t *testing.T) {
	dir := t.TempDir()

	cfg := &Config{
		Region:             "us-west-2",
		InstanceType:       "t3.medium",
		VolumeSizeGB:       100,
		IdleTimeoutMinutes: 120,
		SSHConfigApproved:  true,
	}

	if err := Save(cfg, dir); err != nil {
		t.Fatalf("Save() unexpected error: %v", err)
	}

	// Verify the file was created
	path := filepath.Join(dir, "config.toml")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config.toml not created: %v", err)
	}

	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	if loaded.Region != cfg.Region {
		t.Errorf("Region = %q, want %q", loaded.Region, cfg.Region)
	}
	if loaded.InstanceType != cfg.InstanceType {
		t.Errorf("InstanceType = %q, want %q", loaded.InstanceType, cfg.InstanceType)
	}
	if loaded.VolumeSizeGB != cfg.VolumeSizeGB {
		t.Errorf("VolumeSizeGB = %d, want %d", loaded.VolumeSizeGB, cfg.VolumeSizeGB)
	}
	if loaded.IdleTimeoutMinutes != cfg.IdleTimeoutMinutes {
		t.Errorf("IdleTimeoutMinutes = %d, want %d", loaded.IdleTimeoutMinutes, cfg.IdleTimeoutMinutes)
	}
	if loaded.SSHConfigApproved != cfg.SSHConfigApproved {
		t.Errorf("SSHConfigApproved = %v, want %v", loaded.SSHConfigApproved, cfg.SSHConfigApproved)
	}
}

func TestSaveCreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "config")
	cfg := &Config{
		InstanceType:       "m6i.xlarge",
		VolumeSizeGB:       50,
		IdleTimeoutMinutes: 60,
	}

	if err := Save(cfg, dir); err != nil {
		t.Fatalf("Save() should create directory, got error: %v", err)
	}

	path := filepath.Join(dir, "config.toml")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config.toml not created in nested dir: %v", err)
	}
}

func TestSetValidatesRegionFormat(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := Load(dir)

	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"valid us-west-2", "us-west-2", false},
		{"valid eu-central-1", "eu-central-1", false},
		{"valid ap-southeast-1", "ap-southeast-1", false},
		{"empty clears region", "", false},
		{"invalid no number", "us-west", true},
		{"invalid uppercase", "US-WEST-2", true},
		{"invalid random", "foobar", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := cfg.Set("region", tt.value)
			if tt.wantErr && err == nil {
				t.Errorf("Set(region, %q) expected error, got nil", tt.value)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Set(region, %q) unexpected error: %v", tt.value, err)
			}
		})
	}
}

func TestSetValidatesVolumeSizeGB(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := Load(dir)

	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"minimum 50", "50", false},
		{"above minimum", "100", false},
		{"below minimum 30", "30", true},
		{"below minimum 49", "49", true},
		{"not a number", "abc", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := cfg.Set("volume_size_gb", tt.value)
			if tt.wantErr && err == nil {
				t.Errorf("Set(volume_size_gb, %q) expected error, got nil", tt.value)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Set(volume_size_gb, %q) unexpected error: %v", tt.value, err)
			}
		})
	}
}

func TestSetValidatesIdleTimeoutMinutes(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := Load(dir)

	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"minimum 15", "15", false},
		{"above minimum", "60", false},
		{"below minimum 5", "5", true},
		{"below minimum 14", "14", true},
		{"not a number", "abc", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := cfg.Set("idle_timeout_minutes", tt.value)
			if tt.wantErr && err == nil {
				t.Errorf("Set(idle_timeout_minutes, %q) expected error, got nil", tt.value)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Set(idle_timeout_minutes, %q) unexpected error: %v", tt.value, err)
			}
		})
	}
}

func TestSetValidatesInstanceType(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := Load(dir)

	if err := cfg.Set("instance_type", "t3.micro"); err != nil {
		t.Errorf("Set(instance_type, t3.micro) unexpected error: %v", err)
	}
	if cfg.InstanceType != "t3.micro" {
		t.Errorf("InstanceType = %q, want %q", cfg.InstanceType, "t3.micro")
	}

	if err := cfg.Set("instance_type", ""); err == nil {
		t.Errorf("Set(instance_type, empty) expected error, got nil")
	}
}

func TestSetValidatesSSHConfigApproved(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := Load(dir)

	tests := []struct {
		name    string
		value   string
		wantErr bool
		want    bool
	}{
		{"true", "true", false, true},
		{"false", "false", false, false},
		{"invalid", "yes", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := cfg.Set("ssh_config_approved", tt.value)
			if tt.wantErr && err == nil {
				t.Errorf("Set(ssh_config_approved, %q) expected error", tt.value)
			}
			if !tt.wantErr {
				if err != nil {
					t.Errorf("Set(ssh_config_approved, %q) unexpected error: %v", tt.value, err)
				}
				if cfg.SSHConfigApproved != tt.want {
					t.Errorf("SSHConfigApproved = %v, want %v", cfg.SSHConfigApproved, tt.want)
				}
			}
		})
	}
}

func TestSetRejectsUnknownKey(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := Load(dir)

	err := cfg.Set("unknown_key", "foo")
	if err == nil {
		t.Fatal("Set(unknown_key) expected error, got nil")
	}
}

func TestValidKeys(t *testing.T) {
	keys := ValidKeys()
	expected := map[string]bool{
		"region":               true,
		"instance_type":        true,
		"volume_size_gb":       true,
		"idle_timeout_minutes": true,
		"ssh_config_approved":  true,
	}

	if len(keys) != len(expected) {
		t.Fatalf("ValidKeys() returned %d keys, want %d", len(keys), len(expected))
	}

	for _, k := range keys {
		if !expected[k] {
			t.Errorf("unexpected key %q in ValidKeys()", k)
		}
	}
}

func TestSaveFilePermissions(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		InstanceType:       "m6i.xlarge",
		VolumeSizeGB:       50,
		IdleTimeoutMinutes: 60,
	}

	if err := Save(cfg, dir); err != nil {
		t.Fatalf("Save() unexpected error: %v", err)
	}

	path := filepath.Join(dir, "config.toml")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat config.toml: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("config.toml permissions = %o, want 600", perm)
	}
}

func TestSetInstanceTypeWithValidator(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := Load(dir)
	cfg.Region = "us-west-2"

	// Wire a mock validator that accepts only "m6i.xlarge"
	cfg.InstanceTypeValidator = func(instanceType, region string) error {
		if instanceType == "m6i.xlarge" {
			return nil
		}
		return fmt.Errorf("instance type %q is not available in %s", instanceType, region)
	}

	// Valid type passes
	if err := cfg.Set("instance_type", "m6i.xlarge"); err != nil {
		t.Errorf("Set(instance_type, m6i.xlarge) with validator: unexpected error: %v", err)
	}
	if cfg.InstanceType != "m6i.xlarge" {
		t.Errorf("InstanceType = %q, want %q", cfg.InstanceType, "m6i.xlarge")
	}

	// Invalid type rejected by validator
	if err := cfg.Set("instance_type", "z99.nonexistent"); err == nil {
		t.Errorf("Set(instance_type, z99.nonexistent) with validator: expected error, got nil")
	}
}

func TestSetInstanceTypeWithoutValidator(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := Load(dir)

	// No validator set -- falls back to basic non-empty check
	if err := cfg.Set("instance_type", "t3.micro"); err != nil {
		t.Errorf("Set(instance_type, t3.micro) without validator: unexpected error: %v", err)
	}
	if cfg.InstanceType != "t3.micro" {
		t.Errorf("InstanceType = %q, want %q", cfg.InstanceType, "t3.micro")
	}

	// Empty still rejected
	if err := cfg.Set("instance_type", ""); err == nil {
		t.Errorf("Set(instance_type, empty) without validator: expected error, got nil")
	}
}

func TestSetInstanceTypeWithoutRegion(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := Load(dir)
	cfg.Region = "" // No region configured

	called := false
	cfg.InstanceTypeValidator = func(instanceType, region string) error {
		called = true
		return nil
	}

	// Should succeed with basic validation only, validator not called
	if err := cfg.Set("instance_type", "t3.micro"); err != nil {
		t.Errorf("Set(instance_type, t3.micro) without region: unexpected error: %v", err)
	}
	if called {
		t.Errorf("InstanceTypeValidator should not be called when region is empty")
	}
}

func TestSetAndSaveRoundtrip(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := Load(dir)

	if err := cfg.Set("region", "eu-west-1"); err != nil {
		t.Fatalf("Set(region) error: %v", err)
	}
	if err := cfg.Set("volume_size_gb", "200"); err != nil {
		t.Fatalf("Set(volume_size_gb) error: %v", err)
	}

	if err := Save(cfg, dir); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if loaded.Region != "eu-west-1" {
		t.Errorf("Region = %q, want %q", loaded.Region, "eu-west-1")
	}
	if loaded.VolumeSizeGB != 200 {
		t.Errorf("VolumeSizeGB = %d, want 200", loaded.VolumeSizeGB)
	}
}

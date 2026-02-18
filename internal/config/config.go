// Package config manages user preferences stored in ~/.config/mint/config.toml.
// Config stores only local user preferences (region, instance type, etc.).
// AWS is the source of truth for all resource state (ADR-0014).
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/viper"
)

// InstanceTypeValidatorFunc validates that an instance type exists in the
// given AWS region. The caller (cmd layer) wires this with an AWS-backed
// implementation; tests use a mock. When nil, validation falls back to a
// basic non-empty check.
type InstanceTypeValidatorFunc func(instanceType, region string) error

// Config holds user preferences from ~/.config/mint/config.toml.
// All fields use flat snake_case TOML keys per ADR-0012.
type Config struct {
	Region             string `mapstructure:"region"              toml:"region"`
	InstanceType       string `mapstructure:"instance_type"       toml:"instance_type"`
	VolumeSizeGB       int    `mapstructure:"volume_size_gb"      toml:"volume_size_gb"`
	VolumeIOPS         int    `mapstructure:"volume_iops"         toml:"volume_iops"`
	IdleTimeoutMinutes int    `mapstructure:"idle_timeout_minutes" toml:"idle_timeout_minutes"`
	SSHConfigApproved  bool   `mapstructure:"ssh_config_approved" toml:"ssh_config_approved"`

	// InstanceTypeValidator is an optional callback for AWS API validation.
	// Set by the cmd layer when an EC2 client is available. Not serialized.
	InstanceTypeValidator InstanceTypeValidatorFunc `mapstructure:"-" toml:"-"`
}

// validator is a function that validates a string value for a config key.
type validator func(value string) error

// validators maps config keys to their validation functions.
var validators = map[string]validator{
	"region":               validateRegion,
	"instance_type":        validateInstanceType,
	"volume_size_gb":       validateVolumeSizeGB,
	"volume_iops":          validateVolumeIOPS,
	"idle_timeout_minutes": validateIdleTimeoutMinutes,
	"ssh_config_approved":  validateSSHConfigApproved,
}

// ValidKeys returns the sorted list of valid config key names.
func ValidKeys() []string {
	keys := make([]string, 0, len(validators))
	for k := range validators {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// DefaultConfigDir returns the default config directory path (~/.config/mint).
// If MINT_CONFIG_DIR is set, that value is used instead.
func DefaultConfigDir() string {
	if dir := os.Getenv("MINT_CONFIG_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".config", "mint")
	}
	return filepath.Join(home, ".config", "mint")
}

// Load reads the config file from configDir/config.toml and returns a Config
// with defaults applied for any missing keys. If the file does not exist,
// all defaults are returned without error.
func Load(configDir string) (*Config, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("toml")
	v.AddConfigPath(configDir)

	// Set defaults per SPEC.md
	v.SetDefault("region", "")
	v.SetDefault("instance_type", "m6i.xlarge")
	v.SetDefault("volume_size_gb", 50)
	v.SetDefault("volume_iops", 3000)
	v.SetDefault("idle_timeout_minutes", 60)
	v.SetDefault("ssh_config_approved", false)

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			// Ignore missing file, return defaults
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("read config: %w", err)
			}
		}
	}

	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	return cfg, nil
}

// Save writes the config to configDir/config.toml, creating the directory
// if it does not exist.
func Save(cfg *Config, configDir string) error {
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	v := viper.New()
	v.Set("region", cfg.Region)
	v.Set("instance_type", cfg.InstanceType)
	v.Set("volume_size_gb", cfg.VolumeSizeGB)
	v.Set("volume_iops", cfg.VolumeIOPS)
	v.Set("idle_timeout_minutes", cfg.IdleTimeoutMinutes)
	v.Set("ssh_config_approved", cfg.SSHConfigApproved)

	path := filepath.Join(configDir, "config.toml")
	if err := v.WriteConfigAs(path); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// Set validates and applies a single key-value pair to the config.
// Returns an error if the key is unknown or the value fails validation.
func (c *Config) Set(key, value string) error {
	validate, ok := validators[key]
	if !ok {
		return fmt.Errorf("unknown config key %q; valid keys: %s", key, strings.Join(ValidKeys(), ", "))
	}

	if err := validate(value); err != nil {
		return fmt.Errorf("invalid value for %s: %w", key, err)
	}

	// For instance_type, run AWS API validation when a validator and region
	// are both available. Without a region, we cannot query a specific
	// region's instance type catalog, so we fall back to the basic check.
	if key == "instance_type" && c.InstanceTypeValidator != nil && c.Region != "" {
		if err := c.InstanceTypeValidator(value, c.Region); err != nil {
			return fmt.Errorf("invalid value for %s: %w", key, err)
		}
	}

	switch key {
	case "region":
		c.Region = value
	case "instance_type":
		c.InstanceType = value
	case "volume_size_gb":
		n, _ := strconv.Atoi(value) // already validated
		c.VolumeSizeGB = n
	case "volume_iops":
		n, _ := strconv.Atoi(value) // already validated
		c.VolumeIOPS = n
	case "idle_timeout_minutes":
		n, _ := strconv.Atoi(value) // already validated
		c.IdleTimeoutMinutes = n
	case "ssh_config_approved":
		c.SSHConfigApproved = value == "true"
	}

	return nil
}

// regionPattern matches valid AWS region formats like us-west-2, eu-central-1.
var regionPattern = regexp.MustCompile(`^[a-z]{2}-[a-z]+-\d+$`)

func validateRegion(value string) error {
	if value == "" {
		return nil // empty clears the region
	}
	if !regionPattern.MatchString(value) {
		return fmt.Errorf("%q does not match AWS region format (e.g., us-west-2)", value)
	}
	return nil
}

func validateInstanceType(value string) error {
	if value == "" {
		return fmt.Errorf("instance_type cannot be empty")
	}
	return nil
}

func validateVolumeSizeGB(value string) error {
	n, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("%q is not a valid integer", value)
	}
	if n < 50 {
		return fmt.Errorf("must be >= 50 (got %d)", n)
	}
	return nil
}

func validateVolumeIOPS(value string) error {
	n, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("%q is not a valid integer", value)
	}
	if n < 3000 {
		return fmt.Errorf("must be >= 3000 (got %d)", n)
	}
	if n > 16000 {
		return fmt.Errorf("must be <= 16000 (got %d)", n)
	}
	return nil
}

func validateIdleTimeoutMinutes(value string) error {
	n, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("%q is not a valid integer", value)
	}
	if n < 15 {
		return fmt.Errorf("must be >= 15 (got %d)", n)
	}
	return nil
}

func validateSSHConfigApproved(value string) error {
	if value != "true" && value != "false" {
		return fmt.Errorf("%q is not a valid boolean (use true or false)", value)
	}
	return nil
}

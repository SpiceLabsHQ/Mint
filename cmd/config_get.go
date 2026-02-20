package cmd

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/nicholasgasior/mint/internal/cli"
	"github.com/nicholasgasior/mint/internal/config"
	"github.com/spf13/cobra"
)

func newConfigGetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Get a configuration value",
		Long:  "Print a single configuration value from ~/.config/mint/config.toml.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]

			// Validate the key is known before loading config.
			validKeys := config.ValidKeys()
			valid := false
			for _, k := range validKeys {
				if k == key {
					valid = true
					break
				}
			}
			if !valid {
				return fmt.Errorf("unknown config key %q; valid keys: %s", key, strings.Join(validKeys, ", "))
			}

			configDir := config.DefaultConfigDir()
			cfg, err := config.Load(configDir)
			if err != nil {
				return err
			}

			value := configValue(cfg, key)

			cliCtx := cli.FromCommand(cmd)
			if cliCtx != nil && cliCtx.JSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{key: value})
			}

			fmt.Fprintln(cmd.OutOrStdout(), value)
			return nil
		},
	}
}

// configValue returns the string representation of a config field by key name.
func configValue(cfg *config.Config, key string) string {
	switch key {
	case "region":
		if cfg.Region == "" {
			return "(not set)"
		}
		return cfg.Region
	case "instance_type":
		return cfg.InstanceType
	case "volume_size_gb":
		return strconv.Itoa(cfg.VolumeSizeGB)
	case "volume_iops":
		return strconv.Itoa(cfg.VolumeIOPS)
	case "idle_timeout_minutes":
		return strconv.Itoa(cfg.IdleTimeoutMinutes)
	case "ssh_config_approved":
		return strconv.FormatBool(cfg.SSHConfigApproved)
	default:
		return ""
	}
}

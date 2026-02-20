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

			cliCtx := cli.FromCommand(cmd)
			if cliCtx != nil && cliCtx.JSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				// Use the raw config value in JSON mode (consistent with
				// `config --json`) rather than the human-readable sentinel.
				return enc.Encode(map[string]any{key: configValueRaw(cfg, key)})
			}

			fmt.Fprintln(cmd.OutOrStdout(), configValue(cfg, key))
			return nil
		},
	}
}

// configValue returns the human-readable string representation of a config
// field by key name. Unset fields use display sentinels (e.g., "(not set)").
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

// configValueRaw returns the actual config field value by key for JSON output.
// Unlike configValue it does not apply human-readable display transformations
// (e.g., returns "" for an unset region rather than "(not set)"), keeping the
// output consistent with `config --json`.
func configValueRaw(cfg *config.Config, key string) any {
	switch key {
	case "region":
		return cfg.Region
	case "instance_type":
		return cfg.InstanceType
	case "volume_size_gb":
		return cfg.VolumeSizeGB
	case "volume_iops":
		return cfg.VolumeIOPS
	case "idle_timeout_minutes":
		return cfg.IdleTimeoutMinutes
	case "ssh_config_approved":
		return cfg.SSHConfigApproved
	default:
		return nil
	}
}

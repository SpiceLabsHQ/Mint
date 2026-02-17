package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/nicholasgasior/mint/internal/cli"
	"github.com/nicholasgasior/mint/internal/config"
	"github.com/spf13/cobra"
)

func newConfigCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Display current configuration",
		Long:  "Display all mint configuration values. Uses ~/.config/mint/config.toml.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir := config.DefaultConfigDir()
			cfg, err := config.Load(configDir)
			if err != nil {
				return err
			}

			cliCtx := cli.FromCommand(cmd)
			if cliCtx != nil && cliCtx.JSON {
				return printConfigJSON(cmd, cfg)
			}

			return printConfigHuman(cmd, cfg)
		},
	}

	cmd.AddCommand(newConfigSetCommand())

	return cmd
}

func printConfigJSON(cmd *cobra.Command, cfg *config.Config) error {
	data := map[string]any{
		"region":               cfg.Region,
		"instance_type":        cfg.InstanceType,
		"volume_size_gb":       cfg.VolumeSizeGB,
		"idle_timeout_minutes": cfg.IdleTimeoutMinutes,
		"ssh_config_approved":  cfg.SSHConfigApproved,
	}

	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(data)
}

func printConfigHuman(cmd *cobra.Command, cfg *config.Config) error {
	w := cmd.OutOrStdout()

	region := cfg.Region
	if region == "" {
		region = "(not set)"
	}

	_, err := fmt.Fprintf(w,
		"region               %s\n"+
			"instance_type        %s\n"+
			"volume_size_gb       %d\n"+
			"idle_timeout_minutes %d\n"+
			"ssh_config_approved  %v\n",
		region,
		cfg.InstanceType,
		cfg.VolumeSizeGB,
		cfg.IdleTimeoutMinutes,
		cfg.SSHConfigApproved,
	)
	return err
}

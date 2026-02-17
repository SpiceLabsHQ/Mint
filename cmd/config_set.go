package cmd

import (
	"fmt"

	"github.com/nicholasgasior/mint/internal/config"
	"github.com/spf13/cobra"
)

func newConfigSetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a configuration value",
		Long:  "Validate and set a single configuration key in ~/.config/mint/config.toml.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, value := args[0], args[1]

			configDir := config.DefaultConfigDir()
			cfg, err := config.Load(configDir)
			if err != nil {
				return err
			}

			if err := cfg.Set(key, value); err != nil {
				return err
			}

			if err := config.Save(cfg, configDir); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Set %s = %s\n", key, value)
			return nil
		},
	}
}

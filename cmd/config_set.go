package cmd

import (
	"context"
	"fmt"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	mintaws "github.com/nicholasgasior/mint/internal/aws"
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

			// Wire the real EC2 instance type validator when a region is
			// available. Without a region we cannot query a specific region's
			// instance type catalog, so cfg.Set falls back to its basic check.
			if cfg.Region != "" {
				awsCfg, err := awsconfig.LoadDefaultConfig(
					context.Background(),
					awsconfig.WithRegion(cfg.Region),
				)
				if err == nil {
					ec2Client := ec2.NewFromConfig(awsCfg)
					validator := mintaws.NewInstanceTypeValidator(ec2Client)
					cfg.InstanceTypeValidator = func(instanceType, region string) error {
						return validator.Validate(context.Background(), instanceType, region)
					}
				}
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

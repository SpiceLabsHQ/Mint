package cmd

import (
	"context"
	"fmt"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	mintaws "github.com/nicholasgasior/mint/internal/aws"
	"github.com/nicholasgasior/mint/internal/config"
	"github.com/spf13/cobra"
)

// instanceTypeValidatorOverride allows tests to inject a mock validator
// without calling real AWS APIs. When non-nil it replaces the real EC2
// validator wired in newConfigSetCommand.
var instanceTypeValidatorOverride config.InstanceTypeValidatorFunc

// credentialErrorKeywords are substrings found in AWS SDK credential errors.
// When any of these appear in an instance_type validation error we replace the
// raw SDK chain with a single actionable message.
var credentialErrorKeywords = []string{
	"get credentials",
	"NoCredentialProviders",
	"no EC2 IMDS role found",
	"failed to refresh cached credentials",
	"credential",
}

// isCredentialError reports whether err looks like an AWS credential failure.
func isCredentialError(err error) bool {
	msg := err.Error()
	for _, kw := range credentialErrorKeywords {
		if strings.Contains(msg, kw) {
			return true
		}
	}
	return false
}

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

			// Wire the instance type validator. Tests may inject a mock via
			// instanceTypeValidatorOverride; production code uses the real
			// EC2 client when a region is available.
			if instanceTypeValidatorOverride != nil {
				cfg.InstanceTypeValidator = instanceTypeValidatorOverride
			} else if cfg.Region != "" {
				// Wire the real EC2 instance type validator when a region is
				// available. Without a region we cannot query a specific
				// region's instance type catalog, so cfg.Set falls back to
				// its basic check.
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
				// When the instance_type validator fails due to missing or broken
				// AWS credentials, the raw SDK error chain (IMDS retry counts,
				// internal endpoint URLs, TCP details) is not actionable. Replace
				// it with a single friendly message so the user knows exactly
				// what to do.
				if key == "instance_type" && isCredentialError(err) {
					return fmt.Errorf(`cannot validate instance type: AWS credentials unavailable — run "aws configure" or set AWS_PROFILE`)
				}
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

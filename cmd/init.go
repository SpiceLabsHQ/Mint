package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/efs"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/nicholasgasior/mint/internal/cli"
	"github.com/nicholasgasior/mint/internal/identity"
	"github.com/nicholasgasior/mint/internal/provision"
	"github.com/spf13/cobra"
)

func newInitCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize mint for the current user",
		Long: "Validate prerequisites (default VPC, admin EFS) and create per-user " +
			"resources (security group, EFS access point). Safe to run multiple times — " +
			"existing resources are detected and skipped.",
		Args: cobra.NoArgs,
		RunE: runInit,
	}
}

func runInit(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	cliCtx := cli.FromCommand(cmd)

	// Load AWS config.
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}

	// Resolve owner identity (ADR-0013).
	stsClient := sts.NewFromConfig(cfg)
	resolver := identity.NewResolver(stsClient)
	owner, err := resolver.Resolve(ctx)
	if err != nil {
		return fmt.Errorf("resolve identity: %w", err)
	}

	vmName := "default"
	if cliCtx != nil && cliCtx.VM != "" {
		vmName = cliCtx.VM
	}

	// Wire up AWS clients and run init.
	ec2Client := ec2.NewFromConfig(cfg)
	efsClient := efs.NewFromConfig(cfg)
	iamClient := iam.NewFromConfig(cfg)

	initializer := provision.NewInitializer(
		ec2Client, // DescribeVpcsAPI
		ec2Client, // DescribeSubnetsAPI
		efsClient, // DescribeFileSystemsAPI
		iamClient, // GetInstanceProfileAPI
		ec2Client, // DescribeSecurityGroupsAPI
		ec2Client, // CreateSecurityGroupAPI
		ec2Client, // AuthorizeSecurityGroupIngressAPI
		ec2Client, // CreateTagsAPI
		efsClient, // DescribeAccessPointsAPI
		efsClient, // CreateAccessPointAPI
	)

	result, err := initializer.Run(ctx, owner.Name, owner.ARN, vmName)
	if err != nil {
		return err
	}

	return printInitResult(cmd, cliCtx, result)
}

func printInitResult(cmd *cobra.Command, cliCtx *cli.CLIContext, result *provision.InitResult) error {
	if cliCtx != nil && cliCtx.JSON {
		return printInitJSON(cmd, result)
	}
	return printInitHuman(cmd, result)
}

func printInitJSON(cmd *cobra.Command, result *provision.InitResult) error {
	data := map[string]any{
		"vpc_id":           result.VPCID,
		"efs_id":           result.EFSID,
		"security_group":   result.SecurityGroup,
		"sg_created":       result.SGCreated,
		"access_point_id":  result.AccessPointID,
		"ap_created":       result.APCreated,
	}

	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(data)
}

func printInitHuman(cmd *cobra.Command, result *provision.InitResult) error {
	w := cmd.OutOrStdout()

	fmt.Fprintf(w, "VPC           %s\n", result.VPCID)
	fmt.Fprintf(w, "EFS           %s\n", result.EFSID)

	if result.SGCreated {
		fmt.Fprintf(w, "Security group %s (created)\n", result.SecurityGroup)
	} else {
		fmt.Fprintf(w, "Security group %s (exists)\n", result.SecurityGroup)
	}

	if result.APCreated {
		fmt.Fprintf(w, "Access point  %s (created)\n", result.AccessPointID)
	} else {
		fmt.Fprintf(w, "Access point  %s (exists)\n", result.AccessPointID)
	}

	fmt.Fprintln(w, "\nInitialization complete.")
	return nil
}

// initWithInitializer runs init with a pre-built Initializer (for testing).
func initWithInitializer(ctx context.Context, cmd *cobra.Command, cliCtx *cli.CLIContext, initializer *provision.Initializer, owner, ownerARN, vmName string) error {
	result, err := initializer.Run(ctx, owner, ownerARN, vmName)
	if err != nil {
		return err
	}
	return printInitResult(cmd, cliCtx, result)
}

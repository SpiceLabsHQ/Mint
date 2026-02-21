package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/iam"

	"github.com/nicholasgasior/mint/internal/cli"
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

	// Use the shared AWS clients initialized by PersistentPreRunE.  This
	// avoids a second STS GetCallerIdentity call and ensures the --debug
	// flag is honoured for all AWS API calls made by init.
	clients := awsClientsFromContext(ctx)
	if clients == nil {
		return fmt.Errorf("AWS clients not configured")
	}

	vmName := "default"
	if cliCtx != nil && cliCtx.VM != "" {
		vmName = cliCtx.VM
	}

	// IAM is not included in the shared awsClients (it is only needed by
	// mint init).  Create it from the default config; credentials were
	// already validated by PersistentPreRunE so this is just client wiring.
	var awsOpts []func(*awsconfig.LoadOptions) error

	// Pass --profile so the IAM client uses the same AWS profile as the
	// rest of the mint command invocation.
	if cliCtx != nil && cliCtx.Profile != "" {
		awsOpts = append(awsOpts, awsconfig.WithSharedConfigProfile(cliCtx.Profile))
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsOpts...)
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}
	iamClient := iam.NewFromConfig(awsCfg)

	initializer := provision.NewInitializer(
		clients.ec2Client, // DescribeVpcsAPI
		clients.ec2Client, // DescribeSubnetsAPI
		clients.efsClient, // DescribeFileSystemsAPI
		iamClient,         // GetInstanceProfileAPI
		clients.ec2Client, // DescribeSecurityGroupsAPI
		clients.ec2Client, // CreateSecurityGroupAPI
		clients.ec2Client, // AuthorizeSecurityGroupIngressAPI
		clients.ec2Client, // CreateTagsAPI
		clients.efsClient, // DescribeAccessPointsAPI
		clients.efsClient, // CreateAccessPointAPI
	)

	result, err := initializer.Run(ctx, clients.owner, clients.ownerARN, vmName)
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
		"vpc_id":          result.VPCID,
		"efs_id":          result.EFSID,
		"security_group":  result.SecurityGroup,
		"sg_created":      result.SGCreated,
		"access_point_id": result.AccessPointID,
		"ap_created":      result.APCreated,
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

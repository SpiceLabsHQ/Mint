package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/nicholasgasior/mint/internal/admin"
	mintaws "github.com/nicholasgasior/mint/internal/aws"
	"github.com/nicholasgasior/mint/internal/cli"
)

// adminDeployDeps holds the injectable dependencies for the admin deploy command.
type adminDeployDeps struct {
	cfnCreate       mintaws.CreateStackAPI
	cfnUpdate       mintaws.UpdateStackAPI
	cfnDescribe     mintaws.DescribeStacksAPI
	cfnEvents       mintaws.DescribeStackEventsAPI
	ec2DescribeVPCs    mintaws.DescribeVpcsAPI
	ec2DescribeSubnets mintaws.DescribeSubnetsAPI
}

// newAdminDeployCommand creates the production admin deploy command.
func newAdminDeployCommand() *cobra.Command {
	return newAdminDeployCommandWithDeps(nil)
}

// newAdminDeployCommandWithDeps creates the admin deploy command with explicit
// dependencies for testing.
func newAdminDeployCommandWithDeps(deps *adminDeployDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy the Mint admin CloudFormation stack",
		Long: "Deploy the Mint admin CloudFormation stack which provisions shared IAM, " +
			"EFS, and security group infrastructure required for mint init.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps != nil {
				return runAdminDeploy(cmd, deps)
			}
			clients := awsClientsFromContext(cmd.Context())
			if clients == nil {
				return fmt.Errorf("AWS clients not configured")
			}
			return runAdminDeploy(cmd, &adminDeployDeps{
				cfnCreate:          clients.cfnClient,
				cfnUpdate:          clients.cfnClient,
				cfnDescribe:        clients.cfnClient,
				cfnEvents:          clients.cfnClient,
				ec2DescribeVPCs:    clients.ec2Client,
				ec2DescribeSubnets: clients.ec2Client,
			})
		},
	}

	cmd.Flags().String("stack-name", "mint-admin-setup", "CloudFormation stack name")

	return cmd
}

// runAdminDeploy executes the admin deploy logic.
func runAdminDeploy(cmd *cobra.Command, deps *adminDeployDeps) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	cliCtx := cli.FromCommand(cmd)
	jsonOutput := false
	if cliCtx != nil {
		jsonOutput = cliCtx.JSON
	}

	stackName, _ := cmd.Flags().GetString("stack-name")

	fmt.Fprintln(cmd.ErrOrStderr(), "Deploying Mint admin CloudFormation stack...")

	deployer := admin.NewDeployer(
		deps.cfnCreate,
		deps.cfnUpdate,
		deps.cfnDescribe,
		deps.cfnEvents,
		deps.ec2DescribeVPCs,
		deps.ec2DescribeSubnets,
	)

	result, err := deployer.Deploy(ctx, admin.DeployOptions{
		StackName:   stackName,
		EventWriter: cmd.ErrOrStderr(),
	})
	if err != nil {
		if errors.Is(err, admin.ErrVPCNotFound) {
			fmt.Fprintf(cmd.ErrOrStderr(), "Error: %v\n", err)
			return err
		}
		return fmt.Errorf("deploying admin stack: %w", err)
	}

	if jsonOutput {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "Stack deployed successfully.\n")
	fmt.Fprintf(w, "  EFS File System ID:   %s\n", result.EfsFileSystemId)
	fmt.Fprintf(w, "  EFS Security Group:   %s\n", result.EfsSecurityGroupId)
	fmt.Fprintf(w, "  Instance Profile ARN: %s\n", result.InstanceProfileArn)
	fmt.Fprintf(w, "  Pass-Role Policy ARN: %s\n", result.PassRolePolicyArn)
	return nil
}

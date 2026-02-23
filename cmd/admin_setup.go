package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/nicholasgasior/mint/internal/admin"
	"github.com/nicholasgasior/mint/internal/cli"
)

// adminSetupDeps holds the injectable dependencies for the admin setup command.
// It composes the deps for the two sub-steps: deploy and attach-policy.
type adminSetupDeps struct {
	deploy       *adminDeployDeps
	attachPolicy *adminAttachPolicyDeps
}

// SetupResult is the combined JSON output from mint admin setup.
type SetupResult struct {
	Deploy       *admin.DeployResult  `json:"deploy"`
	AttachPolicy *admin.AttachResult  `json:"attach_policy,omitempty"`
}

// newAdminSetupCommand creates the production admin setup command.
func newAdminSetupCommand() *cobra.Command {
	return newAdminSetupCommandWithDeps(nil)
}

// newAdminSetupCommandWithDeps creates the admin setup command with explicit
// dependencies for testing.
func newAdminSetupCommandWithDeps(deps *adminSetupDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Run admin deploy then attach-policy in sequence",
		Long: "Run the full Mint admin setup in one step: deploy the CloudFormation " +
			"stack and attach the customer-managed IAM policy to the IAM Identity Center " +
			"permission set. Equivalent to running 'admin deploy' then 'admin attach-policy'.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps != nil {
				return runAdminSetup(cmd, deps)
			}
			clients := awsClientsFromContext(cmd.Context())
			if clients == nil {
				return fmt.Errorf("AWS clients not configured")
			}
			return runAdminSetup(cmd, &adminSetupDeps{
				deploy: &adminDeployDeps{
					cfnCreate:          clients.cfnClient,
					cfnUpdate:          clients.cfnClient,
					cfnDelete:          clients.cfnClient,
					cfnDescribe:        clients.cfnClient,
					cfnEvents:          clients.cfnClient,
					ec2DescribeVPCs:    clients.ec2Client,
					ec2DescribeSubnets: clients.ec2Client,
				},
				attachPolicy: &adminAttachPolicyDeps{
					ssoListInstances:   clients.ssoAdminClient,
					ssoListPermSets:    clients.ssoAdminClient,
					ssoDescribePermSet: clients.ssoAdminClient,
					ssoAttachPolicy:    clients.ssoAdminClient,
					ssoProvision:       clients.ssoAdminClient,
				},
			})
		},
	}

	cmd.Flags().String("stack-name", "mint-admin-setup", "CloudFormation stack name")
	cmd.Flags().String("permission-set", "PowerUserAccess", "IAM Identity Center permission set name")
	cmd.Flags().String("policy", "mint-pass-instance-role", "Customer managed policy name to attach")

	return cmd
}

// runAdminSetup executes the composite admin setup logic.
func runAdminSetup(cmd *cobra.Command, deps *adminSetupDeps) error {
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
	permSet, _ := cmd.Flags().GetString("permission-set")
	policy, _ := cmd.Flags().GetString("policy")

	// Step 1: admin deploy.
	fmt.Fprintln(cmd.ErrOrStderr(), "Running admin deploy...")

	deployer := admin.NewDeployer(
		deps.deploy.cfnCreate,
		deps.deploy.cfnUpdate,
		deps.deploy.cfnDelete,
		deps.deploy.cfnDescribe,
		deps.deploy.cfnEvents,
		deps.deploy.ec2DescribeVPCs,
		deps.deploy.ec2DescribeSubnets,
	)

	deployResult, err := deployer.Deploy(ctx, admin.DeployOptions{
		StackName:   stackName,
		EventWriter: cmd.ErrOrStderr(),
	})
	if err != nil {
		if errors.Is(err, admin.ErrVPCNotFound) {
			fmt.Fprintf(cmd.ErrOrStderr(), "Error: %v\n", err)
			return err
		}
		return fmt.Errorf("admin deploy: %w", err)
	}

	// Step 2: admin attach-policy.
	fmt.Fprintln(cmd.ErrOrStderr(), "Running admin attach-policy...")

	attacher := admin.NewPolicyAttacher(
		deps.attachPolicy.ssoListInstances,
		deps.attachPolicy.ssoListPermSets,
		deps.attachPolicy.ssoDescribePermSet,
		deps.attachPolicy.ssoAttachPolicy,
		deps.attachPolicy.ssoProvision,
	)

	attachResult, err := attacher.Attach(ctx, admin.AttachOptions{
		PermissionSetName: permSet,
		PolicyName:        policy,
	})
	if err != nil {
		if errors.Is(err, admin.ErrNoSSOInstance) {
			fmt.Fprintln(cmd.ErrOrStderr(), "IAM Identity Center not configured for this account — skipping policy attachment")
			// Graceful skip: output deploy-only result.
			if jsonOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(SetupResult{Deploy: deployResult})
			}
			printDeployResult(cmd, deployResult)
			return nil
		}
		return fmt.Errorf("admin attach-policy: %w", err)
	}

	// Both steps succeeded.
	if jsonOutput {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(SetupResult{Deploy: deployResult, AttachPolicy: attachResult})
	}

	printDeployResult(cmd, deployResult)
	printAttachResult(cmd, attachResult)
	return nil
}

// printDeployResult writes the human-readable deploy output.
func printDeployResult(cmd *cobra.Command, result *admin.DeployResult) {
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "Stack deployed successfully.\n")
	fmt.Fprintf(w, "  EFS File System ID:   %s\n", result.EfsFileSystemId)
	fmt.Fprintf(w, "  EFS Security Group:   %s\n", result.EfsSecurityGroupId)
	fmt.Fprintf(w, "  Instance Profile ARN: %s\n", result.InstanceProfileArn)
	fmt.Fprintf(w, "  Pass-Role Policy ARN: %s\n", result.PassRolePolicyArn)
}

// printAttachResult writes the human-readable attach-policy output.
func printAttachResult(cmd *cobra.Command, result *admin.AttachResult) {
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "Policy attached successfully.\n")
	fmt.Fprintf(w, "  Permission Set ARN: %s\n", result.PermissionSetArn)
	fmt.Fprintf(w, "  Provisioning Status: %s\n", result.ProvisioningStatus)
}

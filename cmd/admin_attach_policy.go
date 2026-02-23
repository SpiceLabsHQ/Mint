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

// adminAttachPolicyDeps holds the injectable dependencies for the admin attach-policy command.
type adminAttachPolicyDeps struct {
	ssoListInstances   mintaws.ListSSOInstancesAPI
	ssoListPermSets    mintaws.ListPermissionSetsAPI
	ssoDescribePermSet mintaws.DescribePermissionSetAPI
	ssoAttachPolicy    mintaws.AttachCustomerManagedPolicyReferenceAPI
	ssoProvision       mintaws.ProvisionPermissionSetAPI
}

// newAdminAttachPolicyCommand creates the production admin attach-policy command.
func newAdminAttachPolicyCommand() *cobra.Command {
	return newAdminAttachPolicyCommandWithDeps(nil)
}

// newAdminAttachPolicyCommandWithDeps creates the admin attach-policy command with
// explicit dependencies for testing.
func newAdminAttachPolicyCommandWithDeps(deps *adminAttachPolicyDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attach-policy",
		Short: "Attach a customer-managed policy to an IAM Identity Center permission set",
		Long: "Attach a customer-managed IAM policy to an IAM Identity Center permission set " +
			"and reprovision it across all assigned accounts.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps != nil {
				return runAdminAttachPolicy(cmd, deps)
			}
			clients := awsClientsFromContext(cmd.Context())
			if clients == nil {
				return fmt.Errorf("AWS clients not configured")
			}
			return runAdminAttachPolicy(cmd, &adminAttachPolicyDeps{
				ssoListInstances:   clients.ssoAdminClient,
				ssoListPermSets:    clients.ssoAdminClient,
				ssoDescribePermSet: clients.ssoAdminClient,
				ssoAttachPolicy:    clients.ssoAdminClient,
				ssoProvision:       clients.ssoAdminClient,
			})
		},
	}

	cmd.Flags().String("permission-set", "PowerUserAccess", "IAM Identity Center permission set name")
	cmd.Flags().String("policy", "mint-pass-instance-role", "Customer managed policy name to attach")

	return cmd
}

// runAdminAttachPolicy executes the admin attach-policy logic.
func runAdminAttachPolicy(cmd *cobra.Command, deps *adminAttachPolicyDeps) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	cliCtx := cli.FromCommand(cmd)
	jsonOutput := false
	if cliCtx != nil {
		jsonOutput = cliCtx.JSON
	}

	permSet, _ := cmd.Flags().GetString("permission-set")
	policy, _ := cmd.Flags().GetString("policy")

	fmt.Fprintln(cmd.ErrOrStderr(), "Attaching policy to permission set...")

	attacher := admin.NewPolicyAttacher(
		deps.ssoListInstances,
		deps.ssoListPermSets,
		deps.ssoDescribePermSet,
		deps.ssoAttachPolicy,
		deps.ssoProvision,
	)

	result, err := attacher.Attach(ctx, admin.AttachOptions{
		PermissionSetName: permSet,
		PolicyName:        policy,
	})
	if err != nil {
		if errors.Is(err, admin.ErrNoSSOInstance) {
			fmt.Fprintln(cmd.ErrOrStderr(), "IAM Identity Center not configured for this account — skipping policy attachment")
			return nil
		}
		return fmt.Errorf("attaching policy: %w", err)
	}

	if jsonOutput {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Policy attached successfully.\n")
	fmt.Fprintf(cmd.OutOrStdout(), "  Permission Set ARN: %s\n", result.PermissionSetArn)
	fmt.Fprintf(cmd.OutOrStdout(), "  Provisioning Status: %s\n", result.ProvisioningStatus)
	return nil
}

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ssoadmin"
	ssoadmintypes "github.com/aws/aws-sdk-go-v2/service/ssoadmin/types"
	"github.com/nicholasgasior/mint/internal/cli"
	"github.com/spf13/cobra"
)

// ---------------------------------------------------------------------------
// Helper: minimal root command for admin setup tests
// ---------------------------------------------------------------------------

func newTestRootForAdminSetup(deps *adminSetupDeps) *cobra.Command {
	root := &cobra.Command{
		Use:           "mint",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			cliCtx := cli.NewCLIContext(cmd)
			cmd.SetContext(cli.WithContext(context.Background(), cliCtx))
			return nil
		},
	}
	root.PersistentFlags().Bool("verbose", false, "")
	root.PersistentFlags().Bool("debug", false, "")
	root.PersistentFlags().Bool("json", false, "")
	root.PersistentFlags().Bool("yes", false, "")
	root.PersistentFlags().String("vm", "default", "")
	root.PersistentFlags().String("profile", "", "")

	adminCmd := newAdminCommandWithSetupDeps(deps)
	root.AddCommand(adminCmd)
	return root
}

// buildSetupDeps builds a fully wired adminSetupDeps from deploy and attach-policy deps.
func buildSetupDeps(deployDeps *adminDeployDeps, attachPolicyDeps *adminAttachPolicyDeps) *adminSetupDeps {
	return &adminSetupDeps{
		deploy:       deployDeps,
		attachPolicy: attachPolicyDeps,
	}
}

// newSuccessDeployDeps returns adminDeployDeps where all operations succeed.
func newSuccessDeployDeps(stackName, efsID, sgID, instanceProfileARN, passRoleARN string) *adminDeployDeps {
	return &adminDeployDeps{
		cfnCreate:          &mockCFNCreate{output: &cloudformation.CreateStackOutput{}},
		cfnUpdate:          &mockCFNUpdate{output: &cloudformation.UpdateStackOutput{}},
		cfnDelete:          noopCFNDelete(),
		cfnDescribe:        newStackSuccessDescribe(stackName, efsID, sgID, instanceProfileARN, passRoleARN),
		cfnEvents:          &mockCFNEvents{output: &cloudformation.DescribeStackEventsOutput{}},
		ec2DescribeVPCs:    &mockEC2DescribeVPCs{output: makeVPCOutput("vpc-111")},
		ec2DescribeSubnets: &mockEC2DescribeSubnets{output: makeSubnetOutput("subnet-aaa", "subnet-bbb")},
	}
}

// newSuccessAttachPolicyDeps returns adminAttachPolicyDeps where all operations succeed.
func newSuccessAttachPolicyDeps(instanceARN, permSetARN, permSetName string) *adminAttachPolicyDeps {
	return &adminAttachPolicyDeps{
		ssoListInstances: &mockSSOListInstances{
			output: &ssoadmin.ListInstancesOutput{
				Instances: []ssoadmintypes.InstanceMetadata{
					{InstanceArn: aws.String(instanceARN)},
				},
			},
		},
		ssoListPermSets: &mockSSOListPermSets{
			output: &ssoadmin.ListPermissionSetsOutput{
				PermissionSets: []string{permSetARN},
			},
		},
		ssoDescribePermSet: &mockSSODescribePermSet{
			output: &ssoadmin.DescribePermissionSetOutput{
				PermissionSet: &ssoadmintypes.PermissionSet{
					PermissionSetArn: aws.String(permSetARN),
					Name:             aws.String(permSetName),
				},
			},
		},
		ssoAttachPolicy: &mockSSOAttachPolicy{
			output: &ssoadmin.AttachCustomerManagedPolicyReferenceToPermissionSetOutput{},
		},
		ssoProvision: &mockSSOProvision{
			output: &ssoadmin.ProvisionPermissionSetOutput{
				PermissionSetProvisioningStatus: &ssoadmintypes.PermissionSetProvisioningStatus{
					Status: ssoadmintypes.StatusValuesSucceeded,
				},
			},
		},
	}
}

// newNoSSOAttachPolicyDeps returns adminAttachPolicyDeps that simulate no SSO instance.
func newNoSSOAttachPolicyDeps() *adminAttachPolicyDeps {
	return &adminAttachPolicyDeps{
		ssoListInstances:   &mockSSOListInstances{output: &ssoadmin.ListInstancesOutput{Instances: []ssoadmintypes.InstanceMetadata{}}},
		ssoListPermSets:    &mockSSOListPermSets{},
		ssoDescribePermSet: &mockSSODescribePermSet{},
		ssoAttachPolicy:    &mockSSOAttachPolicy{},
		ssoProvision:       &mockSSOProvision{},
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestAdminSetupSuccess verifies that when both deploy and attach-policy succeed,
// the JSON output contains both results.
func TestAdminSetupSuccess(t *testing.T) {
	const (
		stackName          = "mint-admin-setup"
		efsID              = "fs-setup-abc"
		sgID               = "sg-setup-def"
		instanceProfileARN = "arn:aws:iam::123456789012:instance-profile/mint-setup"
		passRoleARN        = "arn:aws:iam::123456789012:policy/mint-pass-instance-role"
		instanceARN        = "arn:aws:sso:::instance/ssoins-setup"
		permSetARN         = "arn:aws:sso:::permissionSet/ssoins-setup/ps-setup"
		permSetName        = "PowerUserAccess"
	)

	deployDeps := newSuccessDeployDeps(stackName, efsID, sgID, instanceProfileARN, passRoleARN)
	attachDeps := newSuccessAttachPolicyDeps(instanceARN, permSetARN, permSetName)

	var stdout bytes.Buffer
	root := newTestRootForAdminSetup(buildSetupDeps(deployDeps, attachDeps))
	root.SetOut(&stdout)

	root.SetArgs([]string{"admin", "setup", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stdout.String()

	// Verify it is valid JSON.
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &result); err != nil {
		t.Errorf("output is not valid JSON: %v\noutput: %s", err, out)
	}

	// Verify the JSON contains deploy result fields.
	if !strings.Contains(out, efsID) {
		t.Errorf("expected JSON to contain EFS ID %q, got: %s", efsID, out)
	}
	if !strings.Contains(out, instanceProfileARN) {
		t.Errorf("expected JSON to contain instance profile ARN, got: %s", out)
	}

	// Verify the JSON contains attach-policy result fields.
	if !strings.Contains(out, permSetARN) {
		t.Errorf("expected JSON to contain permission set ARN %q, got: %s", permSetARN, out)
	}

	// Verify both top-level keys are present.
	if _, ok := result["deploy"]; !ok {
		t.Errorf("expected JSON to have 'deploy' key, got: %v", result)
	}
	if _, ok := result["attach_policy"]; !ok {
		t.Errorf("expected JSON to have 'attach_policy' key, got: %v", result)
	}
}

// TestAdminSetupNoSSO verifies that when attach-policy returns ErrNoSSOInstance,
// setup still succeeds and the deploy result is present, while attach_policy is
// omitted (nil/null) from the JSON output.
func TestAdminSetupNoSSO(t *testing.T) {
	const (
		stackName          = "mint-admin-setup"
		efsID              = "fs-nosso-abc"
		sgID               = "sg-nosso-def"
		instanceProfileARN = "arn:aws:iam::123456789012:instance-profile/mint-nosso"
		passRoleARN        = "arn:aws:iam::123456789012:policy/mint-pass-instance-role-nosso"
	)

	deployDeps := newSuccessDeployDeps(stackName, efsID, sgID, instanceProfileARN, passRoleARN)
	attachDeps := newNoSSOAttachPolicyDeps()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	root := newTestRootForAdminSetup(buildSetupDeps(deployDeps, attachDeps))
	root.SetOut(&stdout)
	root.SetErr(&stderr)

	root.SetArgs([]string{"admin", "setup", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("expected graceful skip for ErrNoSSOInstance, got error: %v", err)
	}

	out := stdout.String()

	// Verify it is valid JSON.
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &result); err != nil {
		t.Errorf("output is not valid JSON: %v\noutput: %s", err, out)
	}

	// Deploy result must be present.
	if _, ok := result["deploy"]; !ok {
		t.Errorf("expected JSON to have 'deploy' key, got: %v", result)
	}
	if !strings.Contains(out, efsID) {
		t.Errorf("expected JSON to contain EFS ID %q, got: %s", efsID, out)
	}

	// attach_policy should be absent or null when SSO is not configured.
	if val, ok := result["attach_policy"]; ok && val != nil {
		t.Errorf("expected 'attach_policy' to be absent or null when SSO not configured, got: %v", val)
	}

	// Stderr should mention the graceful skip.
	errOut := stderr.String()
	if !strings.Contains(errOut, "IAM Identity Center") {
		t.Errorf("expected stderr to mention IAM Identity Center, got: %s", errOut)
	}
}

// TestAdminSetupDeployFails verifies that when deploy returns an error,
// setup returns that error without calling attach-policy.
func TestAdminSetupDeployFails(t *testing.T) {
	// Deploy will fail because no default VPC exists (empty Vpcs slice = ErrVPCNotFound).
	deployDeps := &adminDeployDeps{
		cfnCreate:   &mockCFNCreate{},
		cfnUpdate:   &mockCFNUpdate{},
		cfnDelete:   noopCFNDelete(),
		cfnDescribe: &mockCFNDescribe{outputs: []*cloudformation.DescribeStacksOutput{makeDescribeStacksNotFound()}},
		cfnEvents:   &mockCFNEvents{},
		// Empty VPC slice triggers ErrVPCNotFound immediately.
		ec2DescribeVPCs:    &mockEC2DescribeVPCs{output: &ec2.DescribeVpcsOutput{Vpcs: nil}},
		ec2DescribeSubnets: &mockEC2DescribeSubnets{output: makeSubnetOutput()},
	}

	// Track whether attach-policy was called.
	attachPolicyCalled := false
	attachDeps := &adminAttachPolicyDeps{
		ssoListInstances: &callTrackingSSOListInstances{
			onCall: func() { attachPolicyCalled = true },
			output: &ssoadmin.ListInstancesOutput{},
		},
		ssoListPermSets:    &mockSSOListPermSets{},
		ssoDescribePermSet: &mockSSODescribePermSet{},
		ssoAttachPolicy:    &mockSSOAttachPolicy{},
		ssoProvision:       &mockSSOProvision{},
	}

	root := newTestRootForAdminSetup(buildSetupDeps(deployDeps, attachDeps))
	root.SetArgs([]string{"admin", "setup"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error when deploy fails, got nil")
	}

	if attachPolicyCalled {
		t.Error("expected attach-policy not to be called when deploy fails, but it was called")
	}
}

// ---------------------------------------------------------------------------
// callTrackingSSOListInstances tracks whether ListInstances was called.
// ---------------------------------------------------------------------------

type callTrackingSSOListInstances struct {
	onCall func()
	output *ssoadmin.ListInstancesOutput
	err    error
}

func (m *callTrackingSSOListInstances) ListInstances(ctx context.Context, params *ssoadmin.ListInstancesInput, optFns ...func(*ssoadmin.Options)) (*ssoadmin.ListInstancesOutput, error) {
	if m.onCall != nil {
		m.onCall()
	}
	return m.output, m.err
}

// TestAdminSetupDeployFailsWithEmptyVPC verifies that when VPC has empty ID,
// setup returns an error. This is an additional edge case for robustness.
func TestAdminSetupDeployFailsWithEmptyVPC(t *testing.T) {
	deployDeps := &adminDeployDeps{
		cfnCreate:   &mockCFNCreate{},
		cfnUpdate:   &mockCFNUpdate{},
		cfnDelete:   noopCFNDelete(),
		cfnDescribe: &mockCFNDescribe{outputs: []*cloudformation.DescribeStacksOutput{makeDescribeStacksNotFound()}},
		cfnEvents:   &mockCFNEvents{},
		ec2DescribeVPCs: &mockEC2DescribeVPCs{
			err: errors.New("simulated EC2 describe VPCs failure"),
		},
		ec2DescribeSubnets: &mockEC2DescribeSubnets{output: makeSubnetOutput()},
	}

	attachDeps := newNoSSOAttachPolicyDeps()

	root := newTestRootForAdminSetup(buildSetupDeps(deployDeps, attachDeps))
	root.SetArgs([]string{"admin", "setup"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error when deploy fails due to EC2 error, got nil")
	}
}

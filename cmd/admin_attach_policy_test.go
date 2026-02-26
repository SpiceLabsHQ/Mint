package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssoadmin"
	ssoadmintypes "github.com/aws/aws-sdk-go-v2/service/ssoadmin/types"
	"github.com/SpiceLabsHQ/Mint/internal/cli"
	"github.com/spf13/cobra"
)

// ---------------------------------------------------------------------------
// Inline mocks for admin attach-policy tests
// ---------------------------------------------------------------------------

type mockSSOListInstances struct {
	output *ssoadmin.ListInstancesOutput
	err    error
}

func (m *mockSSOListInstances) ListInstances(ctx context.Context, params *ssoadmin.ListInstancesInput, optFns ...func(*ssoadmin.Options)) (*ssoadmin.ListInstancesOutput, error) {
	return m.output, m.err
}

type mockSSOListPermSets struct {
	output *ssoadmin.ListPermissionSetsOutput
	err    error
}

func (m *mockSSOListPermSets) ListPermissionSets(ctx context.Context, params *ssoadmin.ListPermissionSetsInput, optFns ...func(*ssoadmin.Options)) (*ssoadmin.ListPermissionSetsOutput, error) {
	return m.output, m.err
}

type mockSSODescribePermSet struct {
	output *ssoadmin.DescribePermissionSetOutput
	err    error
}

func (m *mockSSODescribePermSet) DescribePermissionSet(ctx context.Context, params *ssoadmin.DescribePermissionSetInput, optFns ...func(*ssoadmin.Options)) (*ssoadmin.DescribePermissionSetOutput, error) {
	return m.output, m.err
}

type mockSSOAttachPolicy struct {
	output *ssoadmin.AttachCustomerManagedPolicyReferenceToPermissionSetOutput
	err    error
}

func (m *mockSSOAttachPolicy) AttachCustomerManagedPolicyReferenceToPermissionSet(ctx context.Context, params *ssoadmin.AttachCustomerManagedPolicyReferenceToPermissionSetInput, optFns ...func(*ssoadmin.Options)) (*ssoadmin.AttachCustomerManagedPolicyReferenceToPermissionSetOutput, error) {
	return m.output, m.err
}

type mockSSOProvision struct {
	output *ssoadmin.ProvisionPermissionSetOutput
	err    error
}

func (m *mockSSOProvision) ProvisionPermissionSet(ctx context.Context, params *ssoadmin.ProvisionPermissionSetInput, optFns ...func(*ssoadmin.Options)) (*ssoadmin.ProvisionPermissionSetOutput, error) {
	return m.output, m.err
}

// ---------------------------------------------------------------------------
// Helper: minimal root command for admin attach-policy tests
// ---------------------------------------------------------------------------

func newTestRootForAdminAttachPolicy(deps *adminAttachPolicyDeps) *cobra.Command {
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

	adminCmd := newAdminCommandWithAttachPolicyDeps(deps)
	root.AddCommand(adminCmd)
	return root
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestAdminAttachPolicySuccess verifies that a successful attach-policy prints JSON.
func TestAdminAttachPolicySuccess(t *testing.T) {
	const (
		instanceARN    = "arn:aws:sso:::instance/ssoins-abc123"
		permSetARN     = "arn:aws:sso:::permissionSet/ssoins-abc123/ps-xyz789"
		permSetName    = "PowerUserAccess"
	)

	deps := &adminAttachPolicyDeps{
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

	var stdout bytes.Buffer
	root := newTestRootForAdminAttachPolicy(deps)
	root.SetOut(&stdout)

	root.SetArgs([]string{"admin", "attach-policy", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, permSetARN) {
		t.Errorf("expected JSON output to contain permission set ARN %q, got: %s", permSetARN, out)
	}

	// Verify it is valid JSON.
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &result); err != nil {
		t.Errorf("output is not valid JSON: %v\noutput: %s", err, out)
	}
}

// TestAdminAttachPolicyNoSSO verifies that ErrNoSSOInstance produces a graceful skip (nil error).
func TestAdminAttachPolicyNoSSO(t *testing.T) {
	deps := &adminAttachPolicyDeps{
		ssoListInstances: &mockSSOListInstances{
			output: &ssoadmin.ListInstancesOutput{Instances: []ssoadmintypes.InstanceMetadata{}},
		},
		ssoListPermSets:    &mockSSOListPermSets{},
		ssoDescribePermSet: &mockSSODescribePermSet{},
		ssoAttachPolicy:    &mockSSOAttachPolicy{},
		ssoProvision:       &mockSSOProvision{},
	}

	var stderr bytes.Buffer
	root := newTestRootForAdminAttachPolicy(deps)
	root.SetErr(&stderr)

	root.SetArgs([]string{"admin", "attach-policy"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("expected graceful skip (nil error) for ErrNoSSOInstance, got: %v", err)
	}

	errOut := stderr.String()
	if !strings.Contains(errOut, "IAM Identity Center") {
		t.Errorf("expected stderr to mention IAM Identity Center, got: %s", errOut)
	}
}

// TestAdminAttachPolicyDefaultFlags verifies that defaults are passed to Attach when no flags are set.
func TestAdminAttachPolicyDefaultFlags(t *testing.T) {
	const (
		instanceARN = "arn:aws:sso:::instance/ssoins-default"
		permSetARN  = "arn:aws:sso:::permissionSet/ssoins-default/ps-default"
	)

	var capturedPermSetName string
	var capturedPolicyName string

	// Use a capturing mock on AttachCustomerManagedPolicyReferenceToPermissionSet
	// to verify the policy name passed in.
	captureAttach := &capturingSSOAttachPolicy{
		onAttach: func(params *ssoadmin.AttachCustomerManagedPolicyReferenceToPermissionSetInput) {
			if params.CustomerManagedPolicyReference != nil && params.CustomerManagedPolicyReference.Name != nil {
				capturedPolicyName = *params.CustomerManagedPolicyReference.Name
			}
		},
		output: &ssoadmin.AttachCustomerManagedPolicyReferenceToPermissionSetOutput{},
	}

	// Capture the permission set name from DescribePermissionSet calls.
	captureDescribe := &capturingSSODescribePermSet{
		returnName:  "PowerUserAccess",
		returnARN:   permSetARN,
		onDescribe: func(name string) { capturedPermSetName = name },
	}

	deps := &adminAttachPolicyDeps{
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
		ssoDescribePermSet: captureDescribe,
		ssoAttachPolicy:    captureAttach,
		ssoProvision: &mockSSOProvision{
			output: &ssoadmin.ProvisionPermissionSetOutput{
				PermissionSetProvisioningStatus: &ssoadmintypes.PermissionSetProvisioningStatus{
					Status: ssoadmintypes.StatusValuesSucceeded,
				},
			},
		},
	}

	root := newTestRootForAdminAttachPolicy(deps)
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{"admin", "attach-policy"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedPermSetName != "PowerUserAccess" {
		t.Errorf("expected default permission set name %q, got %q", "PowerUserAccess", capturedPermSetName)
	}
	const defaultPolicyName = "mint-pass-instance-role"
	if capturedPolicyName != defaultPolicyName {
		t.Errorf("expected default policy name %q, got %q", defaultPolicyName, capturedPolicyName)
	}
}

// capturingSSOAttachPolicy captures the input passed to AttachCustomerManagedPolicyReferenceToPermissionSet.
type capturingSSOAttachPolicy struct {
	onAttach func(*ssoadmin.AttachCustomerManagedPolicyReferenceToPermissionSetInput)
	output   *ssoadmin.AttachCustomerManagedPolicyReferenceToPermissionSetOutput
	err      error
}

func (m *capturingSSOAttachPolicy) AttachCustomerManagedPolicyReferenceToPermissionSet(ctx context.Context, params *ssoadmin.AttachCustomerManagedPolicyReferenceToPermissionSetInput, optFns ...func(*ssoadmin.Options)) (*ssoadmin.AttachCustomerManagedPolicyReferenceToPermissionSetOutput, error) {
	if m.onAttach != nil {
		m.onAttach(params)
	}
	return m.output, m.err
}

// capturingSSODescribePermSet is a mock DescribePermissionSetAPI that
// returns a fixed permission set name and ARN, and calls a callback with the name.
type capturingSSODescribePermSet struct {
	returnName string
	returnARN  string
	onDescribe func(name string)
}

func (m *capturingSSODescribePermSet) DescribePermissionSet(ctx context.Context, params *ssoadmin.DescribePermissionSetInput, optFns ...func(*ssoadmin.Options)) (*ssoadmin.DescribePermissionSetOutput, error) {
	ps := &ssoadmintypes.PermissionSet{
		PermissionSetArn: aws.String(m.returnARN),
		Name:             aws.String(m.returnName),
	}
	if m.onDescribe != nil {
		m.onDescribe(m.returnName)
	}
	return &ssoadmin.DescribePermissionSetOutput{PermissionSet: ps}, nil
}

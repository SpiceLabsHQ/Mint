package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/SpiceLabsHQ/Mint/internal/cli"
	"github.com/spf13/cobra"
)

// ---------------------------------------------------------------------------
// Inline mocks for admin deploy tests
// ---------------------------------------------------------------------------

type mockCFNCreate struct {
	output *cloudformation.CreateStackOutput
	err    error
}

func (m *mockCFNCreate) CreateStack(ctx context.Context, params *cloudformation.CreateStackInput, optFns ...func(*cloudformation.Options)) (*cloudformation.CreateStackOutput, error) {
	return m.output, m.err
}

type mockCFNUpdate struct {
	output *cloudformation.UpdateStackOutput
	err    error
}

func (m *mockCFNUpdate) UpdateStack(ctx context.Context, params *cloudformation.UpdateStackInput, optFns ...func(*cloudformation.Options)) (*cloudformation.UpdateStackOutput, error) {
	return m.output, m.err
}

type mockCFNDelete struct {
	output *cloudformation.DeleteStackOutput
	err    error
}

func (m *mockCFNDelete) DeleteStack(ctx context.Context, params *cloudformation.DeleteStackInput, optFns ...func(*cloudformation.Options)) (*cloudformation.DeleteStackOutput, error) {
	return m.output, m.err
}

func noopCFNDelete() *mockCFNDelete {
	return &mockCFNDelete{output: &cloudformation.DeleteStackOutput{}}
}

type mockCFNDescribe struct {
	outputs []*cloudformation.DescribeStacksOutput
	err     error
	callIdx int
}

func (m *mockCFNDescribe) DescribeStacks(ctx context.Context, params *cloudformation.DescribeStacksInput, optFns ...func(*cloudformation.Options)) (*cloudformation.DescribeStacksOutput, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.callIdx >= len(m.outputs) {
		return m.outputs[len(m.outputs)-1], nil
	}
	out := m.outputs[m.callIdx]
	m.callIdx++
	return out, nil
}

type mockCFNEvents struct {
	output *cloudformation.DescribeStackEventsOutput
	err    error
}

func (m *mockCFNEvents) DescribeStackEvents(ctx context.Context, params *cloudformation.DescribeStackEventsInput, optFns ...func(*cloudformation.Options)) (*cloudformation.DescribeStackEventsOutput, error) {
	return m.output, m.err
}

type mockEC2DescribeVPCs struct {
	output *ec2.DescribeVpcsOutput
	err    error
}

func (m *mockEC2DescribeVPCs) DescribeVpcs(ctx context.Context, params *ec2.DescribeVpcsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error) {
	return m.output, m.err
}

type mockEC2DescribeSubnets struct {
	output *ec2.DescribeSubnetsOutput
	err    error
}

func (m *mockEC2DescribeSubnets) DescribeSubnets(ctx context.Context, params *ec2.DescribeSubnetsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error) {
	return m.output, m.err
}

// ---------------------------------------------------------------------------
// Helper: minimal root command for admin deploy tests
// ---------------------------------------------------------------------------

func newTestRootForAdminDeploy(deps *adminDeployDeps) *cobra.Command {
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

	adminCmd := newAdminCommandWithDeployDeps(deps)
	root.AddCommand(adminCmd)
	return root
}

// newStackTerminalDescribe returns a mockCFNDescribe that simulates:
// call 1: stack does not exist (for stackExists check)
// call 2: stack in CREATE_COMPLETE (for waitAndStreamEvents poll)
// call 3: stack in CREATE_COMPLETE (for collectOutputs)
func newStackSuccessDescribe(stackName, efsID, sgID, instanceProfileARN, passRoleARN string) *mockCFNDescribe {
	notFound := makeDescribeStacksNotFound()
	complete := makeDescribeStacksComplete(stackName, efsID, sgID, instanceProfileARN, passRoleARN)
	return &mockCFNDescribe{
		outputs: []*cloudformation.DescribeStacksOutput{notFound, complete, complete},
	}
}

func makeDescribeStacksNotFound() *cloudformation.DescribeStacksOutput {
	return &cloudformation.DescribeStacksOutput{Stacks: []cftypes.Stack{}}
}

func makeDescribeStacksComplete(stackName, efsID, sgID, instanceProfileARN, passRoleARN string) *cloudformation.DescribeStacksOutput {
	return &cloudformation.DescribeStacksOutput{
		Stacks: []cftypes.Stack{
			{
				StackName:   aws.String(stackName),
				StackStatus: cftypes.StackStatusCreateComplete,
				Outputs: []cftypes.Output{
					{OutputKey: aws.String("EfsFileSystemId"), OutputValue: aws.String(efsID)},
					{OutputKey: aws.String("EfsSecurityGroupId"), OutputValue: aws.String(sgID)},
					{OutputKey: aws.String("InstanceProfileArn"), OutputValue: aws.String(instanceProfileARN)},
					{OutputKey: aws.String("PassRolePolicyArn"), OutputValue: aws.String(passRoleARN)},
				},
			},
		},
	}
}

func makeVPCOutput(vpcID string) *ec2.DescribeVpcsOutput {
	return &ec2.DescribeVpcsOutput{
		Vpcs: []ec2types.Vpc{
			{VpcId: aws.String(vpcID), IsDefault: aws.Bool(true)},
		},
	}
}

func makeSubnetOutput(subnetIDs ...string) *ec2.DescribeSubnetsOutput {
	subnets := make([]ec2types.Subnet, len(subnetIDs))
	for i, id := range subnetIDs {
		subnets[i] = ec2types.Subnet{SubnetId: aws.String(id)}
	}
	return &ec2.DescribeSubnetsOutput{Subnets: subnets}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestAdminDeploySuccess verifies that a successful deploy prints JSON with all fields.
func TestAdminDeploySuccess(t *testing.T) {
	const (
		stackName          = "mint-admin-setup"
		efsID              = "fs-abc123"
		sgID               = "sg-def456"
		instanceProfileARN = "arn:aws:iam::123456789012:instance-profile/mint"
		passRoleARN        = "arn:aws:iam::123456789012:policy/mint-pass-instance-role"
	)

	deps := &adminDeployDeps{
		cfnCreate:      &mockCFNCreate{output: &cloudformation.CreateStackOutput{}},
		cfnUpdate:      &mockCFNUpdate{output: &cloudformation.UpdateStackOutput{}},
		cfnDelete:      noopCFNDelete(),
		cfnDescribe:    newStackSuccessDescribe(stackName, efsID, sgID, instanceProfileARN, passRoleARN),
		cfnEvents:      &mockCFNEvents{output: &cloudformation.DescribeStackEventsOutput{}},
		ec2DescribeVPCs:     &mockEC2DescribeVPCs{output: makeVPCOutput("vpc-111")},
		ec2DescribeSubnets:  &mockEC2DescribeSubnets{output: makeSubnetOutput("subnet-aaa", "subnet-bbb")},
	}

	var stdout bytes.Buffer
	root := newTestRootForAdminDeploy(deps)
	root.SetOut(&stdout)

	root.SetArgs([]string{"admin", "deploy", "--json", "--stack-name", stackName})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, efsID) {
		t.Errorf("expected JSON output to contain EFS ID %q, got: %s", efsID, out)
	}
	if !strings.Contains(out, instanceProfileARN) {
		t.Errorf("expected JSON output to contain instance profile ARN, got: %s", out)
	}
	if !strings.Contains(out, passRoleARN) {
		t.Errorf("expected JSON output to contain pass-role policy ARN, got: %s", out)
	}

	// Verify it is valid JSON.
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &result); err != nil {
		t.Errorf("output is not valid JSON: %v\noutput: %s", err, out)
	}
}

// TestAdminDeployVPCNotFound verifies that ErrVPCNotFound produces a non-zero exit.
func TestAdminDeployVPCNotFound(t *testing.T) {
	deps := &adminDeployDeps{
		cfnCreate:   &mockCFNCreate{},
		cfnUpdate:   &mockCFNUpdate{},
		cfnDelete:   noopCFNDelete(),
		cfnDescribe: &mockCFNDescribe{outputs: []*cloudformation.DescribeStacksOutput{makeDescribeStacksNotFound()}},
		cfnEvents:   &mockCFNEvents{},
		ec2DescribeVPCs:    &mockEC2DescribeVPCs{output: &ec2.DescribeVpcsOutput{Vpcs: []ec2types.Vpc{}}},
		ec2DescribeSubnets: &mockEC2DescribeSubnets{output: &ec2.DescribeSubnetsOutput{}},
	}

	var stderr bytes.Buffer
	root := newTestRootForAdminDeploy(deps)
	root.SetErr(&stderr)

	root.SetArgs([]string{"admin", "deploy"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for VPC not found, got nil")
	}
}

// TestAdminDeployDefaultStackName verifies that the default stack name "mint-admin-setup" is used
// when no --stack-name flag is provided.
func TestAdminDeployDefaultStackName(t *testing.T) {
	const defaultStack = "mint-admin-setup"

	var capturedStackName string

	// Use a custom mock that captures the stack name passed to CreateStack.
	cfnCreate := &capturingCFNCreate{
		onCreateStack: func(input *cloudformation.CreateStackInput) {
			if input.StackName != nil {
				capturedStackName = *input.StackName
			}
		},
		output: &cloudformation.CreateStackOutput{},
	}

	describe := newStackSuccessDescribe(defaultStack, "fs-xxx", "sg-yyy", "arn:aws:iam::123:instance-profile/m", "arn:aws:iam::123:policy/p")

	deps := &adminDeployDeps{
		cfnCreate:   cfnCreate,
		cfnUpdate:   &mockCFNUpdate{},
		cfnDelete:   noopCFNDelete(),
		cfnDescribe: describe,
		cfnEvents:   &mockCFNEvents{output: &cloudformation.DescribeStackEventsOutput{}},
		ec2DescribeVPCs:    &mockEC2DescribeVPCs{output: makeVPCOutput("vpc-222")},
		ec2DescribeSubnets: &mockEC2DescribeSubnets{output: makeSubnetOutput("subnet-ccc")},
	}

	root := newTestRootForAdminDeploy(deps)
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{"admin", "deploy"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedStackName != defaultStack {
		t.Errorf("expected stack name %q, got %q", defaultStack, capturedStackName)
	}
}

// capturingCFNCreate is a mock CreateStackAPI that invokes a callback on CreateStack.
type capturingCFNCreate struct {
	onCreateStack func(*cloudformation.CreateStackInput)
	output        *cloudformation.CreateStackOutput
	err           error
}

func (m *capturingCFNCreate) CreateStack(ctx context.Context, params *cloudformation.CreateStackInput, optFns ...func(*cloudformation.Options)) (*cloudformation.CreateStackOutput, error) {
	if m.onCreateStack != nil {
		m.onCreateStack(params)
	}
	return m.output, m.err
}

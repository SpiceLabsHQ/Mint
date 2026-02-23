package admin

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// ---------------------------------------------------------------------------
// Inline mocks
// ---------------------------------------------------------------------------

type mockCreateStack struct {
	output *cloudformation.CreateStackOutput
	err    error
	called bool
}

func (m *mockCreateStack) CreateStack(_ context.Context, _ *cloudformation.CreateStackInput, _ ...func(*cloudformation.Options)) (*cloudformation.CreateStackOutput, error) {
	m.called = true
	return m.output, m.err
}

type mockUpdateStack struct {
	output *cloudformation.UpdateStackOutput
	err    error
	called bool
}

func (m *mockUpdateStack) UpdateStack(_ context.Context, _ *cloudformation.UpdateStackInput, _ ...func(*cloudformation.Options)) (*cloudformation.UpdateStackOutput, error) {
	m.called = true
	return m.output, m.err
}

// mockDescribeStacks supports multiple sequential responses so tests can
// exercise the polling loop — first call returns IN_PROGRESS, subsequent calls
// return the terminal status.
type mockDescribeStacks struct {
	responses []*cloudformation.DescribeStacksOutput
	errs      []error
	idx       int
}

func (m *mockDescribeStacks) DescribeStacks(_ context.Context, _ *cloudformation.DescribeStacksInput, _ ...func(*cloudformation.Options)) (*cloudformation.DescribeStacksOutput, error) {
	i := m.idx
	if i >= len(m.responses) {
		i = len(m.responses) - 1
	}
	m.idx++
	var err error
	if i < len(m.errs) {
		err = m.errs[i]
	}
	return m.responses[i], err
}

type mockDescribeStackEvents struct {
	output *cloudformation.DescribeStackEventsOutput
	err    error
}

func (m *mockDescribeStackEvents) DescribeStackEvents(_ context.Context, _ *cloudformation.DescribeStackEventsInput, _ ...func(*cloudformation.Options)) (*cloudformation.DescribeStackEventsOutput, error) {
	return m.output, m.err
}

type mockDescribeVpcs struct {
	output *ec2.DescribeVpcsOutput
	err    error
}

func (m *mockDescribeVpcs) DescribeVpcs(_ context.Context, _ *ec2.DescribeVpcsInput, _ ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error) {
	return m.output, m.err
}

type mockDescribeSubnets struct {
	output *ec2.DescribeSubnetsOutput
	err    error
}

func (m *mockDescribeSubnets) DescribeSubnets(_ context.Context, _ *ec2.DescribeSubnetsInput, _ ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error) {
	return m.output, m.err
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// stackNotFoundDescribe returns a mockDescribeStacks whose first call returns
// the canonical "does not exist" error, followed by a final terminal response.
func stackNotFoundThenComplete(status cftypes.StackStatus, outputs []cftypes.Output) *mockDescribeStacks {
	notFoundErr := errors.New("Stack with id mint-admin does not exist (ValidationError)")
	terminalOut := &cloudformation.DescribeStacksOutput{
		Stacks: []cftypes.Stack{
			{
				StackName:   aws.String("mint-admin"),
				StackStatus: status,
				Outputs:     outputs,
			},
		},
	}
	return &mockDescribeStacks{
		responses: []*cloudformation.DescribeStacksOutput{nil, terminalOut, terminalOut},
		errs:      []error{notFoundErr, nil, nil},
	}
}

// existingStackResponses returns a mockDescribeStacks that first signals the
// stack exists, then returns a terminal status after the update.
func existingStackThenComplete(status cftypes.StackStatus, outputs []cftypes.Output) *mockDescribeStacks {
	existOut := &cloudformation.DescribeStacksOutput{
		Stacks: []cftypes.Stack{
			{StackName: aws.String("mint-admin"), StackStatus: cftypes.StackStatusUpdateComplete},
		},
	}
	terminalOut := &cloudformation.DescribeStacksOutput{
		Stacks: []cftypes.Stack{
			{
				StackName:   aws.String("mint-admin"),
				StackStatus: status,
				Outputs:     outputs,
			},
		},
	}
	return &mockDescribeStacks{
		responses: []*cloudformation.DescribeStacksOutput{existOut, terminalOut, terminalOut},
	}
}

func defaultVPCMock() *mockDescribeVpcs {
	return &mockDescribeVpcs{
		output: &ec2.DescribeVpcsOutput{
			Vpcs: []ec2types.Vpc{
				{VpcId: aws.String("vpc-default")},
			},
		},
	}
}

func twoSubnetsMock() *mockDescribeSubnets {
	return &mockDescribeSubnets{
		output: &ec2.DescribeSubnetsOutput{
			Subnets: []ec2types.Subnet{
				{SubnetId: aws.String("subnet-1a")},
				{SubnetId: aws.String("subnet-1b")},
			},
		},
	}
}

func sampleOutputs() []cftypes.Output {
	return []cftypes.Output{
		{OutputKey: aws.String("EfsFileSystemId"), OutputValue: aws.String("fs-abc123")},
		{OutputKey: aws.String("EfsSecurityGroupId"), OutputValue: aws.String("sg-efs")},
		{OutputKey: aws.String("InstanceProfileArn"), OutputValue: aws.String("arn:aws:iam::111:instance-profile/mint")},
		{OutputKey: aws.String("PassRolePolicyArn"), OutputValue: aws.String("arn:aws:iam::111:policy/mint-pass")},
	}
}

func newDeployerForTest(
	create *mockCreateStack,
	update *mockUpdateStack,
	describe *mockDescribeStacks,
	events *mockDescribeStackEvents,
	vpcs *mockDescribeVpcs,
	subnets *mockDescribeSubnets,
) *Deployer {
	d := NewDeployer(create, update, describe, events, vpcs, subnets)
	// Override poll interval to zero to avoid real sleeps in tests.
	d.pollInterval = 0
	// Fix the clock so startTime filtering is predictable.
	fixedNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	d.clock = func() time.Time { return fixedNow }
	return d
}

// ---------------------------------------------------------------------------
// Table-driven tests
// ---------------------------------------------------------------------------

func TestDeployer_FreshDeploy(t *testing.T) {
	// VPC found, stack does not exist → CreateStack should be called.
	create := &mockCreateStack{output: &cloudformation.CreateStackOutput{}}
	update := &mockUpdateStack{}
	describe := stackNotFoundThenComplete(cftypes.StackStatusCreateComplete, sampleOutputs())
	events := &mockDescribeStackEvents{output: &cloudformation.DescribeStackEventsOutput{}}

	d := newDeployerForTest(create, update, describe, events, defaultVPCMock(), twoSubnetsMock())

	result, err := d.Deploy(context.Background(), DeployOptions{StackName: "mint-admin"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !create.called {
		t.Error("expected CreateStack to be called for a fresh deploy")
	}
	if update.called {
		t.Error("UpdateStack should not be called for a fresh deploy")
	}
	if result.EfsFileSystemId != "fs-abc123" {
		t.Errorf("unexpected EfsFileSystemId: %q", result.EfsFileSystemId)
	}
	if result.StackName != "mint-admin" {
		t.Errorf("unexpected StackName: %q", result.StackName)
	}
}

func TestDeployer_UpdatePath(t *testing.T) {
	// VPC found, stack exists → UpdateStack should be called.
	create := &mockCreateStack{}
	update := &mockUpdateStack{output: &cloudformation.UpdateStackOutput{}}
	describe := existingStackThenComplete(cftypes.StackStatusUpdateComplete, sampleOutputs())
	events := &mockDescribeStackEvents{output: &cloudformation.DescribeStackEventsOutput{}}

	d := newDeployerForTest(create, update, describe, events, defaultVPCMock(), twoSubnetsMock())

	result, err := d.Deploy(context.Background(), DeployOptions{StackName: "mint-admin"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !update.called {
		t.Error("expected UpdateStack to be called for an existing stack")
	}
	if create.called {
		t.Error("CreateStack should not be called when stack already exists")
	}
	if result.EfsSecurityGroupId != "sg-efs" {
		t.Errorf("unexpected EfsSecurityGroupId: %q", result.EfsSecurityGroupId)
	}
}

func TestDeployer_Idempotent_NoUpdates(t *testing.T) {
	// UpdateStack returns "No updates are to be performed" → treated as success.
	create := &mockCreateStack{}
	update := &mockUpdateStack{
		err: errors.New("ValidationError: No updates are to be performed"),
	}
	describe := existingStackThenComplete(cftypes.StackStatusUpdateComplete, sampleOutputs())
	events := &mockDescribeStackEvents{output: &cloudformation.DescribeStackEventsOutput{}}

	d := newDeployerForTest(create, update, describe, events, defaultVPCMock(), twoSubnetsMock())

	result, err := d.Deploy(context.Background(), DeployOptions{StackName: "mint-admin"})
	if err != nil {
		t.Fatalf("expected no error for idempotent no-op, got: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result for idempotent no-op")
	}
}

func TestDeployer_VPCNotFound(t *testing.T) {
	// DescribeVpcs returns 0 VPCs → ErrVPCNotFound.
	vpcs := &mockDescribeVpcs{
		output: &ec2.DescribeVpcsOutput{Vpcs: []ec2types.Vpc{}},
	}
	create := &mockCreateStack{}
	update := &mockUpdateStack{}
	describe := &mockDescribeStacks{
		responses: []*cloudformation.DescribeStacksOutput{{}},
	}
	events := &mockDescribeStackEvents{output: &cloudformation.DescribeStackEventsOutput{}}
	subnets := twoSubnetsMock()

	d := newDeployerForTest(create, update, describe, events, vpcs, subnets)

	_, err := d.Deploy(context.Background(), DeployOptions{StackName: "mint-admin"})
	if err == nil {
		t.Fatal("expected ErrVPCNotFound, got nil")
	}
	if !errors.Is(err, ErrVPCNotFound) {
		t.Errorf("expected errors.Is(err, ErrVPCNotFound), got: %v", err)
	}
}

func TestDeployer_EventStreaming(t *testing.T) {
	// Events written to EventWriter during polling.
	create := &mockCreateStack{output: &cloudformation.CreateStackOutput{}}
	describe := stackNotFoundThenComplete(cftypes.StackStatusCreateComplete, sampleOutputs())
	update := &mockUpdateStack{}

	// Provide a stack event with a timestamp after the fixed clock (2026-01-01).
	eventTime := time.Date(2026, 1, 1, 0, 1, 0, 0, time.UTC)
	events := &mockDescribeStackEvents{
		output: &cloudformation.DescribeStackEventsOutput{
			StackEvents: []cftypes.StackEvent{
				{
					EventId:              aws.String("evt-001"),
					LogicalResourceId:    aws.String("MintEfsFileSystem"),
					ResourceStatus:       cftypes.ResourceStatusCreateComplete,
					ResourceStatusReason: aws.String(""),
					Timestamp:            aws.Time(eventTime),
				},
			},
		},
	}

	var buf bytes.Buffer
	d := newDeployerForTest(create, update, describe, events, defaultVPCMock(), twoSubnetsMock())

	_, err := d.Deploy(context.Background(), DeployOptions{
		StackName:   "mint-admin",
		EventWriter: &buf,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "MintEfsFileSystem") {
		t.Errorf("expected EventWriter to contain resource name, got:\n%s", output)
	}
	if !strings.Contains(output, "CREATE_COMPLETE") {
		t.Errorf("expected EventWriter to contain resource status, got:\n%s", output)
	}
}

func TestDeployer_DefaultStackName(t *testing.T) {
	// Empty StackName defaults to "mint-admin".
	create := &mockCreateStack{output: &cloudformation.CreateStackOutput{}}
	update := &mockUpdateStack{}
	describe := stackNotFoundThenComplete(cftypes.StackStatusCreateComplete, sampleOutputs())
	events := &mockDescribeStackEvents{output: &cloudformation.DescribeStackEventsOutput{}}

	d := newDeployerForTest(create, update, describe, events, defaultVPCMock(), twoSubnetsMock())

	result, err := d.Deploy(context.Background(), DeployOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.StackName != defaultStackName {
		t.Errorf("expected StackName %q, got %q", defaultStackName, result.StackName)
	}
}

func TestDeployer_FailedStackStatus(t *testing.T) {
	// CREATE_FAILED terminal status should be returned as an error.
	create := &mockCreateStack{output: &cloudformation.CreateStackOutput{}}
	update := &mockUpdateStack{}
	describe := stackNotFoundThenComplete(cftypes.StackStatusCreateFailed, nil)
	events := &mockDescribeStackEvents{output: &cloudformation.DescribeStackEventsOutput{}}

	d := newDeployerForTest(create, update, describe, events, defaultVPCMock(), twoSubnetsMock())

	_, err := d.Deploy(context.Background(), DeployOptions{StackName: "mint-admin"})
	if err == nil {
		t.Fatal("expected error for failed stack status, got nil")
	}
	if !strings.Contains(err.Error(), "CREATE_FAILED") {
		t.Errorf("expected error to mention stack status, got: %v", err)
	}
}

func TestDeployer_SubnetParameterMapping(t *testing.T) {
	// Verify that subnet IDs from DescribeSubnets are mapped to Subnet1..N
	// parameters. We test the helper directly.
	params := buildParameters("vpc-abc", []string{"subnet-1", "subnet-2", "subnet-3"})

	paramMap := make(map[string]string)
	for _, p := range params {
		paramMap[aws.ToString(p.ParameterKey)] = aws.ToString(p.ParameterValue)
	}

	if paramMap["VpcId"] != "vpc-abc" {
		t.Errorf("VpcId: got %q", paramMap["VpcId"])
	}
	if paramMap["Subnet1"] != "subnet-1" {
		t.Errorf("Subnet1: got %q", paramMap["Subnet1"])
	}
	if paramMap["Subnet2"] != "subnet-2" {
		t.Errorf("Subnet2: got %q", paramMap["Subnet2"])
	}
	if paramMap["Subnet3"] != "subnet-3" {
		t.Errorf("Subnet3: got %q", paramMap["Subnet3"])
	}
	// Unprovided optional subnets should be empty string.
	if paramMap["Subnet4"] != "" {
		t.Errorf("Subnet4 should be empty, got %q", paramMap["Subnet4"])
	}
}

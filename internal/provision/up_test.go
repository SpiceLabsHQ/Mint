package provision

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	mintaws "github.com/nicholasgasior/mint/internal/aws"
	"github.com/nicholasgasior/mint/internal/tags"
)

// ---------------------------------------------------------------------------
// Inline mocks for up
// ---------------------------------------------------------------------------

type mockUpDescribeInstances struct {
	output *ec2.DescribeInstancesOutput
	err    error
}

func (m *mockUpDescribeInstances) DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return m.output, m.err
}

type mockStartInstances struct {
	output *ec2.StartInstancesOutput
	err    error
	called bool
	input  *ec2.StartInstancesInput
}

func (m *mockStartInstances) StartInstances(ctx context.Context, params *ec2.StartInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error) {
	m.called = true
	m.input = params
	return m.output, m.err
}

type mockRunInstances struct {
	output *ec2.RunInstancesOutput
	err    error
	called bool
	input  *ec2.RunInstancesInput
}

func (m *mockRunInstances) RunInstances(ctx context.Context, params *ec2.RunInstancesInput, optFns ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error) {
	m.called = true
	m.input = params
	return m.output, m.err
}

type mockUpDescribeSecurityGroups struct {
	outputs []*ec2.DescribeSecurityGroupsOutput
	errs    []error
	calls   int
}

func (m *mockUpDescribeSecurityGroups) DescribeSecurityGroups(ctx context.Context, params *ec2.DescribeSecurityGroupsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error) {
	idx := m.calls
	m.calls++
	if idx < len(m.outputs) {
		var err error
		if idx < len(m.errs) {
			err = m.errs[idx]
		}
		return m.outputs[idx], err
	}
	return nil, fmt.Errorf("unexpected DescribeSecurityGroups call %d", idx)
}

type mockUpDescribeSubnets struct {
	output *ec2.DescribeSubnetsOutput
	err    error
}

func (m *mockUpDescribeSubnets) DescribeSubnets(ctx context.Context, params *ec2.DescribeSubnetsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error) {
	return m.output, m.err
}

type mockUpCreateVolume struct {
	output *ec2.CreateVolumeOutput
	err    error
	called bool
	input  *ec2.CreateVolumeInput
}

func (m *mockUpCreateVolume) CreateVolume(ctx context.Context, params *ec2.CreateVolumeInput, optFns ...func(*ec2.Options)) (*ec2.CreateVolumeOutput, error) {
	m.called = true
	m.input = params
	return m.output, m.err
}

type mockUpAttachVolume struct {
	output *ec2.AttachVolumeOutput
	err    error
	called bool
	input  *ec2.AttachVolumeInput
}

func (m *mockUpAttachVolume) AttachVolume(ctx context.Context, params *ec2.AttachVolumeInput, optFns ...func(*ec2.Options)) (*ec2.AttachVolumeOutput, error) {
	m.called = true
	m.input = params
	return m.output, m.err
}

type mockUpAllocateAddress struct {
	output *ec2.AllocateAddressOutput
	err    error
	called bool
}

func (m *mockUpAllocateAddress) AllocateAddress(ctx context.Context, params *ec2.AllocateAddressInput, optFns ...func(*ec2.Options)) (*ec2.AllocateAddressOutput, error) {
	m.called = true
	return m.output, m.err
}

type mockUpAssociateAddress struct {
	output *ec2.AssociateAddressOutput
	err    error
	called bool
	input  *ec2.AssociateAddressInput
}

func (m *mockUpAssociateAddress) AssociateAddress(ctx context.Context, params *ec2.AssociateAddressInput, optFns ...func(*ec2.Options)) (*ec2.AssociateAddressOutput, error) {
	m.called = true
	m.input = params
	return m.output, m.err
}

type mockUpDescribeAddresses struct {
	output *ec2.DescribeAddressesOutput
	err    error
}

func (m *mockUpDescribeAddresses) DescribeAddresses(ctx context.Context, params *ec2.DescribeAddressesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeAddressesOutput, error) {
	return m.output, m.err
}

type mockUpCreateTags struct {
	output *ec2.CreateTagsOutput
	err    error
	called bool
}

func (m *mockUpCreateTags) CreateTags(ctx context.Context, params *ec2.CreateTagsInput, optFns ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error) {
	m.called = true
	return m.output, m.err
}

type mockGetParameter struct {
	output *ssm.GetParameterOutput
	err    error
}

func (m *mockGetParameter) GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	return m.output, m.err
}

// ---------------------------------------------------------------------------
// Helper: build a Provisioner with all mocks
// ---------------------------------------------------------------------------

type upMocks struct {
	describeInstances *mockUpDescribeInstances
	startInstances    *mockStartInstances
	runInstances      *mockRunInstances
	describeSGs       *mockUpDescribeSecurityGroups
	describeSubnets   *mockUpDescribeSubnets
	createVolume      *mockUpCreateVolume
	attachVolume      *mockUpAttachVolume
	allocateAddr      *mockUpAllocateAddress
	associateAddr     *mockUpAssociateAddress
	describeAddrs     *mockUpDescribeAddresses
	createTags        *mockUpCreateTags
	ssmClient         *mockGetParameter

	bootstrapVerifier BootstrapVerifier
	amiResolver       AMIResolver
}

func newUpHappyMocks() *upMocks {
	return &upMocks{
		describeInstances: &mockUpDescribeInstances{
			output: &ec2.DescribeInstancesOutput{}, // no existing VM
		},
		startInstances: &mockStartInstances{
			output: &ec2.StartInstancesOutput{},
		},
		runInstances: &mockRunInstances{
			output: &ec2.RunInstancesOutput{
				Instances: []ec2types.Instance{
					{InstanceId: aws.String("i-new123")},
				},
			},
		},
		describeSGs: &mockUpDescribeSecurityGroups{
			outputs: []*ec2.DescribeSecurityGroupsOutput{
				// First call: user security group
				{SecurityGroups: []ec2types.SecurityGroup{
					{GroupId: aws.String("sg-user1")},
				}},
				// Second call: admin security group
				{SecurityGroups: []ec2types.SecurityGroup{
					{GroupId: aws.String("sg-admin1")},
				}},
			},
			errs: []error{nil, nil},
		},
		describeSubnets: &mockUpDescribeSubnets{
			output: &ec2.DescribeSubnetsOutput{
				Subnets: []ec2types.Subnet{
					{
						SubnetId:         aws.String("subnet-abc"),
						AvailabilityZone: aws.String("us-east-1a"),
					},
				},
			},
		},
		createVolume: &mockUpCreateVolume{
			output: &ec2.CreateVolumeOutput{
				VolumeId: aws.String("vol-proj1"),
			},
		},
		attachVolume: &mockUpAttachVolume{
			output: &ec2.AttachVolumeOutput{},
		},
		allocateAddr: &mockUpAllocateAddress{
			output: &ec2.AllocateAddressOutput{
				AllocationId: aws.String("eipalloc-new1"),
				PublicIp:     aws.String("54.1.2.3"),
			},
		},
		associateAddr: &mockUpAssociateAddress{
			output: &ec2.AssociateAddressOutput{},
		},
		describeAddrs: &mockUpDescribeAddresses{
			output: &ec2.DescribeAddressesOutput{
				Addresses: []ec2types.Address{}, // no existing EIPs
			},
		},
		createTags: &mockUpCreateTags{
			output: &ec2.CreateTagsOutput{},
		},
		ssmClient: &mockGetParameter{},
		bootstrapVerifier: func(content []byte) error {
			return nil // always pass
		},
		amiResolver: func(ctx context.Context, client mintaws.GetParameterAPI) (string, error) {
			return "ami-ubuntu2404", nil
		},
	}
}

func (m *upMocks) build() *Provisioner {
	p := NewProvisioner(
		m.describeInstances,
		m.startInstances,
		m.runInstances,
		m.describeSGs,
		m.describeSubnets,
		m.createVolume,
		m.attachVolume,
		m.allocateAddr,
		m.associateAddr,
		m.describeAddrs,
		m.createTags,
		m.ssmClient,
	)
	p.WithBootstrapVerifier(m.bootstrapVerifier)
	p.WithAMIResolver(m.amiResolver)
	return p
}

func defaultConfig() ProvisionConfig {
	return ProvisionConfig{
		InstanceType:    "m6i.xlarge",
		VolumeSize:      50,
		BootstrapScript: []byte("#!/bin/bash\necho hello"),
	}
}

// ---------------------------------------------------------------------------
// Tests: Restart stopped VM path
// ---------------------------------------------------------------------------

func TestProvisionerRestartStoppedVM(t *testing.T) {
	m := newUpHappyMocks()
	m.describeInstances.output = &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{
			Instances: []ec2types.Instance{{
				InstanceId:   aws.String("i-stopped1"),
				InstanceType: ec2types.InstanceTypeM6iXlarge,
				State: &ec2types.InstanceState{
					Name: ec2types.InstanceStateNameStopped,
				},
				PublicIpAddress: aws.String("54.0.0.1"),
				Tags: []ec2types.Tag{
					{Key: aws.String("mint:vm"), Value: aws.String("default")},
					{Key: aws.String("mint:owner"), Value: aws.String("alice")},
				},
			}},
		}},
	}
	p := m.build()

	result, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !m.startInstances.called {
		t.Error("StartInstances should be called for stopped VM")
	}
	if m.runInstances.called {
		t.Error("RunInstances should NOT be called when restarting")
	}
	if result.InstanceID != "i-stopped1" {
		t.Errorf("result.InstanceID = %q, want %q", result.InstanceID, "i-stopped1")
	}
	if !result.Restarted {
		t.Error("result.Restarted should be true")
	}
}

func TestProvisionerExistingRunningVM(t *testing.T) {
	m := newUpHappyMocks()
	m.describeInstances.output = &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{
			Instances: []ec2types.Instance{{
				InstanceId:   aws.String("i-running1"),
				InstanceType: ec2types.InstanceTypeM6iXlarge,
				State: &ec2types.InstanceState{
					Name: ec2types.InstanceStateNameRunning,
				},
				PublicIpAddress: aws.String("54.0.0.2"),
				Tags: []ec2types.Tag{
					{Key: aws.String("mint:vm"), Value: aws.String("default")},
					{Key: aws.String("mint:owner"), Value: aws.String("alice")},
				},
			}},
		}},
	}
	p := m.build()

	result, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if m.startInstances.called {
		t.Error("StartInstances should NOT be called for running VM")
	}
	if m.runInstances.called {
		t.Error("RunInstances should NOT be called for running VM")
	}
	if result.InstanceID != "i-running1" {
		t.Errorf("result.InstanceID = %q, want %q", result.InstanceID, "i-running1")
	}
	if result.Restarted {
		t.Error("result.Restarted should be false for already-running VM")
	}
}

// ---------------------------------------------------------------------------
// Tests: Full provision flow (happy path)
// ---------------------------------------------------------------------------

func TestProvisionerFullHappyPath(t *testing.T) {
	m := newUpHappyMocks()
	p := m.build()

	result, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.InstanceID != "i-new123" {
		t.Errorf("result.InstanceID = %q, want %q", result.InstanceID, "i-new123")
	}
	if result.PublicIP != "54.1.2.3" {
		t.Errorf("result.PublicIP = %q, want %q", result.PublicIP, "54.1.2.3")
	}
	if result.VolumeID != "vol-proj1" {
		t.Errorf("result.VolumeID = %q, want %q", result.VolumeID, "vol-proj1")
	}
	if result.AllocationID != "eipalloc-new1" {
		t.Errorf("result.AllocationID = %q, want %q", result.AllocationID, "eipalloc-new1")
	}
	if result.Restarted {
		t.Error("result.Restarted should be false for new provision")
	}

	// Verify RunInstances was called with correct parameters.
	if !m.runInstances.called {
		t.Fatal("RunInstances was not called")
	}
	input := m.runInstances.input
	if aws.ToString(input.ImageId) != "ami-ubuntu2404" {
		t.Errorf("ImageId = %q, want %q", aws.ToString(input.ImageId), "ami-ubuntu2404")
	}
	if input.InstanceType != ec2types.InstanceTypeM6iXlarge {
		t.Errorf("InstanceType = %q, want %q", input.InstanceType, ec2types.InstanceTypeM6iXlarge)
	}
	if len(input.SecurityGroupIds) != 2 {
		t.Fatalf("expected 2 security groups, got %d", len(input.SecurityGroupIds))
	}
	if input.SecurityGroupIds[0] != "sg-user1" {
		t.Errorf("SG[0] = %q, want %q", input.SecurityGroupIds[0], "sg-user1")
	}
	if input.SecurityGroupIds[1] != "sg-admin1" {
		t.Errorf("SG[1] = %q, want %q", input.SecurityGroupIds[1], "sg-admin1")
	}
	if aws.ToString(input.SubnetId) != "subnet-abc" {
		t.Errorf("SubnetId = %q, want %q", aws.ToString(input.SubnetId), "subnet-abc")
	}
	if aws.ToString(input.IamInstanceProfile.Name) != "mint-instance-profile" {
		t.Errorf("IamInstanceProfile.Name = %q, want %q", aws.ToString(input.IamInstanceProfile.Name), "mint-instance-profile")
	}

	// Verify user-data is base64-encoded bootstrap script.
	expectedUD := base64.StdEncoding.EncodeToString([]byte("#!/bin/bash\necho hello"))
	if aws.ToString(input.UserData) != expectedUD {
		t.Errorf("UserData = %q, want %q", aws.ToString(input.UserData), expectedUD)
	}

	// Verify volume was created with correct size and type.
	if !m.createVolume.called {
		t.Fatal("CreateVolume was not called")
	}
	if aws.ToInt32(m.createVolume.input.Size) != 50 {
		t.Errorf("volume Size = %d, want 50", aws.ToInt32(m.createVolume.input.Size))
	}
	if m.createVolume.input.VolumeType != ec2types.VolumeTypeGp3 {
		t.Errorf("volume VolumeType = %q, want gp3", m.createVolume.input.VolumeType)
	}

	// Verify volume attached with correct device.
	if !m.attachVolume.called {
		t.Fatal("AttachVolume was not called")
	}
	if aws.ToString(m.attachVolume.input.Device) != "/dev/xvdf" {
		t.Errorf("attach Device = %q, want %q", aws.ToString(m.attachVolume.input.Device), "/dev/xvdf")
	}

	// Verify EIP was allocated and associated.
	if !m.allocateAddr.called {
		t.Fatal("AllocateAddress was not called")
	}
	if !m.associateAddr.called {
		t.Fatal("AssociateAddress was not called")
	}
}

// ---------------------------------------------------------------------------
// Tests: Bootstrap hash verification failure
// ---------------------------------------------------------------------------

func TestProvisionerBootstrapVerificationFailure(t *testing.T) {
	m := newUpHappyMocks()
	m.bootstrapVerifier = func(content []byte) error {
		return fmt.Errorf("bootstrap script hash mismatch: expected abc, got def")
	}
	p := m.build()

	_, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err == nil {
		t.Fatal("expected error for bootstrap verification failure")
	}
	if !strings.Contains(err.Error(), "bootstrap verification failed") {
		t.Errorf("error = %q, want substring %q", err.Error(), "bootstrap verification failed")
	}
	if !strings.Contains(err.Error(), "hash mismatch") {
		t.Errorf("error = %q, want substring %q", err.Error(), "hash mismatch")
	}
	if m.runInstances.called {
		t.Error("RunInstances should NOT be called when bootstrap verification fails")
	}
}

// ---------------------------------------------------------------------------
// Tests: EIP quota exceeded
// ---------------------------------------------------------------------------

func TestProvisionerEIPQuotaExceeded(t *testing.T) {
	m := newUpHappyMocks()
	// Fill up the EIP quota (5 existing).
	addrs := make([]ec2types.Address, DefaultEIPLimit)
	for i := range addrs {
		addrs[i] = ec2types.Address{
			AllocationId: aws.String(fmt.Sprintf("eipalloc-%d", i)),
			PublicIp:     aws.String(fmt.Sprintf("54.0.0.%d", i)),
		}
	}
	m.describeAddrs.output = &ec2.DescribeAddressesOutput{
		Addresses: addrs,
	}
	p := m.build()

	_, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err == nil {
		t.Fatal("expected error for EIP quota exceeded")
	}
	if !strings.Contains(err.Error(), "EIP quota exceeded") {
		t.Errorf("error = %q, want substring %q", err.Error(), "EIP quota exceeded")
	}
	if !strings.Contains(err.Error(), "5 of 5") {
		t.Errorf("error should include count and limit, got: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "console.aws.amazon.com") {
		t.Errorf("error should include console URL, got: %q", err.Error())
	}
	if m.runInstances.called {
		t.Error("RunInstances should NOT be called when EIP quota exceeded")
	}
}

// ---------------------------------------------------------------------------
// Tests: AMI resolution failure
// ---------------------------------------------------------------------------

func TestProvisionerAMIResolutionFailure(t *testing.T) {
	m := newUpHappyMocks()
	m.amiResolver = func(ctx context.Context, client mintaws.GetParameterAPI) (string, error) {
		return "", fmt.Errorf("ssm get-parameter: access denied")
	}
	p := m.build()

	_, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err == nil {
		t.Fatal("expected error for AMI resolution failure")
	}
	if !strings.Contains(err.Error(), "resolving AMI") {
		t.Errorf("error = %q, want substring %q", err.Error(), "resolving AMI")
	}
	if m.runInstances.called {
		t.Error("RunInstances should NOT be called when AMI resolution fails")
	}
}

// ---------------------------------------------------------------------------
// Tests: Security group discovery
// ---------------------------------------------------------------------------

func TestProvisionerUserSGNotFound(t *testing.T) {
	m := newUpHappyMocks()
	m.describeSGs = &mockUpDescribeSecurityGroups{
		outputs: []*ec2.DescribeSecurityGroupsOutput{
			{SecurityGroups: []ec2types.SecurityGroup{}}, // user SG not found
		},
		errs: []error{nil},
	}
	p := m.build()

	_, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err == nil {
		t.Fatal("expected error when user security group not found")
	}
	if !strings.Contains(err.Error(), "mint init") {
		t.Errorf("error should suggest 'mint init', got: %q", err.Error())
	}
}

func TestProvisionerAdminSGNotFound(t *testing.T) {
	m := newUpHappyMocks()
	m.describeSGs = &mockUpDescribeSecurityGroups{
		outputs: []*ec2.DescribeSecurityGroupsOutput{
			// User SG found
			{SecurityGroups: []ec2types.SecurityGroup{
				{GroupId: aws.String("sg-user1")},
			}},
			// Admin SG not found
			{SecurityGroups: []ec2types.SecurityGroup{}},
		},
		errs: []error{nil, nil},
	}
	p := m.build()

	_, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err == nil {
		t.Fatal("expected error when admin security group not found")
	}
	if !strings.Contains(err.Error(), "admin") {
		t.Errorf("error should mention admin, got: %q", err.Error())
	}
}

func TestProvisionerSGDescribeError(t *testing.T) {
	m := newUpHappyMocks()
	m.describeSGs = &mockUpDescribeSecurityGroups{
		outputs: []*ec2.DescribeSecurityGroupsOutput{nil},
		errs:    []error{fmt.Errorf("throttled")},
	}
	p := m.build()

	_, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err == nil {
		t.Fatal("expected error on SG describe failure")
	}
	if !strings.Contains(err.Error(), "throttled") {
		t.Errorf("error = %q, want substring %q", err.Error(), "throttled")
	}
}

// ---------------------------------------------------------------------------
// Tests: Subnet selection
// ---------------------------------------------------------------------------

func TestProvisionerNoSubnetsFound(t *testing.T) {
	m := newUpHappyMocks()
	m.describeSubnets.output = &ec2.DescribeSubnetsOutput{
		Subnets: []ec2types.Subnet{},
	}
	p := m.build()

	_, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err == nil {
		t.Fatal("expected error when no subnets found")
	}
	if !strings.Contains(err.Error(), "no default subnets") {
		t.Errorf("error = %q, want substring %q", err.Error(), "no default subnets")
	}
}

func TestProvisionerSubnetDescribeError(t *testing.T) {
	m := newUpHappyMocks()
	m.describeSubnets.err = fmt.Errorf("subnet API error")
	p := m.build()

	_, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err == nil {
		t.Fatal("expected error on subnet describe failure")
	}
	if !strings.Contains(err.Error(), "subnet API error") {
		t.Errorf("error = %q, want substring %q", err.Error(), "subnet API error")
	}
}

// ---------------------------------------------------------------------------
// Tests: Launch instance errors
// ---------------------------------------------------------------------------

func TestProvisionerRunInstancesError(t *testing.T) {
	m := newUpHappyMocks()
	m.runInstances.err = fmt.Errorf("insufficient capacity")
	p := m.build()

	_, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err == nil {
		t.Fatal("expected error on RunInstances failure")
	}
	if !strings.Contains(err.Error(), "insufficient capacity") {
		t.Errorf("error = %q, want substring %q", err.Error(), "insufficient capacity")
	}
}

func TestProvisionerRunInstancesNoInstances(t *testing.T) {
	m := newUpHappyMocks()
	m.runInstances.output = &ec2.RunInstancesOutput{
		Instances: []ec2types.Instance{},
	}
	p := m.build()

	_, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err == nil {
		t.Fatal("expected error when RunInstances returns no instances")
	}
	if !strings.Contains(err.Error(), "returned no instances") {
		t.Errorf("error = %q, want substring %q", err.Error(), "returned no instances")
	}
}

// ---------------------------------------------------------------------------
// Tests: Volume creation errors
// ---------------------------------------------------------------------------

func TestProvisionerCreateVolumeError(t *testing.T) {
	m := newUpHappyMocks()
	m.createVolume.err = fmt.Errorf("volume limit exceeded")
	p := m.build()

	_, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err == nil {
		t.Fatal("expected error on CreateVolume failure")
	}
	if !strings.Contains(err.Error(), "volume limit exceeded") {
		t.Errorf("error = %q, want substring %q", err.Error(), "volume limit exceeded")
	}
}

func TestProvisionerAttachVolumeError(t *testing.T) {
	m := newUpHappyMocks()
	m.attachVolume.err = fmt.Errorf("attach failed")
	p := m.build()

	_, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err == nil {
		t.Fatal("expected error on AttachVolume failure")
	}
	if !strings.Contains(err.Error(), "attach failed") {
		t.Errorf("error = %q, want substring %q", err.Error(), "attach failed")
	}
}

// ---------------------------------------------------------------------------
// Tests: EIP allocation errors
// ---------------------------------------------------------------------------

func TestProvisionerAllocateAddressError(t *testing.T) {
	m := newUpHappyMocks()
	m.allocateAddr.err = fmt.Errorf("address limit reached")
	p := m.build()

	_, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err == nil {
		t.Fatal("expected error on AllocateAddress failure")
	}
	if !strings.Contains(err.Error(), "address limit reached") {
		t.Errorf("error = %q, want substring %q", err.Error(), "address limit reached")
	}
}

func TestProvisionerAssociateAddressError(t *testing.T) {
	m := newUpHappyMocks()
	m.associateAddr.err = fmt.Errorf("association failed")
	p := m.build()

	_, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err == nil {
		t.Fatal("expected error on AssociateAddress failure")
	}
	if !strings.Contains(err.Error(), "association failed") {
		t.Errorf("error = %q, want substring %q", err.Error(), "association failed")
	}
}

// ---------------------------------------------------------------------------
// Tests: VM discovery error
// ---------------------------------------------------------------------------

func TestProvisionerDiscoverVMError(t *testing.T) {
	m := newUpHappyMocks()
	m.describeInstances.err = fmt.Errorf("describe instances throttled")
	p := m.build()

	_, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err == nil {
		t.Fatal("expected error on DescribeInstances failure")
	}
	if !strings.Contains(err.Error(), "discovering VM") {
		t.Errorf("error = %q, want substring %q", err.Error(), "discovering VM")
	}
}

// ---------------------------------------------------------------------------
// Tests: Start stopped VM error
// ---------------------------------------------------------------------------

func TestProvisionerStartStoppedVMError(t *testing.T) {
	m := newUpHappyMocks()
	m.describeInstances.output = &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{
			Instances: []ec2types.Instance{{
				InstanceId:   aws.String("i-stopped1"),
				InstanceType: ec2types.InstanceTypeM6iXlarge,
				State: &ec2types.InstanceState{
					Name: ec2types.InstanceStateNameStopped,
				},
				Tags: []ec2types.Tag{
					{Key: aws.String("mint:vm"), Value: aws.String("default")},
					{Key: aws.String("mint:owner"), Value: aws.String("alice")},
				},
			}},
		}},
	}
	m.startInstances.err = fmt.Errorf("cannot start instance")
	p := m.build()

	_, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err == nil {
		t.Fatal("expected error on StartInstances failure")
	}
	if !strings.Contains(err.Error(), "cannot start instance") {
		t.Errorf("error = %q, want substring %q", err.Error(), "cannot start instance")
	}
}

// ---------------------------------------------------------------------------
// Tests: Default volume size
// ---------------------------------------------------------------------------

func TestProvisionerDefaultVolumeSize(t *testing.T) {
	m := newUpHappyMocks()
	p := m.build()

	cfg := defaultConfig()
	cfg.VolumeSize = 0 // should default to 50

	_, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if aws.ToInt32(m.createVolume.input.Size) != 50 {
		t.Errorf("volume Size = %d, want 50 (default)", aws.ToInt32(m.createVolume.input.Size))
	}
}

func TestProvisionerCustomVolumeSize(t *testing.T) {
	m := newUpHappyMocks()
	p := m.build()

	cfg := defaultConfig()
	cfg.VolumeSize = 100

	_, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if aws.ToInt32(m.createVolume.input.Size) != 100 {
		t.Errorf("volume Size = %d, want 100", aws.ToInt32(m.createVolume.input.Size))
	}
}

// ---------------------------------------------------------------------------
// Tests: Instance tagging
// ---------------------------------------------------------------------------

func TestProvisionerInstanceTags(t *testing.T) {
	m := newUpHappyMocks()
	p := m.build()

	_, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	input := m.runInstances.input
	if len(input.TagSpecifications) == 0 {
		t.Fatal("no TagSpecifications on RunInstances")
	}

	tagSpec := input.TagSpecifications[0]
	if tagSpec.ResourceType != ec2types.ResourceTypeInstance {
		t.Errorf("ResourceType = %q, want instance", tagSpec.ResourceType)
	}

	tagMap := make(map[string]string)
	for _, tag := range tagSpec.Tags {
		tagMap[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}

	assertions := map[string]string{
		tags.TagMint:      "true",
		tags.TagOwner:     "alice",
		tags.TagOwnerARN:  "arn:aws:iam::123:user/alice",
		tags.TagVM:        "default",
		tags.TagComponent: tags.ComponentInstance,
		tags.TagBootstrap: tags.BootstrapPending,
		tags.TagName:      "mint/alice/default",
	}

	for key, want := range assertions {
		if tagMap[key] != want {
			t.Errorf("tag %q = %q, want %q", key, tagMap[key], want)
		}
	}
}

// ---------------------------------------------------------------------------
// Tests: EIP quota check error
// ---------------------------------------------------------------------------

func TestProvisionerEIPQuotaCheckError(t *testing.T) {
	m := newUpHappyMocks()
	m.describeAddrs.err = fmt.Errorf("describe addresses API error")
	p := m.build()

	_, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err == nil {
		t.Fatal("expected error on DescribeAddresses failure")
	}
	if !strings.Contains(err.Error(), "EIP quota") {
		t.Errorf("error = %q, want substring %q", err.Error(), "EIP quota")
	}
}

// ---------------------------------------------------------------------------
// Tests: Full provision flow table-driven
// ---------------------------------------------------------------------------

func TestProvisionerRun(t *testing.T) {
	tests := []struct {
		name           string
		setup          func(*upMocks)
		wantErr        bool
		wantErrContain string
	}{
		{
			name:  "happy path - all resources created",
			setup: func(m *upMocks) {},
		},
		{
			name: "discover VM error",
			setup: func(m *upMocks) {
				m.describeInstances.err = fmt.Errorf("throttled")
			},
			wantErr:        true,
			wantErrContain: "discovering VM",
		},
		{
			name: "bootstrap verification failure aborts",
			setup: func(m *upMocks) {
				m.bootstrapVerifier = func(content []byte) error {
					return fmt.Errorf("hash mismatch")
				}
			},
			wantErr:        true,
			wantErrContain: "bootstrap verification",
		},
		{
			name: "AMI resolution failure aborts",
			setup: func(m *upMocks) {
				m.amiResolver = func(ctx context.Context, client mintaws.GetParameterAPI) (string, error) {
					return "", fmt.Errorf("SSM unavailable")
				}
			},
			wantErr:        true,
			wantErrContain: "resolving AMI",
		},
		{
			name: "EIP quota exceeded aborts",
			setup: func(m *upMocks) {
				addrs := make([]ec2types.Address, DefaultEIPLimit)
				for i := range addrs {
					addrs[i] = ec2types.Address{AllocationId: aws.String(fmt.Sprintf("eip-%d", i))}
				}
				m.describeAddrs.output = &ec2.DescribeAddressesOutput{Addresses: addrs}
			},
			wantErr:        true,
			wantErrContain: "EIP quota exceeded",
		},
		{
			name: "create volume error",
			setup: func(m *upMocks) {
				m.createVolume.err = fmt.Errorf("volume boom")
			},
			wantErr:        true,
			wantErrContain: "volume boom",
		},
		{
			name: "allocate address error",
			setup: func(m *upMocks) {
				m.allocateAddr.err = fmt.Errorf("eip boom")
			},
			wantErr:        true,
			wantErrContain: "eip boom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newUpHappyMocks()
			tt.setup(m)
			p := m.build()

			result, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.wantErrContain != "" && !strings.Contains(err.Error(), tt.wantErrContain) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.wantErrContain)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result == nil {
				t.Fatal("expected non-nil result on success")
			}
		})
	}
}

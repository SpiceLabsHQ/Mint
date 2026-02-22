package provision

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
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

type mockDescribeImages struct {
	output *ec2.DescribeImagesOutput
	err    error
}

func (m *mockDescribeImages) DescribeImages(ctx context.Context, params *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
	return m.output, m.err
}

type mockUpDescribeVolumes struct {
	output *ec2.DescribeVolumesOutput
	err    error
	called bool
	input  *ec2.DescribeVolumesInput
}

func (m *mockUpDescribeVolumes) DescribeVolumes(ctx context.Context, params *ec2.DescribeVolumesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error) {
	m.called = true
	m.input = params
	return m.output, m.err
}

type mockUpDeleteTags struct {
	output *ec2.DeleteTagsOutput
	err    error
	called bool
	input  *ec2.DeleteTagsInput
}

func (m *mockUpDeleteTags) DeleteTags(ctx context.Context, params *ec2.DeleteTagsInput, optFns ...func(*ec2.Options)) (*ec2.DeleteTagsOutput, error) {
	m.called = true
	m.input = params
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
	describeImages    *mockDescribeImages
	describeVolumes   *mockUpDescribeVolumes
	deleteTags        *mockUpDeleteTags

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
					{
						InstanceId: aws.String("i-new123"),
						// BDM volume ID populated in response (normal for synchronous BDM creation).
						BlockDeviceMappings: []ec2types.InstanceBlockDeviceMapping{{
							DeviceName: aws.String("/dev/xvdf"),
							Ebs: &ec2types.EbsInstanceBlockDevice{
								VolumeId: aws.String("vol-proj1"),
							},
						}},
					},
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
		describeImages: &mockDescribeImages{output: &ec2.DescribeImagesOutput{}},
		// No pending-attach volumes by default.
		describeVolumes: &mockUpDescribeVolumes{
			output: &ec2.DescribeVolumesOutput{Volumes: []ec2types.Volume{}},
		},
		deleteTags: &mockUpDeleteTags{
			output: &ec2.DeleteTagsOutput{},
		},
		bootstrapVerifier: func(content []byte) error {
			return nil // always pass
		},
		amiResolver: func(ctx context.Context, client mintaws.DescribeImagesAPI) (string, error) {
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
		m.describeImages,
	)
	p.WithBootstrapVerifier(m.bootstrapVerifier)
	p.WithAMIResolver(m.amiResolver)
	if m.describeVolumes != nil {
		p.WithDescribeVolumes(m.describeVolumes)
	}
	if m.deleteTags != nil {
		p.WithDeleteTags(m.deleteTags)
	}
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

	// Verify project EBS was specified via BlockDeviceMappings in RunInstances.
	if len(input.BlockDeviceMappings) == 0 {
		t.Fatal("RunInstances: expected BlockDeviceMappings for project EBS")
	}
	bdm := input.BlockDeviceMappings[0]
	if aws.ToString(bdm.DeviceName) != "/dev/xvdf" {
		t.Errorf("BDM DeviceName = %q, want /dev/xvdf", aws.ToString(bdm.DeviceName))
	}
	if bdm.Ebs == nil {
		t.Fatal("BDM Ebs is nil")
	}
	if aws.ToInt32(bdm.Ebs.VolumeSize) != 50 {
		t.Errorf("BDM VolumeSize = %d, want 50", aws.ToInt32(bdm.Ebs.VolumeSize))
	}
	if bdm.Ebs.VolumeType != ec2types.VolumeTypeGp3 {
		t.Errorf("BDM VolumeType = %q, want gp3", bdm.Ebs.VolumeType)
	}
	if aws.ToBool(bdm.Ebs.DeleteOnTermination) {
		t.Error("BDM DeleteOnTermination should be false (project data must survive instance termination)")
	}

	// Verify volume was tagged with CreateTags (not separately created).
	if !m.createTags.called {
		t.Fatal("CreateTags was not called to tag the BDM volume")
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
	m.amiResolver = func(ctx context.Context, client mintaws.DescribeImagesAPI) (string, error) {
		return "", fmt.Errorf("describe images: access denied")
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

	if !m.runInstances.called || len(m.runInstances.input.BlockDeviceMappings) == 0 {
		t.Fatal("RunInstances: expected BlockDeviceMappings")
	}
	if got := aws.ToInt32(m.runInstances.input.BlockDeviceMappings[0].Ebs.VolumeSize); got != 50 {
		t.Errorf("BDM VolumeSize = %d, want 50 (default)", got)
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

	if !m.runInstances.called || len(m.runInstances.input.BlockDeviceMappings) == 0 {
		t.Fatal("RunInstances: expected BlockDeviceMappings")
	}
	if got := aws.ToInt32(m.runInstances.input.BlockDeviceMappings[0].Ebs.VolumeSize); got != 100 {
		t.Errorf("BDM VolumeSize = %d, want 100", got)
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
		tags.TagMint:           "true",
		tags.TagOwner:          "alice",
		tags.TagOwnerARN:       "arn:aws:iam::123:user/alice",
		tags.TagVM:             "default",
		tags.TagComponent:      tags.ComponentInstance,
		tags.TagBootstrap:      tags.BootstrapPending,
		tags.TagName:           "mint/alice/default",
		tags.TagRootVolumeGB:   "200",
		tags.TagProjectVolumeGB: "50",
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

// ---------------------------------------------------------------------------
// Tests: Bootstrap variable interpolation
// ---------------------------------------------------------------------------

func TestInterpolateBootstrapReplacesAllVariables(t *testing.T) {
	script := []byte(`#!/bin/bash
EFS_ID="${MINT_EFS_ID}"
PROJECT_DEV="${MINT_PROJECT_DEV}"
VM_NAME="${MINT_VM_NAME}"
IDLE_TIMEOUT="${MINT_IDLE_TIMEOUT:-60}"
echo done`)

	vars := map[string]string{
		"MINT_EFS_ID":       "fs-abc123",
		"MINT_PROJECT_DEV":  "/dev/xvdf",
		"MINT_VM_NAME":      "default",
		"MINT_IDLE_TIMEOUT": "90",
	}

	result := InterpolateBootstrap(script, vars)

	expected := `#!/bin/bash
EFS_ID="fs-abc123"
PROJECT_DEV="/dev/xvdf"
VM_NAME="default"
IDLE_TIMEOUT="90"
echo done`

	if string(result) != expected {
		t.Errorf("InterpolateBootstrap result:\n%s\nwant:\n%s", string(result), expected)
	}
}

func TestInterpolateBootstrapLeavesUnknownVariables(t *testing.T) {
	script := []byte(`#!/bin/bash
KNOWN="${MINT_EFS_ID}"
UNKNOWN="${SOME_OTHER_VAR}"
ALSO_UNKNOWN="${PATH}"
BARE_KNOWN="${MINT_VM_NAME}"`)

	vars := map[string]string{
		"MINT_EFS_ID":  "fs-xyz",
		"MINT_VM_NAME": "myvm",
	}

	result := InterpolateBootstrap(script, vars)

	// Known variables should be replaced
	if !strings.Contains(string(result), `KNOWN="fs-xyz"`) {
		t.Errorf("expected MINT_EFS_ID to be replaced, got:\n%s", string(result))
	}
	if !strings.Contains(string(result), `BARE_KNOWN="myvm"`) {
		t.Errorf("expected MINT_VM_NAME to be replaced, got:\n%s", string(result))
	}

	// Unknown variables should remain untouched
	if !strings.Contains(string(result), "${SOME_OTHER_VAR}") {
		t.Errorf("expected ${SOME_OTHER_VAR} to remain, got:\n%s", string(result))
	}
	if !strings.Contains(string(result), "${PATH}") {
		t.Errorf("expected ${PATH} to remain, got:\n%s", string(result))
	}
}

func TestInterpolateBootstrapHandlesBashDefaults(t *testing.T) {
	// When a variable is in the mapping, ${VAR:-default} should become
	// just the mapped value (the whole ${...} is replaced).
	script := []byte(`TIMEOUT="${MINT_IDLE_TIMEOUT:-60}"`)
	vars := map[string]string{
		"MINT_IDLE_TIMEOUT": "120",
	}

	result := InterpolateBootstrap(script, vars)

	if !strings.Contains(string(result), `TIMEOUT="120"`) {
		t.Errorf("expected bash default expression to be replaced, got:\n%s", string(result))
	}
}

func TestLaunchInstanceInterpolatesBootstrapScript(t *testing.T) {
	m := newUpHappyMocks()
	p := m.build()

	scriptWithVars := []byte(`#!/bin/bash
EFS="${MINT_EFS_ID}"
DEV="${MINT_PROJECT_DEV}"
VM="${MINT_VM_NAME}"
TIMEOUT="${MINT_IDLE_TIMEOUT:-60}"`)

	cfg := ProvisionConfig{
		InstanceType:    "m6i.xlarge",
		VolumeSize:      50,
		BootstrapScript: scriptWithVars,
		EFSID:           "fs-test789",
		IdleTimeout:     45,
	}

	_, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "testvm", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !m.runInstances.called {
		t.Fatal("RunInstances was not called")
	}

	// Decode UserData and verify variables were interpolated.
	ud, err := base64.StdEncoding.DecodeString(aws.ToString(m.runInstances.input.UserData))
	if err != nil {
		t.Fatalf("failed to decode UserData: %v", err)
	}

	decoded := string(ud)
	checks := map[string]string{
		"MINT_EFS_ID":       "fs-test789",
		"MINT_PROJECT_DEV":  "/dev/xvdf",
		"MINT_VM_NAME":      "testvm",
		"MINT_IDLE_TIMEOUT": "45",
	}

	for varName, wantVal := range checks {
		if strings.Contains(decoded, "${"+varName) {
			t.Errorf("UserData still contains uninterpolated ${%s}", varName)
		}
		if !strings.Contains(decoded, wantVal) {
			t.Errorf("UserData missing interpolated value %q for %s\nUserData:\n%s", wantVal, varName, decoded)
		}
	}
}

func TestLaunchInstanceDefaultsIdleTimeout(t *testing.T) {
	m := newUpHappyMocks()
	p := m.build()

	scriptWithVars := []byte(`TIMEOUT="${MINT_IDLE_TIMEOUT:-60}"`)

	cfg := ProvisionConfig{
		InstanceType:    "m6i.xlarge",
		VolumeSize:      50,
		BootstrapScript: scriptWithVars,
		EFSID:           "fs-test789",
		IdleTimeout:     0, // zero means use default (60)
	}

	_, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ud, err := base64.StdEncoding.DecodeString(aws.ToString(m.runInstances.input.UserData))
	if err != nil {
		t.Fatalf("failed to decode UserData: %v", err)
	}

	if !strings.Contains(string(ud), "60") {
		t.Errorf("expected default idle timeout of 60, got:\n%s", string(ud))
	}
}

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
				m.amiResolver = func(ctx context.Context, client mintaws.DescribeImagesAPI) (string, error) {
					return "", fmt.Errorf("describe images: unavailable")
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

// ---------------------------------------------------------------------------
// Tests: Bootstrap polling integration
// ---------------------------------------------------------------------------

func TestProvisionerCallsPollOnFreshProvision(t *testing.T) {
	m := newUpHappyMocks()
	p := m.build()

	pollCalled := false
	var pollOwner, pollVM, pollInstance string
	p.WithBootstrapPollFunc(func(ctx context.Context, owner, vmName, instanceID string) error {
		pollCalled = true
		pollOwner = owner
		pollVM = vmName
		pollInstance = instanceID
		return nil
	})

	result, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !pollCalled {
		t.Error("bootstrap poll function should be called on fresh provision")
	}
	if pollOwner != "alice" {
		t.Errorf("poll owner = %q, want %q", pollOwner, "alice")
	}
	if pollVM != "default" {
		t.Errorf("poll vmName = %q, want %q", pollVM, "default")
	}
	if pollInstance != "i-new123" {
		t.Errorf("poll instanceID = %q, want %q", pollInstance, "i-new123")
	}
	if result.BootstrapError != nil {
		t.Errorf("BootstrapError should be nil on success, got: %v", result.BootstrapError)
	}
}

func TestProvisionerBootstrapPollFailureSetsBootstrapError(t *testing.T) {
	m := newUpHappyMocks()
	p := m.build()

	pollErr := fmt.Errorf("bootstrap timed out")
	p.WithBootstrapPollFunc(func(ctx context.Context, owner, vmName, instanceID string) error {
		return pollErr
	})

	result, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	// Run itself should succeed -- the instance exists.
	if err != nil {
		t.Fatalf("Run should not return error on poll failure, got: %v", err)
	}

	if result.BootstrapError == nil {
		t.Fatal("BootstrapError should be non-nil when poll fails")
	}
	if result.BootstrapError.Error() != "bootstrap timed out" {
		t.Errorf("BootstrapError = %q, want %q", result.BootstrapError.Error(), "bootstrap timed out")
	}

	// Verify the result still contains all resource info.
	if result.InstanceID != "i-new123" {
		t.Errorf("InstanceID = %q, want %q", result.InstanceID, "i-new123")
	}
	if result.PublicIP != "54.1.2.3" {
		t.Errorf("PublicIP = %q, want %q", result.PublicIP, "54.1.2.3")
	}
}

func TestProvisionerNoPollWhenPollerNil(t *testing.T) {
	m := newUpHappyMocks()
	p := m.build()
	// Do NOT set a poll func -- default nil behavior.

	result, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.BootstrapError != nil {
		t.Errorf("BootstrapError should be nil when no poller set, got: %v", result.BootstrapError)
	}
}

func TestProvisionerNoPollOnRestart(t *testing.T) {
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

	pollCalled := false
	p.WithBootstrapPollFunc(func(ctx context.Context, owner, vmName, instanceID string) error {
		pollCalled = true
		return nil
	})

	result, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if pollCalled {
		t.Error("bootstrap poll should NOT be called on restart (Restarted == true)")
	}
	if !result.Restarted {
		t.Error("result.Restarted should be true")
	}
}

// ---------------------------------------------------------------------------
// Tests: Volume size tags on instance (ADR-0004)
// ---------------------------------------------------------------------------

func TestLaunchInstanceIncludesVolumeTagsDefault(t *testing.T) {
	m := newUpHappyMocks()
	p := m.build()

	cfg := defaultConfig()
	cfg.VolumeSize = 0 // should default to 50

	_, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	input := m.runInstances.input
	if len(input.TagSpecifications) == 0 {
		t.Fatal("no TagSpecifications on RunInstances")
	}

	tagMap := make(map[string]string)
	for _, tag := range input.TagSpecifications[0].Tags {
		tagMap[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}

	if got, ok := tagMap[tags.TagRootVolumeGB]; !ok {
		t.Errorf("missing tag %q on instance", tags.TagRootVolumeGB)
	} else if got != "200" {
		t.Errorf("tag %q = %q, want %q", tags.TagRootVolumeGB, got, "200")
	}

	if got, ok := tagMap[tags.TagProjectVolumeGB]; !ok {
		t.Errorf("missing tag %q on instance", tags.TagProjectVolumeGB)
	} else if got != "50" {
		t.Errorf("tag %q = %q, want %q (default)", tags.TagProjectVolumeGB, got, "50")
	}
}

func TestLaunchInstanceIncludesVolumeTagsCustom(t *testing.T) {
	m := newUpHappyMocks()
	p := m.build()

	cfg := defaultConfig()
	cfg.VolumeSize = 100

	_, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	input := m.runInstances.input
	if len(input.TagSpecifications) == 0 {
		t.Fatal("no TagSpecifications on RunInstances")
	}

	tagMap := make(map[string]string)
	for _, tag := range input.TagSpecifications[0].Tags {
		tagMap[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}

	if got, ok := tagMap[tags.TagRootVolumeGB]; !ok {
		t.Errorf("missing tag %q on instance", tags.TagRootVolumeGB)
	} else if got != "200" {
		t.Errorf("tag %q = %q, want %q", tags.TagRootVolumeGB, got, "200")
	}

	if got, ok := tagMap[tags.TagProjectVolumeGB]; !ok {
		t.Errorf("missing tag %q on instance", tags.TagProjectVolumeGB)
	} else if got != "100" {
		t.Errorf("tag %q = %q, want %q", tags.TagProjectVolumeGB, got, "100")
	}
}

// ---------------------------------------------------------------------------
// Tests: Pending-attach volume recovery
// ---------------------------------------------------------------------------

func TestProvisionerPendingAttachHappyPath(t *testing.T) {
	// When a pending-attach volume exists in the same AZ, the provisioner
	// should attach it (skip volume creation) and remove the pending-attach tag.
	m := newUpHappyMocks()
	m.describeVolumes = &mockUpDescribeVolumes{
		output: &ec2.DescribeVolumesOutput{
			Volumes: []ec2types.Volume{{
				VolumeId:         aws.String("vol-pending1"),
				AvailabilityZone: aws.String("us-east-1a"),
			}},
		},
	}
	m.deleteTags = &mockUpDeleteTags{
		output: &ec2.DeleteTagsOutput{},
	}
	p := m.build()

	result, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Volume should be the pending-attach volume, not a newly created one.
	if result.VolumeID != "vol-pending1" {
		t.Errorf("result.VolumeID = %q, want %q", result.VolumeID, "vol-pending1")
	}

	// CreateVolume should NOT be called (volume already exists).
	if m.createVolume.called {
		t.Error("CreateVolume should NOT be called when pending-attach volume exists")
	}

	// AttachVolume should be called with the pending volume.
	if !m.attachVolume.called {
		t.Fatal("AttachVolume was not called")
	}
	if aws.ToString(m.attachVolume.input.VolumeId) != "vol-pending1" {
		t.Errorf("AttachVolume VolumeId = %q, want %q", aws.ToString(m.attachVolume.input.VolumeId), "vol-pending1")
	}
	if aws.ToString(m.attachVolume.input.Device) != "/dev/xvdf" {
		t.Errorf("AttachVolume Device = %q, want %q", aws.ToString(m.attachVolume.input.Device), "/dev/xvdf")
	}

	// DeleteTags should be called to remove the pending-attach tag.
	if !m.deleteTags.called {
		t.Fatal("DeleteTags was not called to remove pending-attach tag")
	}
	if len(m.deleteTags.input.Resources) != 1 || m.deleteTags.input.Resources[0] != "vol-pending1" {
		t.Errorf("DeleteTags resources = %v, want [vol-pending1]", m.deleteTags.input.Resources)
	}
	foundTag := false
	for _, tag := range m.deleteTags.input.Tags {
		if aws.ToString(tag.Key) == tags.TagPendingAttach {
			foundTag = true
		}
	}
	if !foundTag {
		t.Error("DeleteTags did not include mint:pending-attach tag")
	}
}

func TestProvisionerPendingAttachAZMismatch(t *testing.T) {
	// When a pending-attach volume is in a different AZ than the instance,
	// the provisioner should fail fast with guidance.
	m := newUpHappyMocks()
	m.describeVolumes = &mockUpDescribeVolumes{
		output: &ec2.DescribeVolumesOutput{
			Volumes: []ec2types.Volume{{
				VolumeId:         aws.String("vol-wrongaz"),
				AvailabilityZone: aws.String("us-west-2a"), // Different AZ
			}},
		},
	}
	p := m.build()

	_, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err == nil {
		t.Fatal("expected error for AZ mismatch")
	}
	if !strings.Contains(err.Error(), "AZ mismatch") {
		t.Errorf("error = %q, want substring %q", err.Error(), "AZ mismatch")
	}
	if !strings.Contains(err.Error(), "mint destroy") {
		t.Errorf("error should mention 'mint destroy', got: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "vol-wrongaz") {
		t.Errorf("error should include volume ID, got: %q", err.Error())
	}
}

func TestProvisionerPendingAttachNoneFound(t *testing.T) {
	// When no pending-attach volume is found, normal provisioning continues:
	// the project EBS is created via BlockDeviceMappings in RunInstances.
	m := newUpHappyMocks()
	// describeVolumes returns empty (no pending-attach volumes) — this is the default.
	p := m.build()

	result, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Normal flow: BlockDeviceMappings should be present in RunInstances.
	if len(m.runInstances.input.BlockDeviceMappings) == 0 {
		t.Error("RunInstances should have BlockDeviceMappings when no pending-attach volume exists")
	}
	if result.VolumeID != "vol-proj1" {
		t.Errorf("result.VolumeID = %q, want %q (from BDM)", result.VolumeID, "vol-proj1")
	}

	// DeleteTags should NOT be called.
	if m.deleteTags.called {
		t.Error("DeleteTags should NOT be called when no pending-attach volume exists")
	}
}

func TestProvisionerPendingAttachDescribeError(t *testing.T) {
	// When DescribeVolumes fails, the error should propagate.
	m := newUpHappyMocks()
	m.describeVolumes = &mockUpDescribeVolumes{
		err: fmt.Errorf("describe volumes throttled"),
	}
	p := m.build()

	_, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err == nil {
		t.Fatal("expected error from DescribeVolumes failure")
	}
	if !strings.Contains(err.Error(), "pending-attach") {
		t.Errorf("error = %q, want substring %q", err.Error(), "pending-attach")
	}
}

func TestProvisionerPendingAttachAttachError(t *testing.T) {
	// When attaching the pending-attach volume fails, the error should propagate.
	m := newUpHappyMocks()
	m.describeVolumes = &mockUpDescribeVolumes{
		output: &ec2.DescribeVolumesOutput{
			Volumes: []ec2types.Volume{{
				VolumeId:         aws.String("vol-pending1"),
				AvailabilityZone: aws.String("us-east-1a"),
			}},
		},
	}
	m.attachVolume = &mockUpAttachVolume{
		err: fmt.Errorf("volume busy"),
	}
	p := m.build()

	_, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err == nil {
		t.Fatal("expected error from attach failure")
	}
	if !strings.Contains(err.Error(), "volume busy") {
		t.Errorf("error = %q, want substring %q", err.Error(), "volume busy")
	}
}

func TestProvisionerPendingAttachDeleteTagError(t *testing.T) {
	// When removing the pending-attach tag fails, the error should propagate.
	m := newUpHappyMocks()
	m.describeVolumes = &mockUpDescribeVolumes{
		output: &ec2.DescribeVolumesOutput{
			Volumes: []ec2types.Volume{{
				VolumeId:         aws.String("vol-pending1"),
				AvailabilityZone: aws.String("us-east-1a"),
			}},
		},
	}
	m.deleteTags = &mockUpDeleteTags{
		err: fmt.Errorf("delete tags denied"),
	}
	p := m.build()

	_, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err == nil {
		t.Fatal("expected error from DeleteTags failure")
	}
	if !strings.Contains(err.Error(), "delete tags denied") {
		t.Errorf("error = %q, want substring %q", err.Error(), "delete tags denied")
	}
}

func TestProvisionerCrashAfterTerminateRecovery(t *testing.T) {
	// Scenario: mint recreate crashed after terminating the old instance.
	// The old instance no longer exists but the project volume is orphaned
	// with a mint:pending-attach=true tag. Running mint up should recover
	// by attaching the existing volume instead of creating a new one.
	//
	// Timeline:
	//   1. User runs "mint recreate" on VM "default"
	//   2. Recreate stops instance, detaches volume, tags it pending-attach, terminates instance
	//   3. CRASH — recreate dies after terminate but before launching a replacement
	//   4. User runs "mint up" — DescribeInstances finds NO instance (it was terminated)
	//   5. mint up provisions a fresh instance
	//   6. findPendingAttachVolume discovers the orphaned volume
	//   7. The volume is attached to the new instance (skip CreateVolume)
	//   8. The mint:pending-attach tag is removed
	//   9. User's data is preserved

	m := newUpHappyMocks()

	// No existing instance (it was terminated before the crash).
	// newUpHappyMocks() already defaults to empty DescribeInstancesOutput.

	// The orphaned volume from the crashed recreate:
	m.describeVolumes = &mockUpDescribeVolumes{
		output: &ec2.DescribeVolumesOutput{
			Volumes: []ec2types.Volume{{
				VolumeId:         aws.String("vol-orphaned"),
				AvailabilityZone: aws.String("us-east-1a"),
			}},
		},
	}
	m.deleteTags = &mockUpDeleteTags{
		output: &ec2.DeleteTagsOutput{},
	}
	p := m.build()

	result, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The orphaned volume should be recovered, not a new one created.
	if result.VolumeID != "vol-orphaned" {
		t.Errorf("VolumeID = %q, want %q (orphaned volume should be recovered)", result.VolumeID, "vol-orphaned")
	}

	// CreateVolume should NOT be called — the orphaned volume replaces it.
	if m.createVolume.called {
		t.Error("CreateVolume called — should reuse orphaned pending-attach volume")
	}

	// AttachVolume should attach the orphaned volume.
	if !m.attachVolume.called {
		t.Fatal("AttachVolume not called — orphaned volume should be attached to new instance")
	}
	if aws.ToString(m.attachVolume.input.VolumeId) != "vol-orphaned" {
		t.Errorf("AttachVolume VolumeId = %q, want %q", aws.ToString(m.attachVolume.input.VolumeId), "vol-orphaned")
	}

	// DeleteTags should remove the pending-attach tag.
	if !m.deleteTags.called {
		t.Fatal("DeleteTags not called — pending-attach tag should be removed after recovery")
	}

	// A new instance should have been launched (not a restart).
	if result.InstanceID != "i-new123" {
		t.Errorf("InstanceID = %q, want %q (fresh instance should be launched)", result.InstanceID, "i-new123")
	}
}

// ---------------------------------------------------------------------------
// Tests: Volume IOPS configuration
// ---------------------------------------------------------------------------

func TestProvisionerDefaultVolumeIOPS(t *testing.T) {
	// When VolumeIOPS is 0 in ProvisionConfig, the BDM should use 3000 (gp3 default).
	m := newUpHappyMocks()
	p := m.build()

	cfg := defaultConfig()
	cfg.VolumeIOPS = 0 // should default to 3000

	_, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !m.runInstances.called || len(m.runInstances.input.BlockDeviceMappings) == 0 {
		t.Fatal("RunInstances: expected BlockDeviceMappings")
	}
	if got := aws.ToInt32(m.runInstances.input.BlockDeviceMappings[0].Ebs.Iops); got != 3000 {
		t.Errorf("BDM Iops = %d, want 3000 (default)", got)
	}
}

func TestProvisionerCustomVolumeIOPS(t *testing.T) {
	// When VolumeIOPS is explicitly set, it should be in the BDM.
	m := newUpHappyMocks()
	p := m.build()

	cfg := defaultConfig()
	cfg.VolumeIOPS = 6000

	_, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !m.runInstances.called || len(m.runInstances.input.BlockDeviceMappings) == 0 {
		t.Fatal("RunInstances: expected BlockDeviceMappings")
	}
	if got := aws.ToInt32(m.runInstances.input.BlockDeviceMappings[0].Ebs.Iops); got != 6000 {
		t.Errorf("BDM Iops = %d, want 6000", got)
	}
}

func TestProvisionerMaxVolumeIOPS(t *testing.T) {
	m := newUpHappyMocks()
	p := m.build()

	cfg := defaultConfig()
	cfg.VolumeIOPS = 16000

	_, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !m.runInstances.called || len(m.runInstances.input.BlockDeviceMappings) == 0 {
		t.Fatal("RunInstances: expected BlockDeviceMappings")
	}
	if got := aws.ToInt32(m.runInstances.input.BlockDeviceMappings[0].Ebs.Iops); got != 16000 {
		t.Errorf("BDM Iops = %d, want 16000", got)
	}
}

func TestProvisionerNoPollOnRunningVM(t *testing.T) {
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

	pollCalled := false
	p.WithBootstrapPollFunc(func(ctx context.Context, owner, vmName, instanceID string) error {
		pollCalled = true
		return nil
	})

	_, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if pollCalled {
		t.Error("bootstrap poll should NOT be called for already-running VM")
	}
}

// ---------------------------------------------------------------------------
// Logger mock for structured logging tests
// ---------------------------------------------------------------------------

type mockLogger struct {
	mu      sync.Mutex
	entries []mockLogEntry
}

type mockLogEntry struct {
	service   string
	operation string
	duration  time.Duration
	err       error
}

func (l *mockLogger) Log(service, operation string, duration time.Duration, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, mockLogEntry{
		service:   service,
		operation: operation,
		duration:  duration,
		err:       err,
	})
}

func (l *mockLogger) SetStderr(_ io.Writer) {}

func (l *mockLogger) findEntry(operation string) (mockLogEntry, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range l.entries {
		if e.operation == operation {
			return e, true
		}
	}
	return mockLogEntry{}, false
}

// ---------------------------------------------------------------------------
// Tests: Provisioner WithLogger and structured logging
// ---------------------------------------------------------------------------

func TestProvisionerWithLoggerLogsRunInstances(t *testing.T) {
	m := newUpHappyMocks()
	p := m.build()

	logger := &mockLogger{}
	p.WithLogger(logger)

	_, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entry, found := logger.findEntry("RunInstances")
	if !found {
		t.Fatal("logger.Log not called with operation=RunInstances")
	}
	if entry.service != "ec2" {
		t.Errorf("service = %q, want %q", entry.service, "ec2")
	}
	if entry.duration < 0 {
		t.Errorf("duration = %v, want >= 0", entry.duration)
	}
	if entry.err != nil {
		t.Errorf("err = %v, want nil on success", entry.err)
	}
}

func TestProvisionerWithLoggerLogsAllocateAddress(t *testing.T) {
	m := newUpHappyMocks()
	p := m.build()

	logger := &mockLogger{}
	p.WithLogger(logger)

	_, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entry, found := logger.findEntry("AllocateAddress")
	if !found {
		t.Fatal("logger.Log not called with operation=AllocateAddress")
	}
	if entry.service != "ec2" {
		t.Errorf("service = %q, want %q", entry.service, "ec2")
	}
}

func TestProvisionerWithLoggerLogsAssociateAddress(t *testing.T) {
	m := newUpHappyMocks()
	p := m.build()

	logger := &mockLogger{}
	p.WithLogger(logger)

	_, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entry, found := logger.findEntry("AssociateAddress")
	if !found {
		t.Fatal("logger.Log not called with operation=AssociateAddress")
	}
	if entry.service != "ec2" {
		t.Errorf("service = %q, want %q", entry.service, "ec2")
	}
}

func TestProvisionerWithLoggerLogsErrorOnRunInstancesFailure(t *testing.T) {
	m := newUpHappyMocks()
	m.runInstances.err = fmt.Errorf("insufficient capacity")
	p := m.build()

	logger := &mockLogger{}
	p.WithLogger(logger)

	_, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err == nil {
		t.Fatal("expected error")
	}

	entry, found := logger.findEntry("RunInstances")
	if !found {
		t.Fatal("logger.Log not called with operation=RunInstances even on failure")
	}
	if entry.err == nil {
		t.Error("expected non-nil err in log entry when RunInstances fails")
	}
	if !strings.Contains(entry.err.Error(), "insufficient capacity") {
		t.Errorf("logged err = %q, want to contain %q", entry.err.Error(), "insufficient capacity")
	}
}

func TestProvisionerNilLoggerNoChange(t *testing.T) {
	// When no logger is set, provision should succeed without panicking.
	m := newUpHappyMocks()
	p := m.build()
	// No WithLogger call — logger field is nil.

	result, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err != nil {
		t.Fatalf("unexpected error with nil logger: %v", err)
	}
	if result.InstanceID != "i-new123" {
		t.Errorf("result.InstanceID = %q, want %q", result.InstanceID, "i-new123")
	}
}

// ---------------------------------------------------------------------------
// Tests: handleExistingVM bootstrap status checking (fix for #97)
// ---------------------------------------------------------------------------

func runningVMInstance(instanceID, publicIP, bootstrapStatus string) *ec2.DescribeInstancesOutput {
	tags := []ec2types.Tag{
		{Key: aws.String("mint:vm"), Value: aws.String("default")},
		{Key: aws.String("mint:owner"), Value: aws.String("alice")},
	}
	if bootstrapStatus != "" {
		tags = append(tags, ec2types.Tag{
			Key:   aws.String("mint:bootstrap"),
			Value: aws.String(bootstrapStatus),
		})
	}
	return &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{
			Instances: []ec2types.Instance{{
				InstanceId:      aws.String(instanceID),
				InstanceType:    ec2types.InstanceTypeM6iXlarge,
				PublicIpAddress: aws.String(publicIP),
				State: &ec2types.InstanceState{
					Name: ec2types.InstanceStateNameRunning,
				},
				Tags: tags,
			}},
		}},
	}
}

func TestHandleExistingVMBootstrapComplete(t *testing.T) {
	m := newUpHappyMocks()
	m.describeInstances.output = runningVMInstance("i-running1", "54.0.0.2", "complete")
	p := m.build()

	result, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.InstanceID != "i-running1" {
		t.Errorf("InstanceID = %q, want %q", result.InstanceID, "i-running1")
	}
	if result.BootstrapError != nil {
		t.Errorf("BootstrapError should be nil for complete bootstrap, got: %v", result.BootstrapError)
	}
	if !result.AlreadyRunning {
		t.Error("AlreadyRunning should be true for existing running VM")
	}
}

func TestHandleExistingVMBootstrapPending(t *testing.T) {
	m := newUpHappyMocks()
	m.describeInstances.output = runningVMInstance("i-running1", "54.0.0.2", "pending")
	p := m.build()

	result, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.InstanceID != "i-running1" {
		t.Errorf("InstanceID = %q, want %q", result.InstanceID, "i-running1")
	}
	// Pending bootstrap must NOT set BootstrapError — that's for failed only.
	// But AlreadyRunning must be true so printUpHuman knows to show in-progress message.
	if !result.AlreadyRunning {
		t.Error("AlreadyRunning should be true for existing running VM")
	}
	// BootstrapStatus must be surfaced so the caller can distinguish pending from complete.
	if result.BootstrapStatus != "pending" {
		t.Errorf("BootstrapStatus = %q, want %q", result.BootstrapStatus, "pending")
	}
}

func TestHandleExistingVMBootstrapFailed(t *testing.T) {
	m := newUpHappyMocks()
	m.describeInstances.output = runningVMInstance("i-running1", "54.0.0.2", "failed")
	p := m.build()

	result, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.InstanceID != "i-running1" {
		t.Errorf("InstanceID = %q, want %q", result.InstanceID, "i-running1")
	}
	if !result.AlreadyRunning {
		t.Error("AlreadyRunning should be true for existing running VM")
	}
	if result.BootstrapError == nil {
		t.Fatal("BootstrapError should be non-nil for failed bootstrap")
	}
	if !strings.Contains(result.BootstrapError.Error(), "bootstrap failed") {
		t.Errorf("BootstrapError = %q, want substring %q", result.BootstrapError.Error(), "bootstrap failed")
	}
	if !strings.Contains(result.BootstrapError.Error(), "mint recreate") {
		t.Errorf("BootstrapError = %q, should mention 'mint recreate'", result.BootstrapError.Error())
	}
}

func TestHandleExistingVMBootstrapStatusEmpty(t *testing.T) {
	// When the bootstrap tag is absent (e.g. very old VM), treat as pending.
	m := newUpHappyMocks()
	m.describeInstances.output = runningVMInstance("i-running1", "54.0.0.2", "")
	p := m.build()

	result, err := p.Run(context.Background(), "alice", "arn:aws:iam::123:user/alice", "default", defaultConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.AlreadyRunning {
		t.Error("AlreadyRunning should be true for existing running VM")
	}
	// No error — unknown status is treated as pending, not failed.
	if result.BootstrapError != nil {
		t.Errorf("BootstrapError should be nil for unknown/empty bootstrap status, got: %v", result.BootstrapError)
	}
}

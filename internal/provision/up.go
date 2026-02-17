// Package provision implements the core provisioning logic for Mint.
// This file contains the Provisioner, which handles the full "mint up"
// flow: check for existing VM, verify bootstrap integrity, resolve AMI,
// launch instance, create project volume, allocate EIP, and tag all resources.
package provision

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	mintaws "github.com/nicholasgasior/mint/internal/aws"
	"github.com/nicholasgasior/mint/internal/bootstrap"
	"github.com/nicholasgasior/mint/internal/tags"
	"github.com/nicholasgasior/mint/internal/vm"
)

// DefaultEIPLimit is the default per-user EIP allocation limit.
const DefaultEIPLimit = 5

// ProvisionConfig holds the user-provided configuration for provisioning.
type ProvisionConfig struct {
	InstanceType    string
	VolumeSize      int32
	BootstrapScript []byte
}

// ProvisionResult holds the outcome of a successful provision run.
type ProvisionResult struct {
	InstanceID   string
	PublicIP     string
	VolumeID     string
	AllocationID string
	Restarted    bool
}

// BootstrapVerifier is a function that verifies bootstrap script integrity.
// Defaults to bootstrap.Verify; overridden in tests.
type BootstrapVerifier func(content []byte) error

// AMIResolver is a function that resolves the current AMI ID.
// Defaults to mintaws.ResolveAMI; overridden in tests.
type AMIResolver func(ctx context.Context, client mintaws.GetParameterAPI) (string, error)

// Provisioner orchestrates the full "mint up" provisioning flow.
// All AWS dependencies are injected via narrow interfaces for testability.
type Provisioner struct {
	describeInstances mintaws.DescribeInstancesAPI
	startInstances    mintaws.StartInstancesAPI
	runInstances      mintaws.RunInstancesAPI
	describeSGs       mintaws.DescribeSecurityGroupsAPI
	describeSubnets   mintaws.DescribeSubnetsAPI
	createVolume      mintaws.CreateVolumeAPI
	attachVolume      mintaws.AttachVolumeAPI
	allocateAddr      mintaws.AllocateAddressAPI
	associateAddr     mintaws.AssociateAddressAPI
	describeAddrs     mintaws.DescribeAddressesAPI
	createTags        mintaws.CreateTagsAPI
	ssmClient         mintaws.GetParameterAPI

	verifyBootstrap BootstrapVerifier
	resolveAMI      AMIResolver
}

// NewProvisioner creates a Provisioner with all required AWS interfaces.
func NewProvisioner(
	describeInstances mintaws.DescribeInstancesAPI,
	startInstances mintaws.StartInstancesAPI,
	runInstances mintaws.RunInstancesAPI,
	describeSGs mintaws.DescribeSecurityGroupsAPI,
	describeSubnets mintaws.DescribeSubnetsAPI,
	createVolume mintaws.CreateVolumeAPI,
	attachVolume mintaws.AttachVolumeAPI,
	allocateAddr mintaws.AllocateAddressAPI,
	associateAddr mintaws.AssociateAddressAPI,
	describeAddrs mintaws.DescribeAddressesAPI,
	createTags mintaws.CreateTagsAPI,
	ssmClient mintaws.GetParameterAPI,
) *Provisioner {
	return &Provisioner{
		describeInstances: describeInstances,
		startInstances:    startInstances,
		runInstances:      runInstances,
		describeSGs:       describeSGs,
		describeSubnets:   describeSubnets,
		createVolume:      createVolume,
		attachVolume:      attachVolume,
		allocateAddr:      allocateAddr,
		associateAddr:     associateAddr,
		describeAddrs:     describeAddrs,
		createTags:        createTags,
		ssmClient:         ssmClient,
		verifyBootstrap:   bootstrap.Verify,
		resolveAMI:        mintaws.ResolveAMI,
	}
}

// WithBootstrapVerifier overrides the default bootstrap verifier (for testing).
func (p *Provisioner) WithBootstrapVerifier(v BootstrapVerifier) *Provisioner {
	p.verifyBootstrap = v
	return p
}

// WithAMIResolver overrides the default AMI resolver (for testing).
func (p *Provisioner) WithAMIResolver(r AMIResolver) *Provisioner {
	p.resolveAMI = r
	return p
}

// Run executes the full provision flow.
func (p *Provisioner) Run(ctx context.Context, owner, ownerARN, vmName string, cfg ProvisionConfig) (*ProvisionResult, error) {
	// Step 1: Check for existing VM.
	existing, err := vm.FindVM(ctx, p.describeInstances, owner, vmName)
	if err != nil {
		return nil, fmt.Errorf("discovering VM: %w", err)
	}

	if existing != nil {
		return p.handleExistingVM(ctx, existing)
	}

	// Step 2: Verify bootstrap script integrity (ADR-0009).
	if err := p.verifyBootstrap(cfg.BootstrapScript); err != nil {
		return nil, fmt.Errorf("bootstrap verification failed: %w", err)
	}

	// Step 3: Resolve Ubuntu 24.04 AMI.
	amiID, err := p.resolveAMI(ctx, p.ssmClient)
	if err != nil {
		return nil, fmt.Errorf("resolving AMI: %w", err)
	}

	// Step 4: Check EIP quota.
	if err := p.checkEIPQuota(ctx, owner); err != nil {
		return nil, err
	}

	// Step 5: Find user's security group.
	userSGID, err := p.findSecurityGroup(ctx, owner, tags.ComponentSecurityGroup)
	if err != nil {
		return nil, fmt.Errorf("finding user security group: %w", err)
	}

	// Step 6: Find admin EFS security group.
	adminSGID, err := p.findAdminSecurityGroup(ctx)
	if err != nil {
		return nil, fmt.Errorf("finding admin security group: %w", err)
	}

	// Step 7: Find a subnet in the default VPC.
	subnetID, az, err := p.findSubnet(ctx)
	if err != nil {
		return nil, fmt.Errorf("finding subnet: %w", err)
	}

	// Step 8: Launch EC2 instance.
	instanceID, err := p.launchInstance(ctx, amiID, cfg, userSGID, adminSGID, subnetID, owner, ownerARN, vmName)
	if err != nil {
		return nil, fmt.Errorf("launching instance: %w", err)
	}

	// Step 9: Create and attach project EBS volume.
	volumeSize := cfg.VolumeSize
	if volumeSize == 0 {
		volumeSize = 50
	}
	volumeID, err := p.createAndAttachVolume(ctx, instanceID, az, volumeSize, owner, ownerARN, vmName)
	if err != nil {
		return nil, fmt.Errorf("creating project volume: %w", err)
	}

	// Step 10: Allocate and associate Elastic IP.
	allocID, publicIP, err := p.allocateAndAssociateEIP(ctx, instanceID, owner, ownerARN, vmName)
	if err != nil {
		return nil, fmt.Errorf("allocating Elastic IP: %w", err)
	}

	return &ProvisionResult{
		InstanceID:   instanceID,
		PublicIP:     publicIP,
		VolumeID:     volumeID,
		AllocationID: allocID,
	}, nil
}

// handleExistingVM starts a stopped VM or returns info about a running VM.
func (p *Provisioner) handleExistingVM(ctx context.Context, existing *vm.VM) (*ProvisionResult, error) {
	if existing.State == string(ec2types.InstanceStateNameStopped) {
		_, err := p.startInstances.StartInstances(ctx, &ec2.StartInstancesInput{
			InstanceIds: []string{existing.ID},
		})
		if err != nil {
			return nil, fmt.Errorf("starting stopped VM %s: %w", existing.ID, err)
		}
		return &ProvisionResult{
			InstanceID: existing.ID,
			PublicIP:   existing.PublicIP,
			Restarted:  true,
		}, nil
	}

	// VM exists and is running (or in another non-stopped state).
	return &ProvisionResult{
		InstanceID: existing.ID,
		PublicIP:   existing.PublicIP,
	}, nil
}

// checkEIPQuota checks if the user has room for another EIP allocation.
func (p *Provisioner) checkEIPQuota(ctx context.Context, owner string) error {
	out, err := p.describeAddrs.DescribeAddresses(ctx, &ec2.DescribeAddressesInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("tag:" + tags.TagMint), Values: []string{"true"}},
			{Name: aws.String("tag:" + tags.TagOwner), Values: []string{owner}},
		},
	})
	if err != nil {
		return fmt.Errorf("checking EIP quota: %w", err)
	}

	count := len(out.Addresses)
	if count >= DefaultEIPLimit {
		return fmt.Errorf(
			"EIP quota exceeded: you have %d of %d allowed Elastic IPs. "+
				"Release unused EIPs at https://console.aws.amazon.com/vpc/home#Addresses: "+
				"or run 'mint destroy' on unused VMs to free allocations",
			count, DefaultEIPLimit,
		)
	}

	return nil
}

// findSecurityGroup discovers a security group by owner and component tags.
func (p *Provisioner) findSecurityGroup(ctx context.Context, owner, component string) (string, error) {
	out, err := p.describeSGs.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("tag:" + tags.TagMint), Values: []string{"true"}},
			{Name: aws.String("tag:" + tags.TagOwner), Values: []string{owner}},
			{Name: aws.String("tag:" + tags.TagComponent), Values: []string{component}},
		},
	})
	if err != nil {
		return "", fmt.Errorf("describe security groups: %w", err)
	}

	if len(out.SecurityGroups) == 0 {
		return "", fmt.Errorf("no security group found with tags mint:owner=%s, mint:component=%s — run 'mint init' first", owner, component)
	}

	return aws.ToString(out.SecurityGroups[0].GroupId), nil
}

// findAdminSecurityGroup discovers the admin EFS security group by tags.
func (p *Provisioner) findAdminSecurityGroup(ctx context.Context) (string, error) {
	out, err := p.describeSGs.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("tag:" + tags.TagMint), Values: []string{"true"}},
			{Name: aws.String("tag:" + tags.TagComponent), Values: []string{"admin"}},
		},
	})
	if err != nil {
		return "", fmt.Errorf("describe admin security groups: %w", err)
	}

	if len(out.SecurityGroups) == 0 {
		return "", fmt.Errorf("no admin security group found — run the admin setup CloudFormation stack first")
	}

	return aws.ToString(out.SecurityGroups[0].GroupId), nil
}

// findSubnet finds the first public subnet in the default VPC.
func (p *Provisioner) findSubnet(ctx context.Context) (subnetID, az string, err error) {
	out, err := p.describeSubnets.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("default-for-az"), Values: []string{"true"}},
		},
	})
	if err != nil {
		return "", "", fmt.Errorf("describe subnets: %w", err)
	}

	if len(out.Subnets) == 0 {
		return "", "", fmt.Errorf("no default subnets found — mint requires a default VPC with subnets (ADR-0010)")
	}

	subnet := out.Subnets[0]
	return aws.ToString(subnet.SubnetId), aws.ToString(subnet.AvailabilityZone), nil
}

// launchInstance runs a new EC2 instance with the given configuration.
func (p *Provisioner) launchInstance(
	ctx context.Context,
	amiID string,
	cfg ProvisionConfig,
	userSGID, adminSGID, subnetID string,
	owner, ownerARN, vmName string,
) (string, error) {
	userData := base64.StdEncoding.EncodeToString(cfg.BootstrapScript)

	instanceTags := tags.NewTagBuilder(owner, ownerARN, vmName).
		WithComponent(tags.ComponentInstance).
		WithBootstrap(tags.BootstrapPending).
		Build()

	instanceType := ec2types.InstanceType(cfg.InstanceType)

	input := &ec2.RunInstancesInput{
		ImageId:      aws.String(amiID),
		InstanceType: instanceType,
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
		SubnetId:     aws.String(subnetID),
		SecurityGroupIds: []string{
			userSGID,
			adminSGID,
		},
		UserData: aws.String(userData),
		IamInstanceProfile: &ec2types.IamInstanceProfileSpecification{
			Name: aws.String("mint-instance-profile"),
		},
		TagSpecifications: []ec2types.TagSpecification{
			{
				ResourceType: ec2types.ResourceTypeInstance,
				Tags:         instanceTags,
			},
		},
	}

	out, err := p.runInstances.RunInstances(ctx, input)
	if err != nil {
		return "", fmt.Errorf("run instances: %w", err)
	}

	if len(out.Instances) == 0 {
		return "", fmt.Errorf("run instances returned no instances")
	}

	return aws.ToString(out.Instances[0].InstanceId), nil
}

// createAndAttachVolume creates a gp3 project EBS volume and attaches it.
func (p *Provisioner) createAndAttachVolume(
	ctx context.Context,
	instanceID, az string,
	sizeGB int32,
	owner, ownerARN, vmName string,
) (string, error) {
	volumeTags := tags.NewTagBuilder(owner, ownerARN, vmName).
		WithComponent(tags.ComponentProjectVolume).
		Build()

	createOut, err := p.createVolume.CreateVolume(ctx, &ec2.CreateVolumeInput{
		AvailabilityZone: aws.String(az),
		Size:             aws.Int32(sizeGB),
		VolumeType:       ec2types.VolumeTypeGp3,
		TagSpecifications: []ec2types.TagSpecification{
			{
				ResourceType: ec2types.ResourceTypeVolume,
				Tags:         volumeTags,
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("create volume: %w", err)
	}

	volumeID := aws.ToString(createOut.VolumeId)

	_, err = p.attachVolume.AttachVolume(ctx, &ec2.AttachVolumeInput{
		VolumeId:   aws.String(volumeID),
		InstanceId: aws.String(instanceID),
		Device:     aws.String("/dev/xvdf"),
	})
	if err != nil {
		return "", fmt.Errorf("attach volume %s to %s: %w", volumeID, instanceID, err)
	}

	return volumeID, nil
}

// allocateAndAssociateEIP allocates an Elastic IP and associates it with the instance.
func (p *Provisioner) allocateAndAssociateEIP(
	ctx context.Context,
	instanceID string,
	owner, ownerARN, vmName string,
) (allocID, publicIP string, err error) {
	eipTags := tags.NewTagBuilder(owner, ownerARN, vmName).
		WithComponent(tags.ComponentElasticIP).
		Build()

	allocOut, err := p.allocateAddr.AllocateAddress(ctx, &ec2.AllocateAddressInput{
		Domain: ec2types.DomainTypeVpc,
		TagSpecifications: []ec2types.TagSpecification{
			{
				ResourceType: ec2types.ResourceTypeElasticIp,
				Tags:         eipTags,
			},
		},
	})
	if err != nil {
		return "", "", fmt.Errorf("allocate address: %w", err)
	}

	allocID = aws.ToString(allocOut.AllocationId)
	publicIP = aws.ToString(allocOut.PublicIp)

	_, err = p.associateAddr.AssociateAddress(ctx, &ec2.AssociateAddressInput{
		AllocationId: aws.String(allocID),
		InstanceId:   aws.String(instanceID),
	})
	if err != nil {
		return "", "", fmt.Errorf("associate address %s to %s: %w", allocID, instanceID, err)
	}

	return allocID, publicIP, nil
}

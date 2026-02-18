// Package provision implements the core provisioning logic for Mint.
// This file contains the Provisioner, which handles the full "mint up"
// flow: check for existing VM, verify bootstrap integrity, resolve AMI,
// launch instance, create project volume, allocate EIP, and tag all resources.
package provision

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

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
	VolumeIOPS      int32  // IOPS for the project gp3 EBS volume (0 defaults to 3000)
	BootstrapScript []byte
	EFSID           string // EFS filesystem ID for user storage
	IdleTimeout     int    // Idle timeout in minutes (0 defaults to 60)
}

// ProvisionResult holds the outcome of a successful provision run.
type ProvisionResult struct {
	InstanceID     string
	PublicIP       string
	VolumeID       string
	AllocationID   string
	Restarted      bool
	BootstrapError error // non-nil if bootstrap polling failed/timed out
}

// BootstrapVerifier is a function that verifies bootstrap script integrity.
// Defaults to bootstrap.Verify; overridden in tests.
type BootstrapVerifier func(content []byte) error

// BootstrapPollFunc is a function that polls for bootstrap completion.
// Matches the signature of BootstrapPoller.Poll for test injection.
type BootstrapPollFunc func(ctx context.Context, owner, vmName, instanceID string) error

// AMIResolver is a function that resolves the current AMI ID.
// Defaults to mintaws.ResolveAMI; overridden in tests.
type AMIResolver func(ctx context.Context, client mintaws.GetParameterAPI) (string, error)

// DeleteTagsAPI defines the subset of the EC2 API used for removing tags.
type DeleteTagsAPI interface {
	DeleteTags(ctx context.Context, params *ec2.DeleteTagsInput, optFns ...func(*ec2.Options)) (*ec2.DeleteTagsOutput, error)
}

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
	describeVolumes   mintaws.DescribeVolumesAPI
	deleteTags        DeleteTagsAPI

	verifyBootstrap BootstrapVerifier
	resolveAMI      AMIResolver
	pollBootstrap   BootstrapPollFunc
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

// WithDescribeVolumes sets the DescribeVolumes client for pending-attach recovery.
func (p *Provisioner) WithDescribeVolumes(dv mintaws.DescribeVolumesAPI) *Provisioner {
	p.describeVolumes = dv
	return p
}

// WithDeleteTags sets the DeleteTags client for pending-attach tag cleanup.
func (p *Provisioner) WithDeleteTags(dt DeleteTagsAPI) *Provisioner {
	p.deleteTags = dt
	return p
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

// WithBootstrapPollFunc sets a function to poll for bootstrap completion.
// When set, Run() calls this after EIP allocation on fresh provisions (not restarts).
// Use WithBootstrapPoller for production; this method enables test injection.
func (p *Provisioner) WithBootstrapPollFunc(fn BootstrapPollFunc) *Provisioner {
	p.pollBootstrap = fn
	return p
}

// WithBootstrapPoller sets a BootstrapPoller to poll for bootstrap completion.
// Wraps the poller's Poll method as a BootstrapPollFunc.
func (p *Provisioner) WithBootstrapPoller(bp *BootstrapPoller) *Provisioner {
	p.pollBootstrap = bp.Poll
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
	// Check for a pending-attach volume first (crash recovery from mint recreate).
	volumeSize := cfg.VolumeSize
	if volumeSize == 0 {
		volumeSize = 50
	}

	var volumeID string
	pendingVolID, pendingVolAZ, pendingErr := p.findPendingAttachVolume(ctx, owner, vmName)
	if pendingErr != nil {
		return nil, fmt.Errorf("checking pending-attach volumes: %w", pendingErr)
	}

	if pendingVolID != "" {
		// Found a pending-attach volume from a previous recreate.
		// Verify AZ match before attaching.
		if pendingVolAZ != az {
			return nil, fmt.Errorf(
				"pending-attach volume %s is in %s but instance launched in %s — "+
					"run 'mint destroy' and start fresh to resolve this AZ mismatch",
				pendingVolID, pendingVolAZ, az,
			)
		}

		// Attach the existing volume instead of creating a new one.
		_, attachErr := p.attachVolume.AttachVolume(ctx, &ec2.AttachVolumeInput{
			VolumeId:   aws.String(pendingVolID),
			InstanceId: aws.String(instanceID),
			Device:     aws.String("/dev/xvdf"),
		})
		if attachErr != nil {
			return nil, fmt.Errorf("attaching pending-attach volume %s to %s: %w", pendingVolID, instanceID, attachErr)
		}

		// Remove the pending-attach tag via DeleteTags.
		if p.deleteTags != nil {
			_, delErr := p.deleteTags.DeleteTags(ctx, &ec2.DeleteTagsInput{
				Resources: []string{pendingVolID},
				Tags: []ec2types.Tag{
					{Key: aws.String(tags.TagPendingAttach)},
				},
			})
			if delErr != nil {
				return nil, fmt.Errorf("removing pending-attach tag from %s: %w", pendingVolID, delErr)
			}
		}

		volumeID = pendingVolID
	} else {
		// No pending-attach volume — create a new one.
		volumeIOPS := cfg.VolumeIOPS
		if volumeIOPS == 0 {
			volumeIOPS = 3000
		}
		var createErr error
		volumeID, createErr = p.createAndAttachVolume(ctx, instanceID, az, volumeSize, volumeIOPS, owner, ownerARN, vmName)
		if createErr != nil {
			return nil, fmt.Errorf("creating project volume: %w", createErr)
		}
	}

	// Step 10: Allocate and associate Elastic IP.
	allocID, publicIP, err := p.allocateAndAssociateEIP(ctx, instanceID, owner, ownerARN, vmName)
	if err != nil {
		return nil, fmt.Errorf("allocating Elastic IP: %w", err)
	}

	result := &ProvisionResult{
		InstanceID:   instanceID,
		PublicIP:     publicIP,
		VolumeID:     volumeID,
		AllocationID: allocID,
	}

	// Step 11: Poll for bootstrap completion (if poller configured).
	if p.pollBootstrap != nil {
		if pollErr := p.pollBootstrap(ctx, owner, vmName, instanceID); pollErr != nil {
			result.BootstrapError = pollErr
		}
	}

	return result, nil
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

// InterpolateBootstrap substitutes Mint-specific variables in the bootstrap
// script. Only variables present in the vars map are replaced; all other
// ${...} expressions (including bash defaults like ${VAR:-default}) are left
// untouched so the shell can evaluate them normally.
func InterpolateBootstrap(script []byte, vars map[string]string) []byte {
	result := string(script)
	for name, value := range vars {
		// Replace ${VAR:-default} patterns first (bash default syntax).
		// We need to handle these because os.Expand won't match them.
		// Find and replace any ${NAME...} where NAME matches our variable.
		result = ReplaceBashVar(result, name, value)
	}
	return []byte(result)
}

// ReplaceBashVar replaces all occurrences of ${name}, ${name:-...}, and
// ${name-...} with the given value. This handles bash default-value syntax
// so that Go sets the value explicitly rather than relying on shell defaults.
func ReplaceBashVar(s, name, value string) string {
	var b strings.Builder
	b.Grow(len(s))

	for i := 0; i < len(s); i++ {
		if i+1 < len(s) && s[i] == '$' && s[i+1] == '{' {
			// Check if this ${...} starts with our variable name.
			rest := s[i+2:]
			if strings.HasPrefix(rest, name) {
				after := rest[len(name):]
				if len(after) > 0 && after[0] == '}' {
					// Exact match: ${NAME}
					b.WriteString(value)
					i += 2 + len(name) // skip past }
					continue
				}
				if len(after) > 0 && (after[0] == ':' || after[0] == '-') {
					// Bash default syntax: ${NAME:-default} or ${NAME-default}
					// Find the closing brace.
					closeBrace := strings.IndexByte(after, '}')
					if closeBrace >= 0 {
						b.WriteString(value)
						i += 2 + len(name) + closeBrace // skip past }
						continue
					}
				}
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// launchInstance runs a new EC2 instance with the given configuration.
func (p *Provisioner) launchInstance(
	ctx context.Context,
	amiID string,
	cfg ProvisionConfig,
	userSGID, adminSGID, subnetID string,
	owner, ownerARN, vmName string,
) (string, error) {
	idleTimeout := cfg.IdleTimeout
	if idleTimeout == 0 {
		idleTimeout = 60
	}

	interpolated := InterpolateBootstrap(cfg.BootstrapScript, map[string]string{
		"MINT_EFS_ID":       cfg.EFSID,
		"MINT_PROJECT_DEV":  "/dev/xvdf",
		"MINT_VM_NAME":      vmName,
		"MINT_IDLE_TIMEOUT": strconv.Itoa(idleTimeout),
	})
	userData := base64.StdEncoding.EncodeToString(interpolated)

	instanceTags := tags.NewTagBuilder(owner, ownerARN, vmName).
		WithComponent(tags.ComponentInstance).
		WithBootstrap(tags.BootstrapPending).
		Build()

	// Add volume size tags for mint status to read back (ADR-0004).
	projectVolSize := cfg.VolumeSize
	if projectVolSize == 0 {
		projectVolSize = 50
	}
	instanceTags = append(instanceTags,
		ec2types.Tag{Key: aws.String(tags.TagRootVolumeGB), Value: aws.String("200")},
		ec2types.Tag{Key: aws.String(tags.TagProjectVolumeGB), Value: aws.String(strconv.Itoa(int(projectVolSize)))},
	)

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

// findPendingAttachVolume checks for a project EBS volume with the
// mint:pending-attach tag, indicating a crash-recovery scenario from
// mint recreate. Returns empty strings if no pending volume is found
// or if the describeVolumes client is not configured.
func (p *Provisioner) findPendingAttachVolume(ctx context.Context, owner, vmName string) (volumeID, az string, err error) {
	if p.describeVolumes == nil {
		return "", "", nil
	}

	filters := []ec2types.Filter{
		{Name: aws.String("tag:" + tags.TagMint), Values: []string{"true"}},
		{Name: aws.String("tag:" + tags.TagComponent), Values: []string{tags.ComponentProjectVolume}},
		{Name: aws.String("tag:" + tags.TagOwner), Values: []string{owner}},
		{Name: aws.String("tag:" + tags.TagVM), Values: []string{vmName}},
		{Name: aws.String("tag:" + tags.TagPendingAttach), Values: []string{"true"}},
	}

	out, err := p.describeVolumes.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{
		Filters: filters,
	})
	if err != nil {
		return "", "", fmt.Errorf("describe pending-attach volumes: %w", err)
	}

	if len(out.Volumes) == 0 {
		return "", "", nil
	}

	vol := out.Volumes[0]
	return aws.ToString(vol.VolumeId), aws.ToString(vol.AvailabilityZone), nil
}

// createAndAttachVolume creates a gp3 project EBS volume and attaches it.
func (p *Provisioner) createAndAttachVolume(
	ctx context.Context,
	instanceID, az string,
	sizeGB int32,
	iops int32,
	owner, ownerARN, vmName string,
) (string, error) {
	volumeTags := tags.NewTagBuilder(owner, ownerARN, vmName).
		WithComponent(tags.ComponentProjectVolume).
		Build()

	createOut, err := p.createVolume.CreateVolume(ctx, &ec2.CreateVolumeInput{
		AvailabilityZone: aws.String(az),
		Size:             aws.Int32(sizeGB),
		Iops:             aws.Int32(iops),
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

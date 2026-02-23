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
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	mintaws "github.com/nicholasgasior/mint/internal/aws"
	"github.com/nicholasgasior/mint/internal/bootstrap"
	"github.com/nicholasgasior/mint/internal/logging"
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
	InstanceID      string
	PublicIP        string
	VolumeID        string
	AllocationID    string
	Restarted       bool
	AlreadyRunning  bool   // true when the VM was already running (not freshly provisioned or restarted)
	BootstrapStatus string // the mint:bootstrap tag value at the time of the call ("pending", "complete", "failed", or "")
	BootstrapError  error  // non-nil if bootstrap polling failed/timed out, or if an existing VM's bootstrap has failed
}

// BootstrapVerifier is a function that verifies bootstrap script integrity.
// Defaults to bootstrap.Verify; overridden in tests.
type BootstrapVerifier func(content []byte) error

// BootstrapPollFunc is a function that polls for bootstrap completion.
// Matches the signature of BootstrapPoller.Poll for test injection.
type BootstrapPollFunc func(ctx context.Context, owner, vmName, instanceID string) error

// AMIResolver is a function that resolves the current AMI ID.
// Defaults to mintaws.ResolveAMI; overridden in tests.
type AMIResolver func(ctx context.Context, client mintaws.DescribeImagesAPI) (string, error)

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
	describeImages    mintaws.DescribeImagesAPI
	waitRunning          mintaws.WaitInstanceRunningAPI
	waitVolumeAvailable  mintaws.WaitVolumeAvailableAPI
	describeVolumes      mintaws.DescribeVolumesAPI
	deleteTags        DeleteTagsAPI

	verifyBootstrap BootstrapVerifier
	resolveAMI      AMIResolver
	pollBootstrap   BootstrapPollFunc

	logger logging.Logger
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
	describeImages mintaws.DescribeImagesAPI,
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
		describeImages:    describeImages,
		verifyBootstrap:   bootstrap.Verify,
		resolveAMI:        mintaws.ResolveAMI,
	}
}

// WithWaitRunning sets the waiter used to block until the instance is running
// before attaching the EBS volume. When nil, no wait is performed (tests).
func (p *Provisioner) WithWaitRunning(w mintaws.WaitInstanceRunningAPI) *Provisioner {
	p.waitRunning = w
	return p
}

// WithWaitVolumeAvailable sets the waiter used to block until the EBS volume
// is available before attaching it. When nil, no wait is performed (tests).
func (p *Provisioner) WithWaitVolumeAvailable(w mintaws.WaitVolumeAvailableAPI) *Provisioner {
	p.waitVolumeAvailable = w
	return p
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

// WithLogger sets the structured logger for AWS API call timing and error logging.
// When nil (the default), logging is skipped and there is no behavioral change.
func (p *Provisioner) WithLogger(l logging.Logger) *Provisioner {
	p.logger = l
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
	amiID, err := p.resolveAMI(ctx, p.describeImages)
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

	// Step 7.5: Check for a pending-attach volume BEFORE launch so we know
	// whether to include BlockDeviceMappings in RunInstances.
	pendingVolID, pendingVolAZ, pendingErr := p.findPendingAttachVolume(ctx, owner, vmName)
	if pendingErr != nil {
		return nil, fmt.Errorf("checking pending-attach volumes: %w", pendingErr)
	}

	volumeSize := cfg.VolumeSize
	if volumeSize == 0 {
		volumeSize = 50
	}
	volumeIOPS := cfg.VolumeIOPS
	if volumeIOPS == 0 {
		volumeIOPS = 3000
	}

	// For fresh provisions, create the project EBS via BlockDeviceMappings so
	// the device is attached before user-data runs (eliminates the race where
	// bootstrap reaches the EBS step before the volume is attached).
	// For pending-attach recovery, skip BDM and attach the existing volume after launch.
	launchVolSize, launchVolIOPS := volumeSize, volumeIOPS
	if pendingVolID != "" {
		launchVolSize = 0
		launchVolIOPS = 0
	}

	// Step 8: Launch EC2 instance.
	instanceID, bdmVolumeID, err := p.launchInstance(ctx, amiID, cfg, userSGID, adminSGID, subnetID, owner, ownerARN, vmName, launchVolSize, launchVolIOPS)
	if err != nil {
		return nil, fmt.Errorf("launching instance: %w", err)
	}

	// Step 9: Wait for instance to reach running state.
	if p.waitRunning != nil {
		if err := p.waitRunning.Wait(ctx, &ec2.DescribeInstancesInput{
			InstanceIds: []string{instanceID},
		}, 5*time.Minute); err != nil {
			return nil, fmt.Errorf("waiting for instance %s to be running: %w", instanceID, err)
		}
	}

	// Step 10: Handle project EBS volume.
	var volumeID string
	if pendingVolID != "" {
		// Attach the pending-attach volume from a previous mint recreate.
		if pendingVolAZ != az {
			return nil, fmt.Errorf(
				"pending-attach volume %s is in %s but instance launched in %s — "+
					"run 'mint destroy' and start fresh to resolve this AZ mismatch",
				pendingVolID, pendingVolAZ, az,
			)
		}
		_, attachErr := p.attachVolume.AttachVolume(ctx, &ec2.AttachVolumeInput{
			VolumeId:   aws.String(pendingVolID),
			InstanceId: aws.String(instanceID),
			Device:     aws.String("/dev/xvdf"),
		})
		if attachErr != nil {
			return nil, fmt.Errorf("attaching pending-attach volume %s to %s: %w", pendingVolID, instanceID, attachErr)
		}
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
		// Volume was created via BlockDeviceMappings at launch.
		volumeID = bdmVolumeID
		if volumeID == "" {
			// Fallback: BDM volume ID not yet populated in RunInstances response;
			// describe the running instance to get it.
			var getErr error
			volumeID, getErr = p.getBDMVolumeID(ctx, instanceID)
			if getErr != nil {
				return nil, fmt.Errorf("getting project volume ID for instance %s: %w", instanceID, getErr)
			}
		}
		if tagErr := p.tagVolume(ctx, volumeID, owner, ownerARN, vmName); tagErr != nil {
			return nil, fmt.Errorf("tagging project volume: %w", tagErr)
		}
	}

	// Step 11: Allocate and associate Elastic IP.
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

	// Step 12: Poll for bootstrap completion (if poller configured).
	if p.pollBootstrap != nil {
		if pollErr := p.pollBootstrap(ctx, owner, vmName, instanceID); pollErr != nil {
			result.BootstrapError = pollErr
		}
	}

	return result, nil
}

// handleExistingVM starts a stopped VM or returns info about a running VM.
// For running VMs, it reads the mint:bootstrap tag to surface the actual
// bootstrap status rather than implying success for all running VMs.
func (p *Provisioner) handleExistingVM(ctx context.Context, existing *vm.VM) (*ProvisionResult, error) {
	if existing.State == string(ec2types.InstanceStateNameStopped) {
		_, err := p.startInstances.StartInstances(ctx, &ec2.StartInstancesInput{
			InstanceIds: []string{existing.ID},
		})
		if err != nil {
			return nil, fmt.Errorf("starting stopped VM %s: %w", existing.ID, err)
		}
		result := &ProvisionResult{
			InstanceID:      existing.ID,
			PublicIP:        existing.PublicIP,
			Restarted:       true,
			BootstrapStatus: existing.BootstrapStatus,
		}
		if existing.BootstrapStatus == tags.BootstrapFailed {
			result.BootstrapError = fmt.Errorf(
				"VM %q has a previously failed bootstrap — run 'mint recreate' to recover",
				existing.Name,
			)
		}
		return result, nil
	}

	// VM exists and is running (or in another non-stopped state).
	// Reflect the actual mint:bootstrap tag so callers never infer success
	// from the absence of an error when bootstrap may still be pending.
	result := &ProvisionResult{
		InstanceID:      existing.ID,
		PublicIP:        existing.PublicIP,
		AlreadyRunning:  true,
		BootstrapStatus: existing.BootstrapStatus,
	}

	if existing.BootstrapStatus == tags.BootstrapFailed {
		result.BootstrapError = fmt.Errorf(
			"VM %q bootstrap failed. Run 'mint recreate' to rebuild.",
			existing.Name,
		)
	}

	return result, nil
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

// findBDMVolumeID returns the EBS volume ID for the given device name from
// a RunInstances (or DescribeInstances) block device mapping list.
func findBDMVolumeID(mappings []ec2types.InstanceBlockDeviceMapping, deviceName string) string {
	for _, bdm := range mappings {
		if aws.ToString(bdm.DeviceName) == deviceName && bdm.Ebs != nil {
			return aws.ToString(bdm.Ebs.VolumeId)
		}
	}
	return ""
}

// getBDMVolumeID calls DescribeInstances to retrieve the project EBS volume ID
// from the instance's block device mapping. Used as a fallback when RunInstances
// does not populate the volume ID in its response.
func (p *Provisioner) getBDMVolumeID(ctx context.Context, instanceID string) (string, error) {
	out, err := p.describeInstances.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		return "", fmt.Errorf("describe instance %s: %w", instanceID, err)
	}
	for _, reservation := range out.Reservations {
		for _, inst := range reservation.Instances {
			if id := findBDMVolumeID(inst.BlockDeviceMappings, "/dev/xvdf"); id != "" {
				return id, nil
			}
		}
	}
	return "", fmt.Errorf("block device /dev/xvdf not found on instance %s", instanceID)
}

// tagVolume applies Mint project-volume tags to an EBS volume via CreateTags.
func (p *Provisioner) tagVolume(ctx context.Context, volumeID, owner, ownerARN, vmName string) error {
	volumeTags := tags.NewTagBuilder(owner, ownerARN, vmName).
		WithComponent(tags.ComponentProjectVolume).
		Build()
	start := time.Now()
	_, err := p.createTags.CreateTags(ctx, &ec2.CreateTagsInput{
		Resources: []string{volumeID},
		Tags:      volumeTags,
	})
	if p.logger != nil {
		p.logger.Log("ec2", "CreateTags", time.Since(start), err)
	}
	if err != nil {
		return fmt.Errorf("tagging volume %s: %w", volumeID, err)
	}
	return nil
}

// launchInstance runs a new EC2 instance with the given configuration.
// When projectVolSize > 0, the project EBS volume is created via
// BlockDeviceMappings so the device is attached before user-data runs.
// Returns the instance ID and (if available in the response) the BDM volume ID.
func (p *Provisioner) launchInstance(
	ctx context.Context,
	amiID string,
	cfg ProvisionConfig,
	userSGID, adminSGID, subnetID string,
	owner, ownerARN, vmName string,
	projectVolSize int32,
	projectVolIOPS int32,
) (instanceID, bdmVolumeID string, err error) {
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
	// Use the configured volume size for display; fall back to 50 if not set.
	displayVolSize := cfg.VolumeSize
	if displayVolSize == 0 {
		displayVolSize = 50
	}
	instanceTags = append(instanceTags,
		ec2types.Tag{Key: aws.String(tags.TagRootVolumeGB), Value: aws.String("200")},
		ec2types.Tag{Key: aws.String(tags.TagProjectVolumeGB), Value: aws.String(strconv.Itoa(int(displayVolSize)))},
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

	// When provisioning fresh, create the project EBS via BlockDeviceMappings
	// so it is attached before user-data runs (no race condition).
	if projectVolSize > 0 {
		input.BlockDeviceMappings = []ec2types.BlockDeviceMapping{
			{
				DeviceName: aws.String("/dev/xvdf"),
				Ebs: &ec2types.EbsBlockDevice{
					VolumeSize:          aws.Int32(projectVolSize),
					VolumeType:          ec2types.VolumeTypeGp3,
					Iops:                aws.Int32(projectVolIOPS),
					DeleteOnTermination: aws.Bool(false),
				},
			},
		}
	}

	start := time.Now()
	out, launchErr := p.runInstances.RunInstances(ctx, input)
	if p.logger != nil {
		p.logger.Log("ec2", "RunInstances", time.Since(start), launchErr)
	}
	if launchErr != nil {
		return "", "", fmt.Errorf("run instances: %w", launchErr)
	}

	if len(out.Instances) == 0 {
		return "", "", fmt.Errorf("run instances returned no instances")
	}

	instanceID = aws.ToString(out.Instances[0].InstanceId)

	// Try to get the BDM volume ID from the RunInstances response.
	// AWS populates this when the volume is created synchronously at launch.
	if projectVolSize > 0 {
		bdmVolumeID = findBDMVolumeID(out.Instances[0].BlockDeviceMappings, "/dev/xvdf")
	}

	return instanceID, bdmVolumeID, nil
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

// allocateAndAssociateEIP allocates an Elastic IP and associates it with the instance.
func (p *Provisioner) allocateAndAssociateEIP(
	ctx context.Context,
	instanceID string,
	owner, ownerARN, vmName string,
) (allocID, publicIP string, err error) {
	eipTags := tags.NewTagBuilder(owner, ownerARN, vmName).
		WithComponent(tags.ComponentElasticIP).
		Build()

	aaStart := time.Now()
	allocOut, err := p.allocateAddr.AllocateAddress(ctx, &ec2.AllocateAddressInput{
		Domain: ec2types.DomainTypeVpc,
		TagSpecifications: []ec2types.TagSpecification{
			{
				ResourceType: ec2types.ResourceTypeElasticIp,
				Tags:         eipTags,
			},
		},
	})
	if p.logger != nil {
		p.logger.Log("ec2", "AllocateAddress", time.Since(aaStart), err)
	}
	if err != nil {
		return "", "", fmt.Errorf("allocate address: %w", err)
	}

	allocID = aws.ToString(allocOut.AllocationId)
	publicIP = aws.ToString(allocOut.PublicIp)

	assocStart := time.Now()
	_, err = p.associateAddr.AssociateAddress(ctx, &ec2.AssociateAddressInput{
		AllocationId: aws.String(allocID),
		InstanceId:   aws.String(instanceID),
	})
	if p.logger != nil {
		p.logger.Log("ec2", "AssociateAddress", time.Since(assocStart), err)
	}
	if err != nil {
		return "", "", fmt.Errorf("associate address %s to %s: %w", allocID, instanceID, err)
	}

	return allocID, publicIP, nil
}

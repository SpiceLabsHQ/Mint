// Package provision implements the core provisioning logic for Mint.
// This file contains the Initializer, which validates prerequisites
// (default VPC, admin EFS) and creates per-user resources (security group,
// EFS access point). All operations are idempotent — existing resources
// discovered by tags are skipped.
package provision

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/efs"
	efstypes "github.com/aws/aws-sdk-go-v2/service/efs/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	mintaws "github.com/nicholasgasior/mint/internal/aws"
	"github.com/nicholasgasior/mint/internal/tags"
)

// defaultInstanceProfileName is the IAM instance profile created by the admin
// CloudFormation stack. EC2 instances launched by mint up require this profile
// for Instance Connect, EFS mount, self-stop, and bootstrap tag updates.
const defaultInstanceProfileName = "mint-vm"

// InitResult holds the outcome of a successful init run.
type InitResult struct {
	VPCID          string
	EFSID          string
	SecurityGroup  string
	AccessPointID  string
	SGCreated      bool
	APCreated      bool
}

// Initializer validates prerequisites and creates per-user resources.
// All AWS dependencies are injected via narrow interfaces for testability.
type Initializer struct {
	vpcs            mintaws.DescribeVpcsAPI
	subnets         mintaws.DescribeSubnetsAPI
	fileSystems     mintaws.DescribeFileSystemsAPI
	instanceProfile mintaws.GetInstanceProfileAPI
	describeSGs     mintaws.DescribeSecurityGroupsAPI
	createSG        mintaws.CreateSecurityGroupAPI
	authorizeIn     mintaws.AuthorizeSecurityGroupIngressAPI
	createTags      mintaws.CreateTagsAPI
	describeAPs     mintaws.DescribeAccessPointsAPI
	createAP        mintaws.CreateAccessPointAPI
}

// NewInitializer creates an Initializer with all required AWS interfaces.
func NewInitializer(
	vpcs mintaws.DescribeVpcsAPI,
	subnets mintaws.DescribeSubnetsAPI,
	fileSystems mintaws.DescribeFileSystemsAPI,
	instanceProfile mintaws.GetInstanceProfileAPI,
	describeSGs mintaws.DescribeSecurityGroupsAPI,
	createSG mintaws.CreateSecurityGroupAPI,
	authorizeIn mintaws.AuthorizeSecurityGroupIngressAPI,
	createTags mintaws.CreateTagsAPI,
	describeAPs mintaws.DescribeAccessPointsAPI,
	createAP mintaws.CreateAccessPointAPI,
) *Initializer {
	return &Initializer{
		vpcs:            vpcs,
		subnets:         subnets,
		fileSystems:     fileSystems,
		instanceProfile: instanceProfile,
		describeSGs:     describeSGs,
		createSG:        createSG,
		authorizeIn:     authorizeIn,
		createTags:      createTags,
		describeAPs:     describeAPs,
		createAP:        createAP,
	}
}

// Run executes the full init flow: validate prerequisites, then create
// per-user resources idempotently.
func (i *Initializer) Run(ctx context.Context, owner, ownerARN, vmName string) (*InitResult, error) {
	// Step 1: Validate default VPC with public subnets.
	vpcID, err := i.validateVPC(ctx)
	if err != nil {
		return nil, fmt.Errorf("vpc validation: %w", err)
	}

	// Step 1.5: Validate admin-created IAM instance profile exists.
	if err := i.validateInstanceProfile(ctx); err != nil {
		return nil, fmt.Errorf("instance profile: %w", err)
	}

	// Step 2: Discover admin EFS filesystem.
	efsID, err := i.discoverEFS(ctx)
	if err != nil {
		return nil, fmt.Errorf("efs discovery: %w", err)
	}

	// Step 3: Ensure per-user security group exists.
	sgResult, err := i.ensureSecurityGroup(ctx, vpcID, owner, ownerARN, vmName)
	if err != nil {
		return nil, fmt.Errorf("security group: %w", err)
	}

	// Step 4: Ensure per-user EFS access point exists.
	apResult, err := i.ensureAccessPointResult(ctx, efsID, owner, ownerARN, vmName)
	if err != nil {
		return nil, fmt.Errorf("access point: %w", err)
	}

	return &InitResult{
		VPCID:         vpcID,
		EFSID:         efsID,
		SecurityGroup: sgResult.groupID,
		SGCreated:     sgResult.created,
		AccessPointID: apResult.accessPointID,
		APCreated:     apResult.created,
	}, nil
}

// ---------------------------------------------------------------------------
// VPC validation
// ---------------------------------------------------------------------------

// validateVPC checks that a default VPC exists with at least one public subnet.
func (i *Initializer) validateVPC(ctx context.Context) (string, error) {
	out, err := i.vpcs.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("is-default"), Values: []string{"true"}},
		},
	})
	if err != nil {
		return "", fmt.Errorf("describe VPCs: %w", err)
	}

	if len(out.Vpcs) == 0 {
		return "", fmt.Errorf("no default VPC found in this region; mint requires a default VPC (ADR-0010). " +
			"Create one with: aws ec2 create-default-vpc")
	}

	vpcID := aws.ToString(out.Vpcs[0].VpcId)

	// Verify at least one public subnet exists.
	subOut, err := i.subnets.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("vpc-id"), Values: []string{vpcID}},
		},
	})
	if err != nil {
		return "", fmt.Errorf("describe subnets for VPC %s: %w", vpcID, err)
	}

	hasPublic := false
	for _, subnet := range subOut.Subnets {
		if aws.ToBool(subnet.MapPublicIpOnLaunch) {
			hasPublic = true
			break
		}
	}
	if !hasPublic {
		return "", fmt.Errorf("no public subnets found in default VPC %s; "+
			"mint requires at least one subnet with auto-assign public IP enabled", vpcID)
	}

	return vpcID, nil
}

// ---------------------------------------------------------------------------
// Instance profile validation
// ---------------------------------------------------------------------------

// validateInstanceProfile checks that the admin-created IAM instance profile
// exists. Without this profile, EC2 instances cannot use Instance Connect,
// mount EFS, perform self-stop, or update bootstrap tags.
func (i *Initializer) validateInstanceProfile(ctx context.Context) error {
	_, err := i.instanceProfile.GetInstanceProfile(ctx, &iam.GetInstanceProfileInput{
		InstanceProfileName: aws.String(defaultInstanceProfileName),
	})
	if err != nil {
		var noSuchEntity *iamtypes.NoSuchEntityException
		if errors.As(err, &noSuchEntity) {
			return fmt.Errorf("instance profile %q not found; run the admin setup "+
				"CloudFormation stack first (see docs/admin-setup.md)", defaultInstanceProfileName)
		}
		return fmt.Errorf("get instance profile %q: %w", defaultInstanceProfileName, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// EFS discovery
// ---------------------------------------------------------------------------

// discoverEFS finds the admin-created EFS filesystem by tags (mint=true, mint:component=admin).
func (i *Initializer) discoverEFS(ctx context.Context) (string, error) {
	out, err := i.fileSystems.DescribeFileSystems(ctx, &efs.DescribeFileSystemsInput{})
	if err != nil {
		return "", fmt.Errorf("describe EFS file systems: %w", err)
	}

	// Filter by tags: mint=true and mint:component=admin.
	for _, fs := range out.FileSystems {
		tagMap := efsTagsToMap(fs.Tags)
		if tagMap[tags.TagMint] == "true" && tagMap[tags.TagComponent] == "admin" {
			return aws.ToString(fs.FileSystemId), nil
		}
	}

	return "", fmt.Errorf("no admin EFS filesystem found; run the admin setup CloudFormation stack first " +
		"(see docs/admin-setup.md)")
}

// ---------------------------------------------------------------------------
// Security group
// ---------------------------------------------------------------------------

type sgResult struct {
	groupID string
	created bool
}

// ensureSecurityGroup creates the per-user security group if it does not already
// exist. Discovery is by tag: mint=true, mint:owner=<owner>, mint:component=security-group.
func (i *Initializer) ensureSecurityGroup(ctx context.Context, vpcID, owner, ownerARN, vmName string) (*sgResult, error) {
	// Check for existing SG by tags.
	descOut, err := i.describeSGs.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("tag:" + tags.TagMint), Values: []string{"true"}},
			{Name: aws.String("tag:" + tags.TagOwner), Values: []string{owner}},
			{Name: aws.String("tag:" + tags.TagComponent), Values: []string{tags.ComponentSecurityGroup}},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("describe security groups: %w", err)
	}

	if len(descOut.SecurityGroups) > 0 {
		return &sgResult{
			groupID: aws.ToString(descOut.SecurityGroups[0].GroupId),
			created: false,
		}, nil
	}

	// Create new security group.
	sgName := fmt.Sprintf("mint-%s", owner)
	createOut, err := i.createSG.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:   aws.String(sgName),
		Description: aws.String(fmt.Sprintf("Mint security group for %s", owner)),
		VpcId:       aws.String(vpcID),
	})
	if err != nil {
		return nil, fmt.Errorf("create security group: %w", err)
	}

	sgID := aws.ToString(createOut.GroupId)

	// Add ingress rules: TCP 41122 and UDP 60000-61000 from 0.0.0.0/0 (ADR-0016).
	_, err = i.authorizeIn.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId: aws.String(sgID),
		IpPermissions: []ec2types.IpPermission{
			{
				IpProtocol: aws.String("tcp"),
				FromPort:   aws.Int32(41122),
				ToPort:     aws.Int32(41122),
				IpRanges: []ec2types.IpRange{
					{CidrIp: aws.String("0.0.0.0/0"), Description: aws.String("SSH on non-standard port")},
				},
			},
			{
				IpProtocol: aws.String("udp"),
				FromPort:   aws.Int32(60000),
				ToPort:     aws.Int32(61000),
				IpRanges: []ec2types.IpRange{
					{CidrIp: aws.String("0.0.0.0/0"), Description: aws.String("Mosh UDP range")},
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("authorize ingress on %s: %w", sgID, err)
	}

	// Tag the security group with full Mint tag schema.
	ec2Tags := tags.NewTagBuilder(owner, ownerARN, vmName).
		WithComponent(tags.ComponentSecurityGroup).
		Build()

	_, err = i.createTags.CreateTags(ctx, &ec2.CreateTagsInput{
		Resources: []string{sgID},
		Tags:      ec2Tags,
	})
	if err != nil {
		return nil, fmt.Errorf("tag security group %s: %w", sgID, err)
	}

	return &sgResult{groupID: sgID, created: true}, nil
}

// ---------------------------------------------------------------------------
// EFS access point
// ---------------------------------------------------------------------------

type apResult struct {
	accessPointID string
	created       bool
}

func (i *Initializer) ensureAccessPointResult(ctx context.Context, fsID, owner, ownerARN, vmName string) (*apResult, error) {
	// Check for existing access point by listing all APs for this filesystem
	// and filtering by tags.
	descOut, err := i.describeAPs.DescribeAccessPoints(ctx, &efs.DescribeAccessPointsInput{
		FileSystemId: aws.String(fsID),
	})
	if err != nil {
		return nil, fmt.Errorf("describe access points for %s: %w", fsID, err)
	}

	for _, ap := range descOut.AccessPoints {
		tagMap := efsTagsToMap(ap.Tags)
		if tagMap[tags.TagMint] == "true" &&
			tagMap[tags.TagOwner] == owner &&
			tagMap[tags.TagComponent] == tags.ComponentEFSAccessPoint {
			return &apResult{
				accessPointID: aws.ToString(ap.AccessPointId),
				created:       false,
			}, nil
		}
	}

	// Create new access point with Mint tags.
	efsTags := toEFSTags(
		tags.NewTagBuilder(owner, ownerARN, vmName).
			WithComponent(tags.ComponentEFSAccessPoint).
			Build(),
	)

	createOut, err := i.createAP.CreateAccessPoint(ctx, &efs.CreateAccessPointInput{
		FileSystemId: aws.String(fsID),
		Tags:         efsTags,
		PosixUser: &efstypes.PosixUser{
			Uid: aws.Int64(1000),
			Gid: aws.Int64(1000),
		},
		RootDirectory: &efstypes.RootDirectory{
			Path: aws.String(fmt.Sprintf("/mint/user/%s", owner)),
			CreationInfo: &efstypes.CreationInfo{
				OwnerUid:    aws.Int64(1000),
				OwnerGid:    aws.Int64(1000),
				Permissions: aws.String("755"),
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create access point on %s: %w", fsID, err)
	}

	return &apResult{
		accessPointID: aws.ToString(createOut.AccessPointId),
		created:       true,
	}, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// efsTagsToMap converts EFS tags to a map for easy lookup.
func efsTagsToMap(efsTags []efstypes.Tag) map[string]string {
	m := make(map[string]string, len(efsTags))
	for _, tag := range efsTags {
		m[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}
	return m
}

// toEFSTags converts EC2 tags to EFS tags.
func toEFSTags(ec2Tags []ec2types.Tag) []efstypes.Tag {
	out := make([]efstypes.Tag, len(ec2Tags))
	for i, t := range ec2Tags {
		out[i] = efstypes.Tag{
			Key:   t.Key,
			Value: t.Value,
		}
	}
	return out
}

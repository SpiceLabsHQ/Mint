// Package aws provides thin wrappers around AWS SDK clients used by Mint.
// This file defines narrow interfaces for EC2 provisioning and management
// operations needed by Phase 1. Each interface wraps exactly one AWS SDK
// method, enabling mock injection in tests.
package aws

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

// ---------------------------------------------------------------------------
// Instance state waiters
// ---------------------------------------------------------------------------

// WaitInstanceRunningAPI defines the interface for waiting until an EC2
// instance reaches the running state. Wraps ec2.InstanceRunningWaiter.Wait.
type WaitInstanceRunningAPI interface {
	Wait(ctx context.Context, params *ec2.DescribeInstancesInput, maxWaitDur time.Duration, optFns ...func(*ec2.InstanceRunningWaiterOptions)) error
}

// Compile-time check: ec2.InstanceRunningWaiter satisfies the interface.
var _ WaitInstanceRunningAPI = (*ec2.InstanceRunningWaiter)(nil)

// WaitVolumeAvailableAPI defines the interface for waiting until an EBS
// volume reaches the available state. Wraps ec2.VolumeAvailableWaiter.Wait.
type WaitVolumeAvailableAPI interface {
	Wait(ctx context.Context, params *ec2.DescribeVolumesInput, maxWaitDur time.Duration, optFns ...func(*ec2.VolumeAvailableWaiterOptions)) error
}

// Compile-time check: ec2.VolumeAvailableWaiter satisfies the interface.
var _ WaitVolumeAvailableAPI = (*ec2.VolumeAvailableWaiter)(nil)

// WaitInstanceTerminatedAPI defines the interface for waiting until an EC2
// instance reaches the terminated state. Wraps ec2.InstanceTerminatedWaiter.Wait.
type WaitInstanceTerminatedAPI interface {
	Wait(ctx context.Context, params *ec2.DescribeInstancesInput, maxWaitDur time.Duration, optFns ...func(*ec2.InstanceTerminatedWaiterOptions)) error
}

// Compile-time check: ec2.InstanceTerminatedWaiter satisfies the interface.
var _ WaitInstanceTerminatedAPI = (*ec2.InstanceTerminatedWaiter)(nil)

// WaitInstanceStoppedAPI defines the interface for waiting until an EC2
// instance reaches the stopped state. Wraps ec2.InstanceStoppedWaiter.Wait.
type WaitInstanceStoppedAPI interface {
	Wait(ctx context.Context, params *ec2.DescribeInstancesInput, maxWaitDur time.Duration, optFns ...func(*ec2.InstanceStoppedWaiterOptions)) error
}

// Compile-time check: ec2.InstanceStoppedWaiter satisfies the interface.
var _ WaitInstanceStoppedAPI = (*ec2.InstanceStoppedWaiter)(nil)

// ---------------------------------------------------------------------------
// AMI resolution
// ---------------------------------------------------------------------------

// DescribeImagesAPI defines the subset of the EC2 API used for AMI resolution.
type DescribeImagesAPI interface {
	DescribeImages(ctx context.Context, params *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error)
}

// ---------------------------------------------------------------------------
// Instance lifecycle
// ---------------------------------------------------------------------------

// RunInstancesAPI defines the subset of the EC2 API used for launching instances.
type RunInstancesAPI interface {
	RunInstances(ctx context.Context, params *ec2.RunInstancesInput, optFns ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error)
}

// StartInstancesAPI defines the subset of the EC2 API used for starting stopped instances.
type StartInstancesAPI interface {
	StartInstances(ctx context.Context, params *ec2.StartInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error)
}

// StopInstancesAPI defines the subset of the EC2 API used for stopping running instances.
type StopInstancesAPI interface {
	StopInstances(ctx context.Context, params *ec2.StopInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StopInstancesOutput, error)
}

// TerminateInstancesAPI defines the subset of the EC2 API used for terminating instances.
type TerminateInstancesAPI interface {
	TerminateInstances(ctx context.Context, params *ec2.TerminateInstancesInput, optFns ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error)
}

// DescribeInstancesAPI defines the subset of the EC2 API used for describing instances.
type DescribeInstancesAPI interface {
	DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
}

// ModifyInstanceAttributeAPI defines the subset of the EC2 API used for modifying
// instance attributes (e.g., instance type on a stopped instance).
type ModifyInstanceAttributeAPI interface {
	ModifyInstanceAttribute(ctx context.Context, params *ec2.ModifyInstanceAttributeInput, optFns ...func(*ec2.Options)) (*ec2.ModifyInstanceAttributeOutput, error)
}

// ---------------------------------------------------------------------------
// EBS volume management
// ---------------------------------------------------------------------------

// CreateVolumeAPI defines the subset of the EC2 API used for creating EBS volumes.
type CreateVolumeAPI interface {
	CreateVolume(ctx context.Context, params *ec2.CreateVolumeInput, optFns ...func(*ec2.Options)) (*ec2.CreateVolumeOutput, error)
}

// AttachVolumeAPI defines the subset of the EC2 API used for attaching EBS volumes.
type AttachVolumeAPI interface {
	AttachVolume(ctx context.Context, params *ec2.AttachVolumeInput, optFns ...func(*ec2.Options)) (*ec2.AttachVolumeOutput, error)
}

// DetachVolumeAPI defines the subset of the EC2 API used for detaching EBS volumes.
type DetachVolumeAPI interface {
	DetachVolume(ctx context.Context, params *ec2.DetachVolumeInput, optFns ...func(*ec2.Options)) (*ec2.DetachVolumeOutput, error)
}

// DeleteVolumeAPI defines the subset of the EC2 API used for deleting EBS volumes.
type DeleteVolumeAPI interface {
	DeleteVolume(ctx context.Context, params *ec2.DeleteVolumeInput, optFns ...func(*ec2.Options)) (*ec2.DeleteVolumeOutput, error)
}

// DescribeVolumesAPI defines the subset of the EC2 API used for describing EBS volumes.
type DescribeVolumesAPI interface {
	DescribeVolumes(ctx context.Context, params *ec2.DescribeVolumesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error)
}

// ---------------------------------------------------------------------------
// Elastic IP management
// ---------------------------------------------------------------------------

// AllocateAddressAPI defines the subset of the EC2 API used for allocating Elastic IPs.
type AllocateAddressAPI interface {
	AllocateAddress(ctx context.Context, params *ec2.AllocateAddressInput, optFns ...func(*ec2.Options)) (*ec2.AllocateAddressOutput, error)
}

// AssociateAddressAPI defines the subset of the EC2 API used for associating Elastic IPs.
type AssociateAddressAPI interface {
	AssociateAddress(ctx context.Context, params *ec2.AssociateAddressInput, optFns ...func(*ec2.Options)) (*ec2.AssociateAddressOutput, error)
}

// ReleaseAddressAPI defines the subset of the EC2 API used for releasing Elastic IPs.
type ReleaseAddressAPI interface {
	ReleaseAddress(ctx context.Context, params *ec2.ReleaseAddressInput, optFns ...func(*ec2.Options)) (*ec2.ReleaseAddressOutput, error)
}

// DescribeAddressesAPI defines the subset of the EC2 API used for describing Elastic IPs.
type DescribeAddressesAPI interface {
	DescribeAddresses(ctx context.Context, params *ec2.DescribeAddressesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeAddressesOutput, error)
}

// ---------------------------------------------------------------------------
// Security group management
// ---------------------------------------------------------------------------

// CreateSecurityGroupAPI defines the subset of the EC2 API used for creating security groups.
type CreateSecurityGroupAPI interface {
	CreateSecurityGroup(ctx context.Context, params *ec2.CreateSecurityGroupInput, optFns ...func(*ec2.Options)) (*ec2.CreateSecurityGroupOutput, error)
}

// AuthorizeSecurityGroupIngressAPI defines the subset of the EC2 API used for
// adding inbound rules to security groups.
type AuthorizeSecurityGroupIngressAPI interface {
	AuthorizeSecurityGroupIngress(ctx context.Context, params *ec2.AuthorizeSecurityGroupIngressInput, optFns ...func(*ec2.Options)) (*ec2.AuthorizeSecurityGroupIngressOutput, error)
}

// DescribeSecurityGroupsAPI defines the subset of the EC2 API used for describing security groups.
type DescribeSecurityGroupsAPI interface {
	DescribeSecurityGroups(ctx context.Context, params *ec2.DescribeSecurityGroupsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error)
}

// ---------------------------------------------------------------------------
// Tags and networking
// ---------------------------------------------------------------------------

// CreateTagsAPI defines the subset of the EC2 API used for tagging resources.
type CreateTagsAPI interface {
	CreateTags(ctx context.Context, params *ec2.CreateTagsInput, optFns ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error)
}

// DescribeSubnetsAPI defines the subset of the EC2 API used for describing subnets.
type DescribeSubnetsAPI interface {
	DescribeSubnets(ctx context.Context, params *ec2.DescribeSubnetsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error)
}

// DescribeVpcsAPI defines the subset of the EC2 API used for describing VPCs.
type DescribeVpcsAPI interface {
	DescribeVpcs(ctx context.Context, params *ec2.DescribeVpcsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error)
}

// ---------------------------------------------------------------------------
// Compile-time interface satisfaction checks
// ---------------------------------------------------------------------------

var (
	_ RunInstancesAPI                  = (*ec2.Client)(nil)
	_ StartInstancesAPI                = (*ec2.Client)(nil)
	_ StopInstancesAPI                 = (*ec2.Client)(nil)
	_ TerminateInstancesAPI            = (*ec2.Client)(nil)
	_ DescribeInstancesAPI             = (*ec2.Client)(nil)
	_ ModifyInstanceAttributeAPI       = (*ec2.Client)(nil)
	_ CreateVolumeAPI                  = (*ec2.Client)(nil)
	_ AttachVolumeAPI                  = (*ec2.Client)(nil)
	_ DetachVolumeAPI                  = (*ec2.Client)(nil)
	_ DeleteVolumeAPI                  = (*ec2.Client)(nil)
	_ DescribeVolumesAPI               = (*ec2.Client)(nil)
	_ AllocateAddressAPI               = (*ec2.Client)(nil)
	_ AssociateAddressAPI              = (*ec2.Client)(nil)
	_ ReleaseAddressAPI                = (*ec2.Client)(nil)
	_ DescribeAddressesAPI             = (*ec2.Client)(nil)
	_ CreateSecurityGroupAPI           = (*ec2.Client)(nil)
	_ AuthorizeSecurityGroupIngressAPI = (*ec2.Client)(nil)
	_ DescribeSecurityGroupsAPI        = (*ec2.Client)(nil)
	_ CreateTagsAPI                    = (*ec2.Client)(nil)
	_ DescribeSubnetsAPI               = (*ec2.Client)(nil)
	_ DescribeVpcsAPI                  = (*ec2.Client)(nil)
)

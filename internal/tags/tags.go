// Package tags provides tag constants, a fluent tag builder, and filter
// constructors for Mint's tag-based resource discovery (ADR-0001, ADR-0014).
//
// All AWS resources managed by Mint are identified via a standard set of tags.
// This package centralises the tag schema so that provisioning, discovery, and
// lifecycle commands share a single source of truth.
package tags

import (
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// ---------------------------------------------------------------------------
// Tag key constants (ADR-0001)
// ---------------------------------------------------------------------------

const (
	// TagMint marks a resource as managed by Mint. Value is always "true".
	TagMint = "mint"

	// TagComponent identifies the resource type within a Mint environment.
	TagComponent = "mint:component"

	// TagVM is the user-facing VM name (defaults to "default").
	TagVM = "mint:vm"

	// TagOwner is the friendly owner name derived from STS at runtime.
	TagOwner = "mint:owner"

	// TagOwnerARN is the full IAM ARN of the owner.
	TagOwnerARN = "mint:owner-arn"

	// TagBootstrap tracks bootstrap script execution status.
	TagBootstrap = "mint:bootstrap"

	// TagHealth tracks the health status of the resource.
	TagHealth = "mint:health"

	// TagName is the standard AWS Name tag. Format: mint/<owner>/<vm-name>.
	TagName = "Name"

	// TagRootVolumeGB stores the root EBS volume size in GB (ADR-0004).
	TagRootVolumeGB = "mint:root-volume-gb"

	// TagProjectVolumeGB stores the project EBS volume size in GB (ADR-0004).
	TagProjectVolumeGB = "mint:project-volume-gb"
)

// ---------------------------------------------------------------------------
// Component value constants (ADR-0001)
// ---------------------------------------------------------------------------

const (
	ComponentInstance       = "instance"
	ComponentVolume         = "volume"
	ComponentSecurityGroup  = "security-group"
	ComponentElasticIP      = "elastic-ip"
	ComponentProjectVolume  = "project-volume"
	ComponentEFSAccessPoint = "efs-access-point"
)

// ---------------------------------------------------------------------------
// Bootstrap status constants
// ---------------------------------------------------------------------------

const (
	BootstrapPending  = "pending"
	BootstrapComplete = "complete"
	BootstrapFailed   = "failed"
)

// ---------------------------------------------------------------------------
// TagBuilder — fluent builder for EC2 tag sets
// ---------------------------------------------------------------------------

// TagBuilder constructs a set of EC2 tags for a Mint resource.
// Base tags (mint, owner, owner-arn, vm, Name) are always included.
// Optional tags (component, bootstrap) are added via fluent methods.
type TagBuilder struct {
	owner    string
	ownerARN string
	vmName   string

	component string
	bootstrap string
}

// NewTagBuilder creates a TagBuilder with the required base fields.
func NewTagBuilder(owner, ownerARN, vmName string) *TagBuilder {
	return &TagBuilder{
		owner:    owner,
		ownerARN: ownerARN,
		vmName:   vmName,
	}
}

// WithComponent sets the mint:component tag value.
func (b *TagBuilder) WithComponent(component string) *TagBuilder {
	b.component = component
	return b
}

// WithBootstrap sets the mint:bootstrap tag value.
func (b *TagBuilder) WithBootstrap(status string) *TagBuilder {
	b.bootstrap = status
	return b
}

// Build produces the full set of EC2 tags.
func (b *TagBuilder) Build() []ec2types.Tag {
	tags := []ec2types.Tag{
		{Key: aws.String(TagMint), Value: aws.String("true")},
		{Key: aws.String(TagOwner), Value: aws.String(b.owner)},
		{Key: aws.String(TagOwnerARN), Value: aws.String(b.ownerARN)},
		{Key: aws.String(TagVM), Value: aws.String(b.vmName)},
		{Key: aws.String(TagName), Value: aws.String(fmt.Sprintf("mint/%s/%s", b.owner, b.vmName))},
	}

	if b.component != "" {
		tags = append(tags, ec2types.Tag{
			Key: aws.String(TagComponent), Value: aws.String(b.component),
		})
	}

	if b.bootstrap != "" {
		tags = append(tags, ec2types.Tag{
			Key: aws.String(TagBootstrap), Value: aws.String(b.bootstrap),
		})
	}

	return tags
}

// ---------------------------------------------------------------------------
// Filter constructors for tag-based discovery
// ---------------------------------------------------------------------------

// FilterByOwner returns EC2 filters that match all Mint resources belonging
// to the given owner.
func FilterByOwner(owner string) []ec2types.Filter {
	return []ec2types.Filter{
		{Name: aws.String("tag:" + TagMint), Values: []string{"true"}},
		{Name: aws.String("tag:" + TagOwner), Values: []string{owner}},
	}
}

// FilterByOwnerAndVM returns EC2 filters that match Mint resources belonging
// to the given owner and VM name.
func FilterByOwnerAndVM(owner, vmName string) []ec2types.Filter {
	return []ec2types.Filter{
		{Name: aws.String("tag:" + TagMint), Values: []string{"true"}},
		{Name: aws.String("tag:" + TagOwner), Values: []string{owner}},
		{Name: aws.String("tag:" + TagVM), Values: []string{vmName}},
	}
}

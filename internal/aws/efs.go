// Package aws provides thin wrappers around AWS SDK clients used by Mint.
// This file defines narrow interfaces for EFS operations needed by Phase 1.
// Each interface wraps exactly one AWS SDK method, enabling mock injection
// in tests.
package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/efs"
)

// ---------------------------------------------------------------------------
// EFS file system and access point interfaces
// ---------------------------------------------------------------------------

// DescribeFileSystemsAPI defines the subset of the EFS API used for listing
// and describing file systems.
type DescribeFileSystemsAPI interface {
	DescribeFileSystems(ctx context.Context, params *efs.DescribeFileSystemsInput, optFns ...func(*efs.Options)) (*efs.DescribeFileSystemsOutput, error)
}

// CreateAccessPointAPI defines the subset of the EFS API used for creating
// access points on a file system.
type CreateAccessPointAPI interface {
	CreateAccessPoint(ctx context.Context, params *efs.CreateAccessPointInput, optFns ...func(*efs.Options)) (*efs.CreateAccessPointOutput, error)
}

// DescribeAccessPointsAPI defines the subset of the EFS API used for listing
// and describing access points.
type DescribeAccessPointsAPI interface {
	DescribeAccessPoints(ctx context.Context, params *efs.DescribeAccessPointsInput, optFns ...func(*efs.Options)) (*efs.DescribeAccessPointsOutput, error)
}

// ---------------------------------------------------------------------------
// Compile-time interface satisfaction checks
// ---------------------------------------------------------------------------

var (
	_ DescribeFileSystemsAPI = (*efs.Client)(nil)
	_ CreateAccessPointAPI   = (*efs.Client)(nil)
	_ DescribeAccessPointsAPI = (*efs.Client)(nil)
)

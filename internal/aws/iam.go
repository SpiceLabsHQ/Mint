// Package aws provides thin wrappers around AWS SDK clients used by Mint.
// This file defines narrow interfaces for IAM operations needed by init
// to validate the admin-created instance profile. Each interface wraps
// exactly one AWS SDK method, enabling mock injection in tests.
package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/iam"
)

// ---------------------------------------------------------------------------
// IAM instance profile interface
// ---------------------------------------------------------------------------

// GetInstanceProfileAPI defines the subset of the IAM API used for validating
// that an instance profile exists. Used by mint init to check that the admin
// CloudFormation stack has been deployed.
type GetInstanceProfileAPI interface {
	GetInstanceProfile(ctx context.Context, params *iam.GetInstanceProfileInput, optFns ...func(*iam.Options)) (*iam.GetInstanceProfileOutput, error)
}

// ---------------------------------------------------------------------------
// Compile-time interface satisfaction checks
// ---------------------------------------------------------------------------

var _ GetInstanceProfileAPI = (*iam.Client)(nil)

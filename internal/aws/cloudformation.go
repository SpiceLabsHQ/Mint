// Package aws provides thin wrappers around AWS SDK clients used by Mint.
// This file defines narrow interfaces for CloudFormation operations needed by
// the admin deploy workflow. Each interface wraps exactly one AWS SDK method,
// enabling mock injection in tests.
package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
)

// ---------------------------------------------------------------------------
// CloudFormation stack management interfaces
// ---------------------------------------------------------------------------

// CreateStackAPI defines the subset of the CloudFormation API used for creating
// new stacks. Used by the admin deployer to create the Mint IAM stack on first run.
type CreateStackAPI interface {
	CreateStack(ctx context.Context, params *cloudformation.CreateStackInput, optFns ...func(*cloudformation.Options)) (*cloudformation.CreateStackOutput, error)
}

// UpdateStackAPI defines the subset of the CloudFormation API used for updating
// existing stacks. Used by the admin deployer to apply template changes to an
// already-deployed Mint IAM stack.
type UpdateStackAPI interface {
	UpdateStack(ctx context.Context, params *cloudformation.UpdateStackInput, optFns ...func(*cloudformation.Options)) (*cloudformation.UpdateStackOutput, error)
}

// DescribeStacksAPI defines the subset of the CloudFormation API used for
// querying stack status and outputs. Used by the admin deployer to detect
// whether a stack already exists and to poll for completion.
type DescribeStacksAPI interface {
	DescribeStacks(ctx context.Context, params *cloudformation.DescribeStacksInput, optFns ...func(*cloudformation.Options)) (*cloudformation.DescribeStacksOutput, error)
}

// DescribeStackEventsAPI defines the subset of the CloudFormation API used for
// streaming stack events during a create or update operation. Used by the admin
// deployer to surface real-time progress to the operator.
type DescribeStackEventsAPI interface {
	DescribeStackEvents(ctx context.Context, params *cloudformation.DescribeStackEventsInput, optFns ...func(*cloudformation.Options)) (*cloudformation.DescribeStackEventsOutput, error)
}

// DeleteStackAPI defines the subset of the CloudFormation API used for deleting
// a stack. Used by the admin deployer to remove a stuck ROLLBACK_COMPLETE stack
// before retrying the create operation.
type DeleteStackAPI interface {
	DeleteStack(ctx context.Context, params *cloudformation.DeleteStackInput, optFns ...func(*cloudformation.Options)) (*cloudformation.DeleteStackOutput, error)
}

// ---------------------------------------------------------------------------
// Compile-time interface satisfaction checks
// ---------------------------------------------------------------------------

var (
	_ CreateStackAPI         = (*cloudformation.Client)(nil)
	_ UpdateStackAPI         = (*cloudformation.Client)(nil)
	_ DescribeStacksAPI      = (*cloudformation.Client)(nil)
	_ DescribeStackEventsAPI = (*cloudformation.Client)(nil)
	_ DeleteStackAPI         = (*cloudformation.Client)(nil)
)

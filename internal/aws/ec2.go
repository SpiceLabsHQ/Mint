// Package aws provides thin wrappers around AWS SDK clients used by Mint.
// Each wrapper exposes a narrow interface for mock injection in tests,
// following the same pattern as the identity package.
package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// DescribeInstanceTypesAPI defines the subset of the EC2 API used for instance
// type validation. This interface enables mock injection for testing.
type DescribeInstanceTypesAPI interface {
	DescribeInstanceTypes(ctx context.Context, params *ec2.DescribeInstanceTypesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstanceTypesOutput, error)
}

// InstanceTypeValidator validates that an EC2 instance type exists in a region
// by calling the DescribeInstanceTypes API.
type InstanceTypeValidator struct {
	client DescribeInstanceTypesAPI
}

// NewInstanceTypeValidator creates a validator with the given EC2 client.
func NewInstanceTypeValidator(client DescribeInstanceTypesAPI) *InstanceTypeValidator {
	return &InstanceTypeValidator{client: client}
}

// Validate checks that instanceType is a valid EC2 instance type in the given region.
// Returns nil if the instance type exists, or an error with context including
// the instance type and region.
func (v *InstanceTypeValidator) Validate(ctx context.Context, instanceType, region string) error {
	input := &ec2.DescribeInstanceTypesInput{
		InstanceTypes: []types.InstanceType{types.InstanceType(instanceType)},
	}

	out, err := v.client.DescribeInstanceTypes(ctx, input)
	if err != nil {
		return fmt.Errorf("ec2 describe-instance-types: %w", err)
	}

	if len(out.InstanceTypes) == 0 {
		return fmt.Errorf("instance type %q is not available in %s", instanceType, region)
	}

	return nil
}

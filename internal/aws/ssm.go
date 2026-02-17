// Package aws provides thin wrappers around AWS SDK clients used by Mint.
// This file defines the narrow interface for SSM parameter resolution,
// used primarily for AMI lookup via public SSM parameters.
package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

// ubuntuAMIParameter is the SSM public parameter path for the current
// Ubuntu 24.04 LTS amd64 HVM EBS-GP2 AMI.
const ubuntuAMIParameter = "/aws/service/canonical/ubuntu/server/24.04/stable/current/amd64/hvm/ebs-gp2/ami-id"

// GetParameterAPI defines the subset of the SSM API used for resolving
// SSM parameters. This interface enables mock injection for testing.
type GetParameterAPI interface {
	GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// Compile-time interface satisfaction check.
var _ GetParameterAPI = (*ssm.Client)(nil)

// ResolveAMI queries the SSM public parameter for the current Ubuntu 24.04
// LTS AMI ID. Returns the AMI ID string or an error if the lookup fails.
func ResolveAMI(ctx context.Context, client GetParameterAPI) (string, error) {
	name := ubuntuAMIParameter
	input := &ssm.GetParameterInput{
		Name: &name,
	}

	out, err := client.GetParameter(ctx, input)
	if err != nil {
		return "", fmt.Errorf("ssm get-parameter: %w", err)
	}

	if out.Parameter == nil {
		return "", fmt.Errorf("ssm get-parameter: nil parameter in response for %s", ubuntuAMIParameter)
	}

	if out.Parameter.Value == nil {
		return "", fmt.Errorf("ssm get-parameter: nil value for %s", ubuntuAMIParameter)
	}

	return *out.Parameter.Value, nil
}

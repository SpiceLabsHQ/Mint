// Package aws provides thin wrappers around AWS SDK clients used by Mint.
// This file defines the narrow interface for EC2 Instance Connect,
// used to push ephemeral SSH public keys to instances (ADR-0007).
package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/ec2instanceconnect"
)

// SendSSHPublicKeyAPI defines the subset of the EC2 Instance Connect API
// used for pushing ephemeral SSH public keys. This interface enables mock
// injection for testing.
type SendSSHPublicKeyAPI interface {
	SendSSHPublicKey(ctx context.Context, params *ec2instanceconnect.SendSSHPublicKeyInput, optFns ...func(*ec2instanceconnect.Options)) (*ec2instanceconnect.SendSSHPublicKeyOutput, error)
}

// Compile-time interface satisfaction check.
var _ SendSSHPublicKeyAPI = (*ec2instanceconnect.Client)(nil)

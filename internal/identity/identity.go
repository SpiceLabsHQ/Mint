package identity

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// Owner holds the derived owner identity used for tagging AWS resources.
// Name is the normalized friendly name (mint:owner tag value).
// ARN is the full caller ARN (mint:owner-arn tag value).
type Owner struct {
	Name string
	ARN  string
}

// STSClient defines the subset of the STS API used for identity resolution.
// This interface enables mock injection for testing.
type STSClient interface {
	GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

// Resolver resolves the current AWS caller identity to an Owner.
type Resolver struct {
	client STSClient
}

// NewResolver creates a Resolver with the given STS client.
func NewResolver(client STSClient) *Resolver {
	return &Resolver{client: client}
}

// Resolve calls STS GetCallerIdentity and normalizes the ARN to an Owner.
// This is called on every mint command invocation per ADR-0013.
func (r *Resolver) Resolve(ctx context.Context) (*Owner, error) {
	out, err := r.client.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, fmt.Errorf("sts get-caller-identity: %w", err)
	}

	if out.Arn == nil {
		return nil, fmt.Errorf("sts get-caller-identity returned nil ARN")
	}

	name, err := NormalizeARN(*out.Arn)
	if err != nil {
		return nil, fmt.Errorf("normalize ARN: %w", err)
	}

	return &Owner{
		Name: name,
		ARN:  *out.Arn,
	}, nil
}

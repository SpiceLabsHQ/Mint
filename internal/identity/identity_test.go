package identity

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// mockSTSClient implements STSClient for testing.
type mockSTSClient struct {
	output *sts.GetCallerIdentityOutput
	err    error
}

func (m *mockSTSClient) GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	return m.output, m.err
}

func TestResolveOwner(t *testing.T) {
	iamARN := "arn:aws:iam::123456789012:user/ryan"
	ssoARN := "arn:aws:sts::123456789012:assumed-role/AWSReservedSSO_PowerUserAccess_abc123/ryan@example.com"

	tests := []struct {
		name      string
		client    STSClient
		wantOwner string
		wantARN   string
		wantErr   bool
	}{
		{
			name: "IAM user identity",
			client: &mockSTSClient{
				output: &sts.GetCallerIdentityOutput{
					Arn: &iamARN,
				},
			},
			wantOwner: "ryan",
			wantARN:   iamARN,
		},
		{
			name: "SSO identity",
			client: &mockSTSClient{
				output: &sts.GetCallerIdentityOutput{
					Arn: &ssoARN,
				},
			},
			wantOwner: "ryan",
			wantARN:   ssoARN,
		},
		{
			name: "STS API error",
			client: &mockSTSClient{
				err: errors.New("no credentials"),
			},
			wantErr: true,
		},
		{
			name: "nil ARN in response",
			client: &mockSTSClient{
				output: &sts.GetCallerIdentityOutput{
					Arn: nil,
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := NewResolver(tt.client)
			owner, err := resolver.Resolve(context.Background())

			if tt.wantErr {
				if err == nil {
					t.Fatalf("Resolve() expected error, got owner=%+v", owner)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve() unexpected error: %v", err)
			}
			if owner.Name != tt.wantOwner {
				t.Errorf("owner.Name = %q, want %q", owner.Name, tt.wantOwner)
			}
			if owner.ARN != tt.wantARN {
				t.Errorf("owner.ARN = %q, want %q", owner.ARN, tt.wantARN)
			}
		})
	}
}

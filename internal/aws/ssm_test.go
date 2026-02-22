package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// ---------------------------------------------------------------------------
// Inline mock
// ---------------------------------------------------------------------------

type mockDescribeImages struct {
	output *ec2.DescribeImagesOutput
	err    error
}

func (m *mockDescribeImages) DescribeImages(ctx context.Context, params *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
	return m.output, m.err
}

var _ DescribeImagesAPI = (*mockDescribeImages)(nil)

// ---------------------------------------------------------------------------
// ResolveAMI tests
// ---------------------------------------------------------------------------

func TestResolveAMI(t *testing.T) {
	tests := []struct {
		name       string
		client     DescribeImagesAPI
		wantAMI    string
		wantErr    bool
		errContain string
	}{
		{
			name: "returns most recent AMI",
			client: &mockDescribeImages{
				output: &ec2.DescribeImagesOutput{
					Images: []ec2types.Image{
						{ImageId: aws.String("ami-older"), CreationDate: aws.String("2026-01-01T00:00:00.000Z")},
						{ImageId: aws.String("ami-newest"), CreationDate: aws.String("2026-02-18T10:51:48.000Z")},
					},
				},
			},
			wantAMI: "ami-newest",
		},
		{
			name: "API error propagated",
			client: &mockDescribeImages{
				err: errors.New("describe images: access denied"),
			},
			wantErr:    true,
			errContain: "access denied",
		},
		{
			name: "no AMIs found",
			client: &mockDescribeImages{
				output: &ec2.DescribeImagesOutput{Images: []ec2types.Image{}},
			},
			wantErr:    true,
			errContain: "no Ubuntu 24.04 LTS AMIs found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ami, err := ResolveAMI(context.Background(), tt.client)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errContain != "" && !containsSubstring(err.Error(), tt.errContain) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errContain)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ami != tt.wantAMI {
				t.Errorf("got AMI %q, want %q", ami, tt.wantAMI)
			}
		})
	}
}

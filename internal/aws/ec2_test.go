package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// mockDescribeInstanceTypes implements DescribeInstanceTypesAPI for testing.
type mockDescribeInstanceTypes struct {
	output *ec2.DescribeInstanceTypesOutput
	err    error
}

func (m *mockDescribeInstanceTypes) DescribeInstanceTypes(ctx context.Context, params *ec2.DescribeInstanceTypesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstanceTypesOutput, error) {
	return m.output, m.err
}

func TestValidateInstanceType(t *testing.T) {
	tests := []struct {
		name       string
		client     DescribeInstanceTypesAPI
		instType   string
		region     string
		wantErr    bool
		errContain string
	}{
		{
			name: "valid instance type",
			client: &mockDescribeInstanceTypes{
				output: &ec2.DescribeInstanceTypesOutput{
					InstanceTypes: []types.InstanceTypeInfo{
						{InstanceType: types.InstanceTypeM6iXlarge},
					},
				},
			},
			instType: "m6i.xlarge",
			region:   "us-west-2",
			wantErr:  false,
		},
		{
			name: "invalid instance type returns empty results",
			client: &mockDescribeInstanceTypes{
				output: &ec2.DescribeInstanceTypesOutput{
					InstanceTypes: []types.InstanceTypeInfo{},
				},
			},
			instType:   "z99.nonexistent",
			region:     "us-west-2",
			wantErr:    true,
			errContain: "z99.nonexistent",
		},
		{
			name: "error message includes region",
			client: &mockDescribeInstanceTypes{
				output: &ec2.DescribeInstanceTypesOutput{
					InstanceTypes: []types.InstanceTypeInfo{},
				},
			},
			instType:   "z99.nonexistent",
			region:     "eu-central-1",
			wantErr:    true,
			errContain: "eu-central-1",
		},
		{
			name: "AWS API error propagated",
			client: &mockDescribeInstanceTypes{
				err: errors.New("access denied"),
			},
			instType:   "m6i.xlarge",
			region:     "us-west-2",
			wantErr:    true,
			errContain: "access denied",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validator := NewInstanceTypeValidator(tt.client)
			err := validator.Validate(context.Background(), tt.instType, tt.region)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("Validate(%q, %q) expected error, got nil", tt.instType, tt.region)
				}
				if tt.errContain != "" && !containsSubstring(err.Error(), tt.errContain) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errContain)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate(%q, %q) unexpected error: %v", tt.instType, tt.region, err)
			}
		})
	}
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// ---------------------------------------------------------------------------
// Inline mock structs
// ---------------------------------------------------------------------------

type mockGetParameter struct {
	output *ssm.GetParameterOutput
	err    error
}

func (m *mockGetParameter) GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	return m.output, m.err
}

// ---------------------------------------------------------------------------
// Compile-time interface satisfaction checks for mocks
// ---------------------------------------------------------------------------

var _ GetParameterAPI = (*mockGetParameter)(nil)

// ---------------------------------------------------------------------------
// ResolveAMI tests
// ---------------------------------------------------------------------------

func TestResolveAMI(t *testing.T) {
	tests := []struct {
		name       string
		client     GetParameterAPI
		wantAMI    string
		wantErr    bool
		errContain string
	}{
		{
			name: "successful resolution returns AMI ID",
			client: &mockGetParameter{
				output: &ssm.GetParameterOutput{
					Parameter: &ssmtypes.Parameter{
						Value: ssmStrPtr("ami-0abcdef1234567890"),
					},
				},
			},
			wantAMI: "ami-0abcdef1234567890",
			wantErr: false,
		},
		{
			name: "API error propagated",
			client: &mockGetParameter{
				err: errors.New("parameter not found"),
			},
			wantErr:    true,
			errContain: "parameter not found",
		},
		{
			name: "nil parameter value",
			client: &mockGetParameter{
				output: &ssm.GetParameterOutput{
					Parameter: &ssmtypes.Parameter{
						Value: nil,
					},
				},
			},
			wantErr:    true,
			errContain: "nil value",
		},
		{
			name: "nil parameter in response",
			client: &mockGetParameter{
				output: &ssm.GetParameterOutput{
					Parameter: nil,
				},
			},
			wantErr:    true,
			errContain: "nil parameter",
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

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func ssmStrPtr(s string) *string {
	return &s
}

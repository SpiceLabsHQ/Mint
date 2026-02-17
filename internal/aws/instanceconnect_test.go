package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2instanceconnect"
)

// ---------------------------------------------------------------------------
// Inline mock structs
// ---------------------------------------------------------------------------

type mockSendSSHPublicKey struct {
	output *ec2instanceconnect.SendSSHPublicKeyOutput
	err    error
}

func (m *mockSendSSHPublicKey) SendSSHPublicKey(ctx context.Context, params *ec2instanceconnect.SendSSHPublicKeyInput, optFns ...func(*ec2instanceconnect.Options)) (*ec2instanceconnect.SendSSHPublicKeyOutput, error) {
	return m.output, m.err
}

// ---------------------------------------------------------------------------
// Compile-time interface satisfaction checks for mocks
// ---------------------------------------------------------------------------

var _ SendSSHPublicKeyAPI = (*mockSendSSHPublicKey)(nil)

// ---------------------------------------------------------------------------
// SendSSHPublicKey interface tests
// ---------------------------------------------------------------------------

func TestSendSSHPublicKeyAPI(t *testing.T) {
	tests := []struct {
		name    string
		client  SendSSHPublicKeyAPI
		wantErr bool
	}{
		{
			name: "successful key push",
			client: &mockSendSSHPublicKey{
				output: &ec2instanceconnect.SendSSHPublicKeyOutput{
					Success: true,
				},
			},
			wantErr: false,
		},
		{
			name: "API error propagated",
			client: &mockSendSSHPublicKey{
				err: errors.New("instance not found"),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := tt.client.SendSSHPublicKey(context.Background(), &ec2instanceconnect.SendSSHPublicKeyInput{})
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !out.Success {
				t.Error("expected Success to be true")
			}
		})
	}
}

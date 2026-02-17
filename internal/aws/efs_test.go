package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/efs"
	efstypes "github.com/aws/aws-sdk-go-v2/service/efs/types"
)

// ---------------------------------------------------------------------------
// Inline mock structs
// ---------------------------------------------------------------------------

type mockDescribeFileSystems struct {
	output *efs.DescribeFileSystemsOutput
	err    error
}

func (m *mockDescribeFileSystems) DescribeFileSystems(ctx context.Context, params *efs.DescribeFileSystemsInput, optFns ...func(*efs.Options)) (*efs.DescribeFileSystemsOutput, error) {
	return m.output, m.err
}

type mockCreateAccessPoint struct {
	output *efs.CreateAccessPointOutput
	err    error
}

func (m *mockCreateAccessPoint) CreateAccessPoint(ctx context.Context, params *efs.CreateAccessPointInput, optFns ...func(*efs.Options)) (*efs.CreateAccessPointOutput, error) {
	return m.output, m.err
}

type mockDescribeAccessPoints struct {
	output *efs.DescribeAccessPointsOutput
	err    error
}

func (m *mockDescribeAccessPoints) DescribeAccessPoints(ctx context.Context, params *efs.DescribeAccessPointsInput, optFns ...func(*efs.Options)) (*efs.DescribeAccessPointsOutput, error) {
	return m.output, m.err
}

// ---------------------------------------------------------------------------
// Compile-time interface satisfaction checks for mocks
// ---------------------------------------------------------------------------

var (
	_ DescribeFileSystemsAPI  = (*mockDescribeFileSystems)(nil)
	_ CreateAccessPointAPI    = (*mockCreateAccessPoint)(nil)
	_ DescribeAccessPointsAPI = (*mockDescribeAccessPoints)(nil)
)

// ---------------------------------------------------------------------------
// EFS file system tests
// ---------------------------------------------------------------------------

func TestDescribeFileSystemsAPI(t *testing.T) {
	tests := []struct {
		name    string
		client  DescribeFileSystemsAPI
		wantErr bool
	}{
		{
			name: "successful describe returns file systems",
			client: &mockDescribeFileSystems{
				output: &efs.DescribeFileSystemsOutput{
					FileSystems: []efstypes.FileSystemDescription{
						{
							FileSystemId: efsStrPtr("fs-abc123"),
							LifeCycleState: efstypes.LifeCycleStateAvailable,
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "empty results",
			client: &mockDescribeFileSystems{
				output: &efs.DescribeFileSystemsOutput{
					FileSystems: []efstypes.FileSystemDescription{},
				},
			},
			wantErr: false,
		},
		{
			name: "API error propagated",
			client: &mockDescribeFileSystems{
				err: errors.New("access denied"),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := tt.client.DescribeFileSystems(context.Background(), &efs.DescribeFileSystemsInput{})
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if out == nil {
				t.Fatal("expected non-nil output")
			}
		})
	}
}

func TestCreateAccessPointAPI(t *testing.T) {
	tests := []struct {
		name    string
		client  CreateAccessPointAPI
		wantErr bool
	}{
		{
			name: "successful create",
			client: &mockCreateAccessPoint{
				output: &efs.CreateAccessPointOutput{
					AccessPointId: efsStrPtr("fsap-abc123"),
					FileSystemId:  efsStrPtr("fs-abc123"),
					LifeCycleState: efstypes.LifeCycleStateAvailable,
				},
			},
			wantErr: false,
		},
		{
			name: "API error propagated",
			client: &mockCreateAccessPoint{
				err: errors.New("file system not found"),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := tt.client.CreateAccessPoint(context.Background(), &efs.CreateAccessPointInput{})
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if *out.AccessPointId != "fsap-abc123" {
				t.Errorf("got access point ID %q, want %q", *out.AccessPointId, "fsap-abc123")
			}
		})
	}
}

func TestDescribeAccessPointsAPI(t *testing.T) {
	tests := []struct {
		name    string
		client  DescribeAccessPointsAPI
		wantErr bool
	}{
		{
			name: "successful describe returns access points",
			client: &mockDescribeAccessPoints{
				output: &efs.DescribeAccessPointsOutput{
					AccessPoints: []efstypes.AccessPointDescription{
						{
							AccessPointId: efsStrPtr("fsap-abc123"),
							FileSystemId:  efsStrPtr("fs-abc123"),
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "empty results",
			client: &mockDescribeAccessPoints{
				output: &efs.DescribeAccessPointsOutput{
					AccessPoints: []efstypes.AccessPointDescription{},
				},
			},
			wantErr: false,
		},
		{
			name: "API error propagated",
			client: &mockDescribeAccessPoints{
				err: errors.New("throttling"),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := tt.client.DescribeAccessPoints(context.Background(), &efs.DescribeAccessPointsInput{})
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if out == nil {
				t.Fatal("expected non-nil output")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func efsStrPtr(s string) *string {
	return &s
}

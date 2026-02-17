package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// ---------------------------------------------------------------------------
// Inline mock structs
// ---------------------------------------------------------------------------

type mockRunInstances struct {
	output *ec2.RunInstancesOutput
	err    error
}

func (m *mockRunInstances) RunInstances(ctx context.Context, params *ec2.RunInstancesInput, optFns ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error) {
	return m.output, m.err
}

type mockStartInstances struct {
	output *ec2.StartInstancesOutput
	err    error
}

func (m *mockStartInstances) StartInstances(ctx context.Context, params *ec2.StartInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error) {
	return m.output, m.err
}

type mockStopInstances struct {
	output *ec2.StopInstancesOutput
	err    error
}

func (m *mockStopInstances) StopInstances(ctx context.Context, params *ec2.StopInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StopInstancesOutput, error) {
	return m.output, m.err
}

type mockTerminateInstances struct {
	output *ec2.TerminateInstancesOutput
	err    error
}

func (m *mockTerminateInstances) TerminateInstances(ctx context.Context, params *ec2.TerminateInstancesInput, optFns ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error) {
	return m.output, m.err
}

type mockDescribeInstances struct {
	output *ec2.DescribeInstancesOutput
	err    error
}

func (m *mockDescribeInstances) DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return m.output, m.err
}

type mockCreateVolume struct {
	output *ec2.CreateVolumeOutput
	err    error
}

func (m *mockCreateVolume) CreateVolume(ctx context.Context, params *ec2.CreateVolumeInput, optFns ...func(*ec2.Options)) (*ec2.CreateVolumeOutput, error) {
	return m.output, m.err
}

type mockAttachVolume struct {
	output *ec2.AttachVolumeOutput
	err    error
}

func (m *mockAttachVolume) AttachVolume(ctx context.Context, params *ec2.AttachVolumeInput, optFns ...func(*ec2.Options)) (*ec2.AttachVolumeOutput, error) {
	return m.output, m.err
}

type mockDetachVolume struct {
	output *ec2.DetachVolumeOutput
	err    error
}

func (m *mockDetachVolume) DetachVolume(ctx context.Context, params *ec2.DetachVolumeInput, optFns ...func(*ec2.Options)) (*ec2.DetachVolumeOutput, error) {
	return m.output, m.err
}

type mockDeleteVolume struct {
	output *ec2.DeleteVolumeOutput
	err    error
}

func (m *mockDeleteVolume) DeleteVolume(ctx context.Context, params *ec2.DeleteVolumeInput, optFns ...func(*ec2.Options)) (*ec2.DeleteVolumeOutput, error) {
	return m.output, m.err
}

type mockDescribeVolumes struct {
	output *ec2.DescribeVolumesOutput
	err    error
}

func (m *mockDescribeVolumes) DescribeVolumes(ctx context.Context, params *ec2.DescribeVolumesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error) {
	return m.output, m.err
}

type mockAllocateAddress struct {
	output *ec2.AllocateAddressOutput
	err    error
}

func (m *mockAllocateAddress) AllocateAddress(ctx context.Context, params *ec2.AllocateAddressInput, optFns ...func(*ec2.Options)) (*ec2.AllocateAddressOutput, error) {
	return m.output, m.err
}

type mockAssociateAddress struct {
	output *ec2.AssociateAddressOutput
	err    error
}

func (m *mockAssociateAddress) AssociateAddress(ctx context.Context, params *ec2.AssociateAddressInput, optFns ...func(*ec2.Options)) (*ec2.AssociateAddressOutput, error) {
	return m.output, m.err
}

type mockReleaseAddress struct {
	output *ec2.ReleaseAddressOutput
	err    error
}

func (m *mockReleaseAddress) ReleaseAddress(ctx context.Context, params *ec2.ReleaseAddressInput, optFns ...func(*ec2.Options)) (*ec2.ReleaseAddressOutput, error) {
	return m.output, m.err
}

type mockDescribeAddresses struct {
	output *ec2.DescribeAddressesOutput
	err    error
}

func (m *mockDescribeAddresses) DescribeAddresses(ctx context.Context, params *ec2.DescribeAddressesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeAddressesOutput, error) {
	return m.output, m.err
}

type mockCreateSecurityGroup struct {
	output *ec2.CreateSecurityGroupOutput
	err    error
}

func (m *mockCreateSecurityGroup) CreateSecurityGroup(ctx context.Context, params *ec2.CreateSecurityGroupInput, optFns ...func(*ec2.Options)) (*ec2.CreateSecurityGroupOutput, error) {
	return m.output, m.err
}

type mockAuthorizeSecurityGroupIngress struct {
	output *ec2.AuthorizeSecurityGroupIngressOutput
	err    error
}

func (m *mockAuthorizeSecurityGroupIngress) AuthorizeSecurityGroupIngress(ctx context.Context, params *ec2.AuthorizeSecurityGroupIngressInput, optFns ...func(*ec2.Options)) (*ec2.AuthorizeSecurityGroupIngressOutput, error) {
	return m.output, m.err
}

type mockDescribeSecurityGroups struct {
	output *ec2.DescribeSecurityGroupsOutput
	err    error
}

func (m *mockDescribeSecurityGroups) DescribeSecurityGroups(ctx context.Context, params *ec2.DescribeSecurityGroupsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error) {
	return m.output, m.err
}

type mockCreateTags struct {
	output *ec2.CreateTagsOutput
	err    error
}

func (m *mockCreateTags) CreateTags(ctx context.Context, params *ec2.CreateTagsInput, optFns ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error) {
	return m.output, m.err
}

type mockDescribeSubnets struct {
	output *ec2.DescribeSubnetsOutput
	err    error
}

func (m *mockDescribeSubnets) DescribeSubnets(ctx context.Context, params *ec2.DescribeSubnetsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error) {
	return m.output, m.err
}

type mockDescribeVpcs struct {
	output *ec2.DescribeVpcsOutput
	err    error
}

func (m *mockDescribeVpcs) DescribeVpcs(ctx context.Context, params *ec2.DescribeVpcsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error) {
	return m.output, m.err
}

// ---------------------------------------------------------------------------
// Compile-time interface satisfaction checks for mocks
// ---------------------------------------------------------------------------

var (
	_ RunInstancesAPI                  = (*mockRunInstances)(nil)
	_ StartInstancesAPI                = (*mockStartInstances)(nil)
	_ StopInstancesAPI                 = (*mockStopInstances)(nil)
	_ TerminateInstancesAPI            = (*mockTerminateInstances)(nil)
	_ DescribeInstancesAPI             = (*mockDescribeInstances)(nil)
	_ CreateVolumeAPI                  = (*mockCreateVolume)(nil)
	_ AttachVolumeAPI                  = (*mockAttachVolume)(nil)
	_ DetachVolumeAPI                  = (*mockDetachVolume)(nil)
	_ DeleteVolumeAPI                  = (*mockDeleteVolume)(nil)
	_ DescribeVolumesAPI               = (*mockDescribeVolumes)(nil)
	_ AllocateAddressAPI               = (*mockAllocateAddress)(nil)
	_ AssociateAddressAPI              = (*mockAssociateAddress)(nil)
	_ ReleaseAddressAPI                = (*mockReleaseAddress)(nil)
	_ DescribeAddressesAPI             = (*mockDescribeAddresses)(nil)
	_ CreateSecurityGroupAPI           = (*mockCreateSecurityGroup)(nil)
	_ AuthorizeSecurityGroupIngressAPI = (*mockAuthorizeSecurityGroupIngress)(nil)
	_ DescribeSecurityGroupsAPI        = (*mockDescribeSecurityGroups)(nil)
	_ CreateTagsAPI                    = (*mockCreateTags)(nil)
	_ DescribeSubnetsAPI               = (*mockDescribeSubnets)(nil)
	_ DescribeVpcsAPI                  = (*mockDescribeVpcs)(nil)
)

// ---------------------------------------------------------------------------
// Instance lifecycle tests
// ---------------------------------------------------------------------------

func TestRunInstancesAPI(t *testing.T) {
	tests := []struct {
		name    string
		client  RunInstancesAPI
		wantErr bool
	}{
		{
			name: "successful launch returns reservation",
			client: &mockRunInstances{
				output: &ec2.RunInstancesOutput{
					Instances: []types.Instance{
						{InstanceId: strPtr("i-abc123")},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "API error propagated",
			client: &mockRunInstances{
				err: errors.New("insufficient capacity"),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := tt.client.RunInstances(context.Background(), &ec2.RunInstancesInput{})
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(out.Instances) == 0 {
				t.Fatal("expected at least one instance in output")
			}
			if *out.Instances[0].InstanceId != "i-abc123" {
				t.Errorf("got instance ID %q, want %q", *out.Instances[0].InstanceId, "i-abc123")
			}
		})
	}
}

func TestStartInstancesAPI(t *testing.T) {
	tests := []struct {
		name    string
		client  StartInstancesAPI
		wantErr bool
	}{
		{
			name: "successful start",
			client: &mockStartInstances{
				output: &ec2.StartInstancesOutput{
					StartingInstances: []types.InstanceStateChange{
						{InstanceId: strPtr("i-abc123")},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "API error propagated",
			client: &mockStartInstances{
				err: errors.New("invalid instance id"),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := tt.client.StartInstances(context.Background(), &ec2.StartInstancesInput{})
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(out.StartingInstances) == 0 {
				t.Fatal("expected at least one state change")
			}
		})
	}
}

func TestStopInstancesAPI(t *testing.T) {
	tests := []struct {
		name    string
		client  StopInstancesAPI
		wantErr bool
	}{
		{
			name: "successful stop",
			client: &mockStopInstances{
				output: &ec2.StopInstancesOutput{
					StoppingInstances: []types.InstanceStateChange{
						{InstanceId: strPtr("i-abc123")},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "API error propagated",
			client: &mockStopInstances{
				err: errors.New("cannot stop terminated instance"),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := tt.client.StopInstances(context.Background(), &ec2.StopInstancesInput{})
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(out.StoppingInstances) == 0 {
				t.Fatal("expected at least one state change")
			}
		})
	}
}

func TestTerminateInstancesAPI(t *testing.T) {
	tests := []struct {
		name    string
		client  TerminateInstancesAPI
		wantErr bool
	}{
		{
			name: "successful terminate",
			client: &mockTerminateInstances{
				output: &ec2.TerminateInstancesOutput{
					TerminatingInstances: []types.InstanceStateChange{
						{InstanceId: strPtr("i-abc123")},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "API error propagated",
			client: &mockTerminateInstances{
				err: errors.New("access denied"),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := tt.client.TerminateInstances(context.Background(), &ec2.TerminateInstancesInput{})
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(out.TerminatingInstances) == 0 {
				t.Fatal("expected at least one state change")
			}
		})
	}
}

func TestDescribeInstancesAPI(t *testing.T) {
	tests := []struct {
		name    string
		client  DescribeInstancesAPI
		wantErr bool
	}{
		{
			name: "successful describe returns reservations",
			client: &mockDescribeInstances{
				output: &ec2.DescribeInstancesOutput{
					Reservations: []types.Reservation{
						{
							Instances: []types.Instance{
								{InstanceId: strPtr("i-abc123")},
							},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "empty results",
			client: &mockDescribeInstances{
				output: &ec2.DescribeInstancesOutput{
					Reservations: []types.Reservation{},
				},
			},
			wantErr: false,
		},
		{
			name: "API error propagated",
			client: &mockDescribeInstances{
				err: errors.New("throttling"),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := tt.client.DescribeInstances(context.Background(), &ec2.DescribeInstancesInput{})
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
// EBS volume tests
// ---------------------------------------------------------------------------

func TestCreateVolumeAPI(t *testing.T) {
	tests := []struct {
		name    string
		client  CreateVolumeAPI
		wantErr bool
	}{
		{
			name: "successful create",
			client: &mockCreateVolume{
				output: &ec2.CreateVolumeOutput{
					VolumeId: strPtr("vol-abc123"),
				},
			},
			wantErr: false,
		},
		{
			name: "API error propagated",
			client: &mockCreateVolume{
				err: errors.New("volume limit exceeded"),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := tt.client.CreateVolume(context.Background(), &ec2.CreateVolumeInput{})
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if *out.VolumeId != "vol-abc123" {
				t.Errorf("got volume ID %q, want %q", *out.VolumeId, "vol-abc123")
			}
		})
	}
}

func TestAttachVolumeAPI(t *testing.T) {
	tests := []struct {
		name    string
		client  AttachVolumeAPI
		wantErr bool
	}{
		{
			name: "successful attach",
			client: &mockAttachVolume{
				output: &ec2.AttachVolumeOutput{
					VolumeId: strPtr("vol-abc123"),
					State:    types.VolumeAttachmentStateAttaching,
				},
			},
			wantErr: false,
		},
		{
			name: "API error propagated",
			client: &mockAttachVolume{
				err: errors.New("volume already attached"),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := tt.client.AttachVolume(context.Background(), &ec2.AttachVolumeInput{})
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if out.State != types.VolumeAttachmentStateAttaching {
				t.Errorf("got state %q, want %q", out.State, types.VolumeAttachmentStateAttaching)
			}
		})
	}
}

func TestDetachVolumeAPI(t *testing.T) {
	tests := []struct {
		name    string
		client  DetachVolumeAPI
		wantErr bool
	}{
		{
			name: "successful detach",
			client: &mockDetachVolume{
				output: &ec2.DetachVolumeOutput{
					VolumeId: strPtr("vol-abc123"),
					State:    types.VolumeAttachmentStateDetaching,
				},
			},
			wantErr: false,
		},
		{
			name: "API error propagated",
			client: &mockDetachVolume{
				err: errors.New("volume not attached"),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := tt.client.DetachVolume(context.Background(), &ec2.DetachVolumeInput{})
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if out.State != types.VolumeAttachmentStateDetaching {
				t.Errorf("got state %q, want %q", out.State, types.VolumeAttachmentStateDetaching)
			}
		})
	}
}

func TestDeleteVolumeAPI(t *testing.T) {
	tests := []struct {
		name    string
		client  DeleteVolumeAPI
		wantErr bool
	}{
		{
			name: "successful delete",
			client: &mockDeleteVolume{
				output: &ec2.DeleteVolumeOutput{},
			},
			wantErr: false,
		},
		{
			name: "API error propagated",
			client: &mockDeleteVolume{
				err: errors.New("volume in use"),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.client.DeleteVolume(context.Background(), &ec2.DeleteVolumeInput{})
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestDescribeVolumesAPI(t *testing.T) {
	tests := []struct {
		name    string
		client  DescribeVolumesAPI
		wantErr bool
	}{
		{
			name: "successful describe",
			client: &mockDescribeVolumes{
				output: &ec2.DescribeVolumesOutput{
					Volumes: []types.Volume{
						{VolumeId: strPtr("vol-abc123")},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "API error propagated",
			client: &mockDescribeVolumes{
				err: errors.New("throttling"),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := tt.client.DescribeVolumes(context.Background(), &ec2.DescribeVolumesInput{})
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(out.Volumes) == 0 {
				t.Fatal("expected at least one volume")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Elastic IP tests
// ---------------------------------------------------------------------------

func TestAllocateAddressAPI(t *testing.T) {
	tests := []struct {
		name    string
		client  AllocateAddressAPI
		wantErr bool
	}{
		{
			name: "successful allocate",
			client: &mockAllocateAddress{
				output: &ec2.AllocateAddressOutput{
					AllocationId: strPtr("eipalloc-abc123"),
					PublicIp:     strPtr("203.0.113.1"),
				},
			},
			wantErr: false,
		},
		{
			name: "API error propagated",
			client: &mockAllocateAddress{
				err: errors.New("address limit exceeded"),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := tt.client.AllocateAddress(context.Background(), &ec2.AllocateAddressInput{})
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if *out.AllocationId != "eipalloc-abc123" {
				t.Errorf("got allocation ID %q, want %q", *out.AllocationId, "eipalloc-abc123")
			}
		})
	}
}

func TestAssociateAddressAPI(t *testing.T) {
	tests := []struct {
		name    string
		client  AssociateAddressAPI
		wantErr bool
	}{
		{
			name: "successful associate",
			client: &mockAssociateAddress{
				output: &ec2.AssociateAddressOutput{
					AssociationId: strPtr("eipassoc-abc123"),
				},
			},
			wantErr: false,
		},
		{
			name: "API error propagated",
			client: &mockAssociateAddress{
				err: errors.New("invalid allocation id"),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := tt.client.AssociateAddress(context.Background(), &ec2.AssociateAddressInput{})
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if *out.AssociationId != "eipassoc-abc123" {
				t.Errorf("got association ID %q, want %q", *out.AssociationId, "eipassoc-abc123")
			}
		})
	}
}

func TestReleaseAddressAPI(t *testing.T) {
	tests := []struct {
		name    string
		client  ReleaseAddressAPI
		wantErr bool
	}{
		{
			name: "successful release",
			client: &mockReleaseAddress{
				output: &ec2.ReleaseAddressOutput{},
			},
			wantErr: false,
		},
		{
			name: "API error propagated",
			client: &mockReleaseAddress{
				err: errors.New("address in use"),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.client.ReleaseAddress(context.Background(), &ec2.ReleaseAddressInput{})
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestDescribeAddressesAPI(t *testing.T) {
	tests := []struct {
		name    string
		client  DescribeAddressesAPI
		wantErr bool
	}{
		{
			name: "successful describe",
			client: &mockDescribeAddresses{
				output: &ec2.DescribeAddressesOutput{
					Addresses: []types.Address{
						{PublicIp: strPtr("203.0.113.1")},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "API error propagated",
			client: &mockDescribeAddresses{
				err: errors.New("throttling"),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := tt.client.DescribeAddresses(context.Background(), &ec2.DescribeAddressesInput{})
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(out.Addresses) == 0 {
				t.Fatal("expected at least one address")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Security group tests
// ---------------------------------------------------------------------------

func TestCreateSecurityGroupAPI(t *testing.T) {
	tests := []struct {
		name    string
		client  CreateSecurityGroupAPI
		wantErr bool
	}{
		{
			name: "successful create",
			client: &mockCreateSecurityGroup{
				output: &ec2.CreateSecurityGroupOutput{
					GroupId: strPtr("sg-abc123"),
				},
			},
			wantErr: false,
		},
		{
			name: "API error propagated",
			client: &mockCreateSecurityGroup{
				err: errors.New("duplicate group name"),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := tt.client.CreateSecurityGroup(context.Background(), &ec2.CreateSecurityGroupInput{})
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if *out.GroupId != "sg-abc123" {
				t.Errorf("got group ID %q, want %q", *out.GroupId, "sg-abc123")
			}
		})
	}
}

func TestAuthorizeSecurityGroupIngressAPI(t *testing.T) {
	tests := []struct {
		name    string
		client  AuthorizeSecurityGroupIngressAPI
		wantErr bool
	}{
		{
			name: "successful authorize",
			client: &mockAuthorizeSecurityGroupIngress{
				output: &ec2.AuthorizeSecurityGroupIngressOutput{
					Return: boolPtr(true),
				},
			},
			wantErr: false,
		},
		{
			name: "API error propagated",
			client: &mockAuthorizeSecurityGroupIngress{
				err: errors.New("duplicate rule"),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := tt.client.AuthorizeSecurityGroupIngress(context.Background(), &ec2.AuthorizeSecurityGroupIngressInput{})
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if out.Return == nil || !*out.Return {
				t.Error("expected Return to be true")
			}
		})
	}
}

func TestDescribeSecurityGroupsAPI(t *testing.T) {
	tests := []struct {
		name    string
		client  DescribeSecurityGroupsAPI
		wantErr bool
	}{
		{
			name: "successful describe",
			client: &mockDescribeSecurityGroups{
				output: &ec2.DescribeSecurityGroupsOutput{
					SecurityGroups: []types.SecurityGroup{
						{GroupId: strPtr("sg-abc123")},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "API error propagated",
			client: &mockDescribeSecurityGroups{
				err: errors.New("throttling"),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := tt.client.DescribeSecurityGroups(context.Background(), &ec2.DescribeSecurityGroupsInput{})
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(out.SecurityGroups) == 0 {
				t.Fatal("expected at least one security group")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Tags and networking tests
// ---------------------------------------------------------------------------

func TestCreateTagsAPI(t *testing.T) {
	tests := []struct {
		name    string
		client  CreateTagsAPI
		wantErr bool
	}{
		{
			name: "successful create tags",
			client: &mockCreateTags{
				output: &ec2.CreateTagsOutput{},
			},
			wantErr: false,
		},
		{
			name: "API error propagated",
			client: &mockCreateTags{
				err: errors.New("invalid resource id"),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.client.CreateTags(context.Background(), &ec2.CreateTagsInput{})
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestDescribeSubnetsAPI(t *testing.T) {
	tests := []struct {
		name    string
		client  DescribeSubnetsAPI
		wantErr bool
	}{
		{
			name: "successful describe",
			client: &mockDescribeSubnets{
				output: &ec2.DescribeSubnetsOutput{
					Subnets: []types.Subnet{
						{SubnetId: strPtr("subnet-abc123")},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "API error propagated",
			client: &mockDescribeSubnets{
				err: errors.New("throttling"),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := tt.client.DescribeSubnets(context.Background(), &ec2.DescribeSubnetsInput{})
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(out.Subnets) == 0 {
				t.Fatal("expected at least one subnet")
			}
		})
	}
}

func TestDescribeVpcsAPI(t *testing.T) {
	tests := []struct {
		name    string
		client  DescribeVpcsAPI
		wantErr bool
	}{
		{
			name: "successful describe",
			client: &mockDescribeVpcs{
				output: &ec2.DescribeVpcsOutput{
					Vpcs: []types.Vpc{
						{VpcId: strPtr("vpc-abc123")},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "API error propagated",
			client: &mockDescribeVpcs{
				err: errors.New("throttling"),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := tt.client.DescribeVpcs(context.Background(), &ec2.DescribeVpcsInput{})
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(out.Vpcs) == 0 {
				t.Fatal("expected at least one VPC")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func strPtr(s string) *string {
	return &s
}

func boolPtr(b bool) *bool {
	return &b
}

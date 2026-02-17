package provision

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// ---------------------------------------------------------------------------
// Inline mocks for destroy
// ---------------------------------------------------------------------------

type mockDestroyDescribeInstances struct {
	output *ec2.DescribeInstancesOutput
	err    error
}

func (m *mockDestroyDescribeInstances) DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return m.output, m.err
}

type mockTerminateInstances struct {
	output *ec2.TerminateInstancesOutput
	err    error
	called bool
	input  *ec2.TerminateInstancesInput
}

func (m *mockTerminateInstances) TerminateInstances(ctx context.Context, params *ec2.TerminateInstancesInput, optFns ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error) {
	m.called = true
	m.input = params
	return m.output, m.err
}

type mockDestroyDescribeVolumes struct {
	output *ec2.DescribeVolumesOutput
	err    error
}

func (m *mockDestroyDescribeVolumes) DescribeVolumes(ctx context.Context, params *ec2.DescribeVolumesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error) {
	return m.output, m.err
}

type mockDetachVolume struct {
	output *ec2.DetachVolumeOutput
	err    error
	called bool
	input  *ec2.DetachVolumeInput
}

func (m *mockDetachVolume) DetachVolume(ctx context.Context, params *ec2.DetachVolumeInput, optFns ...func(*ec2.Options)) (*ec2.DetachVolumeOutput, error) {
	m.called = true
	m.input = params
	return m.output, m.err
}

type mockDeleteVolume struct {
	output *ec2.DeleteVolumeOutput
	err    error
	called bool
	inputs []*ec2.DeleteVolumeInput
}

func (m *mockDeleteVolume) DeleteVolume(ctx context.Context, params *ec2.DeleteVolumeInput, optFns ...func(*ec2.Options)) (*ec2.DeleteVolumeOutput, error) {
	m.called = true
	m.inputs = append(m.inputs, params)
	return m.output, m.err
}

type mockDestroyDescribeAddresses struct {
	output *ec2.DescribeAddressesOutput
	err    error
}

func (m *mockDestroyDescribeAddresses) DescribeAddresses(ctx context.Context, params *ec2.DescribeAddressesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeAddressesOutput, error) {
	return m.output, m.err
}

type mockReleaseAddress struct {
	output *ec2.ReleaseAddressOutput
	err    error
	called bool
	input  *ec2.ReleaseAddressInput
}

func (m *mockReleaseAddress) ReleaseAddress(ctx context.Context, params *ec2.ReleaseAddressInput, optFns ...func(*ec2.Options)) (*ec2.ReleaseAddressOutput, error) {
	m.called = true
	m.input = params
	return m.output, m.err
}

// ---------------------------------------------------------------------------
// Helper: build a Destroyer with all mocks
// ---------------------------------------------------------------------------

type destroyMocks struct {
	describe        *mockDestroyDescribeInstances
	terminate       *mockTerminateInstances
	describeVolumes *mockDestroyDescribeVolumes
	detachVolume    *mockDetachVolume
	deleteVolume    *mockDeleteVolume
	describeAddrs   *mockDestroyDescribeAddresses
	releaseAddr     *mockReleaseAddress
}

func newDestroyHappyMocks() *destroyMocks {
	return &destroyMocks{
		describe: &mockDestroyDescribeInstances{
			output: &ec2.DescribeInstancesOutput{
				Reservations: []ec2types.Reservation{{
					Instances: []ec2types.Instance{{
						InstanceId:   aws.String("i-abc123"),
						InstanceType: ec2types.InstanceTypeT3Medium,
						State: &ec2types.InstanceState{
							Name: ec2types.InstanceStateNameRunning,
						},
						Tags: []ec2types.Tag{
							{Key: aws.String("mint:vm"), Value: aws.String("default")},
							{Key: aws.String("mint:owner"), Value: aws.String("alice")},
						},
					}},
				}},
			},
		},
		terminate: &mockTerminateInstances{
			output: &ec2.TerminateInstancesOutput{},
		},
		describeVolumes: &mockDestroyDescribeVolumes{
			output: &ec2.DescribeVolumesOutput{
				Volumes: []ec2types.Volume{{
					VolumeId: aws.String("vol-proj1"),
					State:    ec2types.VolumeStateInUse,
				}},
			},
		},
		detachVolume: &mockDetachVolume{
			output: &ec2.DetachVolumeOutput{},
		},
		deleteVolume: &mockDeleteVolume{
			output: &ec2.DeleteVolumeOutput{},
		},
		describeAddrs: &mockDestroyDescribeAddresses{
			output: &ec2.DescribeAddressesOutput{
				Addresses: []ec2types.Address{{
					AllocationId: aws.String("eipalloc-abc123"),
					PublicIp:     aws.String("1.2.3.4"),
				}},
			},
		},
		releaseAddr: &mockReleaseAddress{
			output: &ec2.ReleaseAddressOutput{},
		},
	}
}

func (m *destroyMocks) build() *Destroyer {
	return NewDestroyer(
		m.describe,
		m.terminate,
		m.describeVolumes,
		m.detachVolume,
		m.deleteVolume,
		m.describeAddrs,
		m.releaseAddr,
	)
}

// ---------------------------------------------------------------------------
// Tests: Full destroy flow
// ---------------------------------------------------------------------------

func TestDestroyRun(t *testing.T) {
	tests := []struct {
		name           string
		setup          func(*destroyMocks)
		owner          string
		vmName         string
		confirmed      bool
		wantErr        bool
		wantErrContain string
	}{
		{
			name:      "happy path - all resources destroyed",
			setup:     func(m *destroyMocks) {},
			owner:     "alice",
			vmName:    "default",
			confirmed: true,
		},
		{
			name:           "not confirmed returns error",
			setup:          func(m *destroyMocks) {},
			owner:          "alice",
			vmName:         "default",
			confirmed:      false,
			wantErr:        true,
			wantErrContain: "not confirmed",
		},
		{
			name: "no VM found returns error",
			setup: func(m *destroyMocks) {
				m.describe.output = &ec2.DescribeInstancesOutput{}
			},
			owner:          "alice",
			vmName:         "default",
			confirmed:      true,
			wantErr:        true,
			wantErrContain: "no VM",
		},
		{
			name: "describe instances error propagates",
			setup: func(m *destroyMocks) {
				m.describe.err = fmt.Errorf("throttled")
			},
			owner:          "alice",
			vmName:         "default",
			confirmed:      true,
			wantErr:        true,
			wantErrContain: "throttled",
		},
		{
			name: "terminate error propagates",
			setup: func(m *destroyMocks) {
				m.terminate.err = fmt.Errorf("permission denied")
			},
			owner:          "alice",
			vmName:         "default",
			confirmed:      true,
			wantErr:        true,
			wantErrContain: "permission denied",
		},
		{
			name: "no project volumes - continues without error",
			setup: func(m *destroyMocks) {
				m.describeVolumes.output = &ec2.DescribeVolumesOutput{
					Volumes: []ec2types.Volume{},
				}
			},
			owner:     "alice",
			vmName:    "default",
			confirmed: true,
		},
		{
			name: "no elastic IP - continues without error",
			setup: func(m *destroyMocks) {
				m.describeAddrs.output = &ec2.DescribeAddressesOutput{
					Addresses: []ec2types.Address{},
				}
			},
			owner:     "alice",
			vmName:    "default",
			confirmed: true,
		},
		{
			name: "volume describe error is non-fatal (logged, continues)",
			setup: func(m *destroyMocks) {
				m.describeVolumes.err = fmt.Errorf("volume API down")
			},
			owner:     "alice",
			vmName:    "default",
			confirmed: true,
			// Should succeed despite volume discovery failure.
		},
		{
			name: "EIP describe error is non-fatal (logged, continues)",
			setup: func(m *destroyMocks) {
				m.describeAddrs.err = fmt.Errorf("EIP API down")
			},
			owner:     "alice",
			vmName:    "default",
			confirmed: true,
			// Should succeed despite EIP discovery failure.
		},
		{
			name: "detach volume error is non-fatal",
			setup: func(m *destroyMocks) {
				m.detachVolume.err = fmt.Errorf("volume already detached")
			},
			owner:     "alice",
			vmName:    "default",
			confirmed: true,
		},
		{
			name: "delete volume error is non-fatal",
			setup: func(m *destroyMocks) {
				m.deleteVolume.err = fmt.Errorf("volume not found")
			},
			owner:     "alice",
			vmName:    "default",
			confirmed: true,
		},
		{
			name: "release address error is non-fatal",
			setup: func(m *destroyMocks) {
				m.releaseAddr.err = fmt.Errorf("address not found")
			},
			owner:     "alice",
			vmName:    "default",
			confirmed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newDestroyHappyMocks()
			tt.setup(m)
			d := m.build()

			err := d.Run(context.Background(), tt.owner, tt.vmName, tt.confirmed)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.wantErrContain != "" && !strings.Contains(err.Error(), tt.wantErrContain) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.wantErrContain)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Tests: Individual resource cleanup steps
// ---------------------------------------------------------------------------

func TestDestroyTerminatesCorrectInstance(t *testing.T) {
	m := newDestroyHappyMocks()
	d := m.build()

	err := d.Run(context.Background(), "alice", "default", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !m.terminate.called {
		t.Fatal("TerminateInstances was not called")
	}
	if len(m.terminate.input.InstanceIds) != 1 {
		t.Fatalf("expected 1 instance ID, got %d", len(m.terminate.input.InstanceIds))
	}
	if m.terminate.input.InstanceIds[0] != "i-abc123" {
		t.Errorf("terminated instance ID = %q, want %q", m.terminate.input.InstanceIds[0], "i-abc123")
	}
}

func TestDestroyDeletesProjectVolume(t *testing.T) {
	m := newDestroyHappyMocks()
	d := m.build()

	err := d.Run(context.Background(), "alice", "default", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !m.deleteVolume.called {
		t.Fatal("DeleteVolume was not called")
	}
	if len(m.deleteVolume.inputs) != 1 {
		t.Fatalf("expected 1 DeleteVolume call, got %d", len(m.deleteVolume.inputs))
	}
	if aws.ToString(m.deleteVolume.inputs[0].VolumeId) != "vol-proj1" {
		t.Errorf("deleted volume ID = %q, want %q", aws.ToString(m.deleteVolume.inputs[0].VolumeId), "vol-proj1")
	}
}

func TestDestroyDetachesInUseVolume(t *testing.T) {
	m := newDestroyHappyMocks()
	d := m.build()

	err := d.Run(context.Background(), "alice", "default", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !m.detachVolume.called {
		t.Fatal("DetachVolume was not called for in-use volume")
	}
	if aws.ToString(m.detachVolume.input.VolumeId) != "vol-proj1" {
		t.Errorf("detached volume ID = %q, want %q", aws.ToString(m.detachVolume.input.VolumeId), "vol-proj1")
	}
}

func TestDestroySkipsDetachForAvailableVolume(t *testing.T) {
	m := newDestroyHappyMocks()
	m.describeVolumes.output = &ec2.DescribeVolumesOutput{
		Volumes: []ec2types.Volume{{
			VolumeId: aws.String("vol-avail"),
			State:    ec2types.VolumeStateAvailable,
		}},
	}
	d := m.build()

	err := d.Run(context.Background(), "alice", "default", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if m.detachVolume.called {
		t.Error("DetachVolume should not be called for available volume")
	}
	if !m.deleteVolume.called {
		t.Fatal("DeleteVolume should still be called")
	}
}

func TestDestroyReleasesElasticIP(t *testing.T) {
	m := newDestroyHappyMocks()
	d := m.build()

	err := d.Run(context.Background(), "alice", "default", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !m.releaseAddr.called {
		t.Fatal("ReleaseAddress was not called")
	}
	if aws.ToString(m.releaseAddr.input.AllocationId) != "eipalloc-abc123" {
		t.Errorf("released allocation ID = %q, want %q", aws.ToString(m.releaseAddr.input.AllocationId), "eipalloc-abc123")
	}
}

func TestDestroyMultipleVolumes(t *testing.T) {
	m := newDestroyHappyMocks()
	m.describeVolumes.output = &ec2.DescribeVolumesOutput{
		Volumes: []ec2types.Volume{
			{VolumeId: aws.String("vol-1"), State: ec2types.VolumeStateAvailable},
			{VolumeId: aws.String("vol-2"), State: ec2types.VolumeStateAvailable},
		},
	}
	d := m.build()

	err := d.Run(context.Background(), "alice", "default", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(m.deleteVolume.inputs) != 2 {
		t.Fatalf("expected 2 DeleteVolume calls, got %d", len(m.deleteVolume.inputs))
	}
}

// ---------------------------------------------------------------------------
// Tests: DestroyResult
// ---------------------------------------------------------------------------

func TestDestroyRunResult(t *testing.T) {
	m := newDestroyHappyMocks()
	d := m.build()

	result, err := d.RunWithResult(context.Background(), "alice", "default", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.InstanceID != "i-abc123" {
		t.Errorf("result.InstanceID = %q, want %q", result.InstanceID, "i-abc123")
	}
	if result.VolumesDeleted != 1 {
		t.Errorf("result.VolumesDeleted = %d, want 1", result.VolumesDeleted)
	}
	if !result.EIPReleased {
		t.Error("result.EIPReleased should be true")
	}
}

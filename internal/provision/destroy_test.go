package provision

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

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

// mockWaitTerminated records call ordering relative to DeleteVolume.
type mockWaitTerminated struct {
	err    error
	called bool
	// callOrder is incremented by a shared counter so tests can verify
	// wait happens before delete.
	callOrder int
	mu        sync.Mutex
	counter   *int // pointer to a shared counter (set by test)
}

func (m *mockWaitTerminated) Wait(ctx context.Context, params *ec2.DescribeInstancesInput, maxWaitDur time.Duration, optFns ...func(*ec2.InstanceTerminatedWaiterOptions)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.called = true
	if m.counter != nil {
		*m.counter++
		m.callOrder = *m.counter
	}
	return m.err
}

// mockDeleteVolumeOrdered records the call order for ordering checks.
type mockDeleteVolumeOrdered struct {
	output    *ec2.DeleteVolumeOutput
	err       error
	called    bool
	inputs    []*ec2.DeleteVolumeInput
	callOrder int
	mu        sync.Mutex
	counter   *int
}

func (m *mockDeleteVolumeOrdered) DeleteVolume(ctx context.Context, params *ec2.DeleteVolumeInput, optFns ...func(*ec2.Options)) (*ec2.DeleteVolumeOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.called = true
	m.inputs = append(m.inputs, params)
	if m.counter != nil {
		*m.counter++
		m.callOrder = *m.counter
	}
	return m.output, m.err
}

// ---------------------------------------------------------------------------
// Helper: build a Destroyer with all mocks
// ---------------------------------------------------------------------------

type destroyMocks struct {
	describe        *mockDestroyDescribeInstances
	terminate       *mockTerminateInstances
	waitTerminated  *mockWaitTerminated
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
		// waitTerminated is nil by default — existing tests keep working without a
		// waiter because WithWaitTerminated is optional (nil = no wait).
		waitTerminated: nil,
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
	d := NewDestroyer(
		m.describe,
		m.terminate,
		m.describeVolumes,
		m.detachVolume,
		m.deleteVolume,
		m.describeAddrs,
		m.releaseAddr,
	)
	if m.waitTerminated != nil {
		d.WithWaitTerminated(m.waitTerminated)
	}
	return d
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

// ---------------------------------------------------------------------------
// Tests: Destroyer WithLogger and structured logging
// ---------------------------------------------------------------------------

func TestDestroyerWithLoggerLogsTerminateInstances(t *testing.T) {
	m := newDestroyHappyMocks()
	d := m.build()

	logger := &mockLogger{}
	d.WithLogger(logger)

	err := d.Run(context.Background(), "alice", "default", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entry, found := logger.findEntry("TerminateInstances")
	if !found {
		t.Fatal("logger.Log not called with operation=TerminateInstances")
	}
	if entry.service != "ec2" {
		t.Errorf("service = %q, want %q", entry.service, "ec2")
	}
	if entry.duration < 0 {
		t.Errorf("duration = %v, want >= 0", entry.duration)
	}
	if entry.err != nil {
		t.Errorf("err = %v, want nil on success", entry.err)
	}
}

func TestDestroyerWithLoggerLogsDetachVolume(t *testing.T) {
	m := newDestroyHappyMocks()
	// Default happy mocks have an in-use volume, so DetachVolume is called.
	d := m.build()

	logger := &mockLogger{}
	d.WithLogger(logger)

	err := d.Run(context.Background(), "alice", "default", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entry, found := logger.findEntry("DetachVolume")
	if !found {
		t.Fatal("logger.Log not called with operation=DetachVolume")
	}
	if entry.service != "ec2" {
		t.Errorf("service = %q, want %q", entry.service, "ec2")
	}
}

func TestDestroyerWithLoggerLogsDeleteVolume(t *testing.T) {
	m := newDestroyHappyMocks()
	d := m.build()

	logger := &mockLogger{}
	d.WithLogger(logger)

	err := d.Run(context.Background(), "alice", "default", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entry, found := logger.findEntry("DeleteVolume")
	if !found {
		t.Fatal("logger.Log not called with operation=DeleteVolume")
	}
	if entry.service != "ec2" {
		t.Errorf("service = %q, want %q", entry.service, "ec2")
	}
}

func TestDestroyerWithLoggerLogsReleaseAddress(t *testing.T) {
	m := newDestroyHappyMocks()
	d := m.build()

	logger := &mockLogger{}
	d.WithLogger(logger)

	err := d.Run(context.Background(), "alice", "default", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entry, found := logger.findEntry("ReleaseAddress")
	if !found {
		t.Fatal("logger.Log not called with operation=ReleaseAddress")
	}
	if entry.service != "ec2" {
		t.Errorf("service = %q, want %q", entry.service, "ec2")
	}
}

func TestDestroyerWithLoggerLogsTerminateError(t *testing.T) {
	m := newDestroyHappyMocks()
	m.terminate.err = fmt.Errorf("permission denied")
	d := m.build()

	logger := &mockLogger{}
	d.WithLogger(logger)

	err := d.Run(context.Background(), "alice", "default", true)
	if err == nil {
		t.Fatal("expected error")
	}

	entry, found := logger.findEntry("TerminateInstances")
	if !found {
		t.Fatal("logger.Log not called with operation=TerminateInstances even on failure")
	}
	if entry.err == nil {
		t.Error("expected non-nil err in log entry when TerminateInstances fails")
	}
	if !strings.Contains(entry.err.Error(), "permission denied") {
		t.Errorf("logged err = %q, want to contain %q", entry.err.Error(), "permission denied")
	}
}

func TestDestroyerNilLoggerNoChange(t *testing.T) {
	// When no logger is set, destroy should succeed without panicking.
	m := newDestroyHappyMocks()
	d := m.build()
	// No WithLogger call — logger field is nil.

	err := d.Run(context.Background(), "alice", "default", true)
	if err != nil {
		t.Fatalf("unexpected error with nil logger: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Tests: WaitInstanceTerminated
// ---------------------------------------------------------------------------

// TestDestroyWaiterCalledBeforeDeleteVolume verifies that when a waiter is
// configured, Wait is invoked before DeleteVolume. This enforces the ordering
// that prevents VolumeInUse errors from EC2's async detach on termination.
func TestDestroyWaiterCalledBeforeDeleteVolume(t *testing.T) {
	counter := 0
	wait := &mockWaitTerminated{counter: &counter}
	del := &mockDeleteVolumeOrdered{
		output:  &ec2.DeleteVolumeOutput{},
		counter: &counter,
	}

	m := newDestroyHappyMocks()
	m.waitTerminated = wait
	m.deleteVolume = nil // We override below after building.

	// Build the Destroyer manually so we can inject the ordered delete mock.
	d := NewDestroyer(
		m.describe,
		m.terminate,
		m.describeVolumes,
		m.detachVolume,
		del,
		m.describeAddrs,
		m.releaseAddr,
	).WithWaitTerminated(wait)

	err := d.Run(context.Background(), "alice", "default", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !wait.called {
		t.Fatal("WaitInstanceTerminated was not called")
	}
	if !del.called {
		t.Fatal("DeleteVolume was not called")
	}
	if wait.callOrder >= del.callOrder {
		t.Errorf("expected Wait (order=%d) to be called before DeleteVolume (order=%d)",
			wait.callOrder, del.callOrder)
	}
}

// TestDestroyWaiterErrorPropagates verifies that a waiter failure is fatal —
// the error is returned and volume cleanup is skipped.
func TestDestroyWaiterErrorPropagates(t *testing.T) {
	wait := &mockWaitTerminated{
		err: fmt.Errorf("instance did not terminate in time"),
	}

	del := &mockDeleteVolumeOrdered{output: &ec2.DeleteVolumeOutput{}}

	m := newDestroyHappyMocks()
	d := NewDestroyer(
		m.describe,
		m.terminate,
		m.describeVolumes,
		m.detachVolume,
		del,
		m.describeAddrs,
		m.releaseAddr,
	).WithWaitTerminated(wait)

	err := d.Run(context.Background(), "alice", "default", true)
	if err == nil {
		t.Fatal("expected error when waiter fails, got nil")
	}
	if !strings.Contains(err.Error(), "instance did not terminate in time") {
		t.Errorf("error %q does not contain expected message", err.Error())
	}
	if del.called {
		t.Error("DeleteVolume should not be called when waiter fails")
	}
}

// TestDestroyNilWaiterSkipsWait verifies that when no waiter is configured
// (nil), the Destroyer proceeds directly to volume cleanup without blocking.
// This preserves backward compatibility for callers that do not set a waiter.
func TestDestroyNilWaiterSkipsWait(t *testing.T) {
	m := newDestroyHappyMocks()
	// waitTerminated is nil by default in newDestroyHappyMocks.
	d := m.build()

	err := d.Run(context.Background(), "alice", "default", true)
	if err != nil {
		t.Fatalf("unexpected error with nil waiter: %v", err)
	}
	// DeleteVolume should still be called — nil waiter does not block cleanup.
	if !m.deleteVolume.called {
		t.Fatal("DeleteVolume should be called even with nil waiter")
	}
}

// TestDestroyWaiterLoggedOnSuccess verifies that when a logger is set and the
// waiter succeeds, WaitInstanceTerminated appears in the structured log.
func TestDestroyWaiterLoggedOnSuccess(t *testing.T) {
	wait := &mockWaitTerminated{}

	m := newDestroyHappyMocks()
	m.waitTerminated = wait
	d := m.build()

	logger := &mockLogger{}
	d.WithLogger(logger)

	err := d.Run(context.Background(), "alice", "default", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entry, found := logger.findEntry("WaitInstanceTerminated")
	if !found {
		t.Fatal("logger.Log not called with operation=WaitInstanceTerminated")
	}
	if entry.service != "ec2" {
		t.Errorf("service = %q, want %q", entry.service, "ec2")
	}
	if entry.err != nil {
		t.Errorf("err = %v, want nil on success", entry.err)
	}
}

// TestDestroyWaiterLoggedOnFailure verifies that waiter failures are captured
// in the structured log even though they cause the destroy to return an error.
func TestDestroyWaiterLoggedOnFailure(t *testing.T) {
	wait := &mockWaitTerminated{
		err: fmt.Errorf("timeout"),
	}

	m := newDestroyHappyMocks()
	m.waitTerminated = wait
	d := m.build()

	logger := &mockLogger{}
	d.WithLogger(logger)

	err := d.Run(context.Background(), "alice", "default", true)
	if err == nil {
		t.Fatal("expected error")
	}

	entry, found := logger.findEntry("WaitInstanceTerminated")
	if !found {
		t.Fatal("logger.Log not called with operation=WaitInstanceTerminated on failure")
	}
	if entry.err == nil {
		t.Error("expected non-nil err in log entry when waiter fails")
	}
	if !strings.Contains(entry.err.Error(), "timeout") {
		t.Errorf("logged err = %q, want to contain %q", entry.err.Error(), "timeout")
	}
}

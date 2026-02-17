package vm

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/nicholasgasior/mint/internal/tags"
)

// ---------------------------------------------------------------------------
// Mock
// ---------------------------------------------------------------------------

type mockDescribeInstances struct {
	output *ec2.DescribeInstancesOutput
	err    error
	// captured records what filters were passed for assertion.
	captured *ec2.DescribeInstancesInput
}

func (m *mockDescribeInstances) DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	m.captured = params
	return m.output, m.err
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func makeInstance(id, state, publicIP, instanceType, vmName, owner, bootstrap string, launchTime time.Time) ec2types.Instance {
	inst := ec2types.Instance{
		InstanceId:   aws.String(id),
		InstanceType: ec2types.InstanceType(instanceType),
		LaunchTime:   aws.Time(launchTime),
		State: &ec2types.InstanceState{
			Name: ec2types.InstanceStateName(state),
		},
		Tags: []ec2types.Tag{
			{Key: aws.String(tags.TagMint), Value: aws.String("true")},
			{Key: aws.String(tags.TagOwner), Value: aws.String(owner)},
			{Key: aws.String(tags.TagVM), Value: aws.String(vmName)},
			{Key: aws.String(tags.TagName), Value: aws.String("mint/" + owner + "/" + vmName)},
		},
	}
	if publicIP != "" {
		inst.PublicIpAddress = aws.String(publicIP)
	}
	if bootstrap != "" {
		inst.Tags = append(inst.Tags, ec2types.Tag{
			Key: aws.String(tags.TagBootstrap), Value: aws.String(bootstrap),
		})
	}
	return inst
}

func makeReservation(instances ...ec2types.Instance) ec2types.Reservation {
	return ec2types.Reservation{Instances: instances}
}

// ---------------------------------------------------------------------------
// FindVM tests
// ---------------------------------------------------------------------------

func TestFindVM_Found(t *testing.T) {
	now := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	inst := makeInstance("i-abc123", "running", "1.2.3.4", "m6i.xlarge", "default", "alice", "complete", now)
	mock := &mockDescribeInstances{
		output: &ec2.DescribeInstancesOutput{
			Reservations: []ec2types.Reservation{makeReservation(inst)},
		},
	}

	vm, err := FindVM(context.Background(), mock, "alice", "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vm == nil {
		t.Fatal("expected VM, got nil")
	}

	if vm.ID != "i-abc123" {
		t.Errorf("ID = %q, want %q", vm.ID, "i-abc123")
	}
	if vm.Name != "default" {
		t.Errorf("Name = %q, want %q", vm.Name, "default")
	}
	if vm.State != "running" {
		t.Errorf("State = %q, want %q", vm.State, "running")
	}
	if vm.PublicIP != "1.2.3.4" {
		t.Errorf("PublicIP = %q, want %q", vm.PublicIP, "1.2.3.4")
	}
	if vm.InstanceType != "m6i.xlarge" {
		t.Errorf("InstanceType = %q, want %q", vm.InstanceType, "m6i.xlarge")
	}
	if !vm.LaunchTime.Equal(now) {
		t.Errorf("LaunchTime = %v, want %v", vm.LaunchTime, now)
	}
	if vm.BootstrapStatus != "complete" {
		t.Errorf("BootstrapStatus = %q, want %q", vm.BootstrapStatus, "complete")
	}
}

func TestFindVM_NotFound(t *testing.T) {
	mock := &mockDescribeInstances{
		output: &ec2.DescribeInstancesOutput{
			Reservations: []ec2types.Reservation{},
		},
	}

	vm, err := FindVM(context.Background(), mock, "alice", "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vm != nil {
		t.Errorf("expected nil VM, got %+v", vm)
	}
}

func TestFindVM_MultipleMatchesError(t *testing.T) {
	now := time.Now()
	inst1 := makeInstance("i-111", "running", "1.1.1.1", "t3.micro", "default", "alice", "", now)
	inst2 := makeInstance("i-222", "running", "2.2.2.2", "t3.micro", "default", "alice", "", now)

	mock := &mockDescribeInstances{
		output: &ec2.DescribeInstancesOutput{
			Reservations: []ec2types.Reservation{
				makeReservation(inst1),
				makeReservation(inst2),
			},
		},
	}

	vm, err := FindVM(context.Background(), mock, "alice", "default")
	if err == nil {
		t.Fatal("expected error for multiple matches, got nil")
	}
	if vm != nil {
		t.Errorf("expected nil VM on error, got %+v", vm)
	}
	if !containsSubstring(err.Error(), "multiple") {
		t.Errorf("error %q should mention 'multiple'", err.Error())
	}
}

func TestFindVM_APIError(t *testing.T) {
	mock := &mockDescribeInstances{
		err: errors.New("access denied"),
	}

	vm, err := FindVM(context.Background(), mock, "alice", "default")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if vm != nil {
		t.Errorf("expected nil VM on error, got %+v", vm)
	}
	if !containsSubstring(err.Error(), "access denied") {
		t.Errorf("error %q should contain 'access denied'", err.Error())
	}
}

func TestFindVM_FiltersTerminatedInstances(t *testing.T) {
	now := time.Now()
	terminated := makeInstance("i-term", "terminated", "", "t3.micro", "default", "alice", "", now)
	running := makeInstance("i-run", "running", "3.3.3.3", "t3.micro", "default", "alice", "", now)

	mock := &mockDescribeInstances{
		output: &ec2.DescribeInstancesOutput{
			Reservations: []ec2types.Reservation{
				makeReservation(terminated, running),
			},
		},
	}

	vm, err := FindVM(context.Background(), mock, "alice", "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vm == nil {
		t.Fatal("expected VM, got nil")
	}
	if vm.ID != "i-run" {
		t.Errorf("ID = %q, want %q (should skip terminated)", vm.ID, "i-run")
	}
}

func TestFindVM_FiltersShuttingDownInstances(t *testing.T) {
	now := time.Now()
	shuttingDown := makeInstance("i-shut", "shutting-down", "", "t3.micro", "default", "alice", "", now)

	mock := &mockDescribeInstances{
		output: &ec2.DescribeInstancesOutput{
			Reservations: []ec2types.Reservation{
				makeReservation(shuttingDown),
			},
		},
	}

	vm, err := FindVM(context.Background(), mock, "alice", "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vm != nil {
		t.Errorf("expected nil (shutting-down filtered), got %+v", vm)
	}
}

func TestFindVM_NoPublicIP(t *testing.T) {
	now := time.Now()
	inst := makeInstance("i-nopub", "running", "", "t3.micro", "default", "alice", "", now)

	mock := &mockDescribeInstances{
		output: &ec2.DescribeInstancesOutput{
			Reservations: []ec2types.Reservation{makeReservation(inst)},
		},
	}

	vm, err := FindVM(context.Background(), mock, "alice", "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vm.PublicIP != "" {
		t.Errorf("PublicIP = %q, want empty", vm.PublicIP)
	}
}

// ---------------------------------------------------------------------------
// ListVMs tests
// ---------------------------------------------------------------------------

func TestListVMs_Empty(t *testing.T) {
	mock := &mockDescribeInstances{
		output: &ec2.DescribeInstancesOutput{
			Reservations: []ec2types.Reservation{},
		},
	}

	vms, err := ListVMs(context.Background(), mock, "alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vms) != 0 {
		t.Errorf("expected 0 VMs, got %d", len(vms))
	}
}

func TestListVMs_Single(t *testing.T) {
	now := time.Now()
	inst := makeInstance("i-one", "running", "1.1.1.1", "t3.micro", "default", "alice", "complete", now)

	mock := &mockDescribeInstances{
		output: &ec2.DescribeInstancesOutput{
			Reservations: []ec2types.Reservation{makeReservation(inst)},
		},
	}

	vms, err := ListVMs(context.Background(), mock, "alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vms) != 1 {
		t.Fatalf("expected 1 VM, got %d", len(vms))
	}
	if vms[0].ID != "i-one" {
		t.Errorf("ID = %q, want %q", vms[0].ID, "i-one")
	}
}

func TestListVMs_Multiple(t *testing.T) {
	now := time.Now()
	inst1 := makeInstance("i-one", "running", "1.1.1.1", "t3.micro", "default", "alice", "", now)
	inst2 := makeInstance("i-two", "stopped", "", "m6i.xlarge", "dev-box", "alice", "", now)

	mock := &mockDescribeInstances{
		output: &ec2.DescribeInstancesOutput{
			Reservations: []ec2types.Reservation{
				makeReservation(inst1),
				makeReservation(inst2),
			},
		},
	}

	vms, err := ListVMs(context.Background(), mock, "alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vms) != 2 {
		t.Fatalf("expected 2 VMs, got %d", len(vms))
	}
}

func TestListVMs_FiltersTerminated(t *testing.T) {
	now := time.Now()
	running := makeInstance("i-run", "running", "1.1.1.1", "t3.micro", "default", "alice", "", now)
	terminated := makeInstance("i-term", "terminated", "", "t3.micro", "old-vm", "alice", "", now)
	shuttingDown := makeInstance("i-shut", "shutting-down", "", "t3.micro", "dying-vm", "alice", "", now)

	mock := &mockDescribeInstances{
		output: &ec2.DescribeInstancesOutput{
			Reservations: []ec2types.Reservation{
				makeReservation(running, terminated, shuttingDown),
			},
		},
	}

	vms, err := ListVMs(context.Background(), mock, "alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vms) != 1 {
		t.Fatalf("expected 1 VM (filtered terminated/shutting-down), got %d", len(vms))
	}
	if vms[0].ID != "i-run" {
		t.Errorf("ID = %q, want %q", vms[0].ID, "i-run")
	}
}

func TestListVMs_APIError(t *testing.T) {
	mock := &mockDescribeInstances{
		err: errors.New("throttled"),
	}

	vms, err := ListVMs(context.Background(), mock, "alice")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if vms != nil {
		t.Errorf("expected nil VMs on error, got %v", vms)
	}
}

func TestVMTagsParsed(t *testing.T) {
	now := time.Now()
	inst := makeInstance("i-tagged", "running", "", "t3.micro", "default", "alice", "complete", now)

	mock := &mockDescribeInstances{
		output: &ec2.DescribeInstancesOutput{
			Reservations: []ec2types.Reservation{makeReservation(inst)},
		},
	}

	vm, err := FindVM(context.Background(), mock, "alice", "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Tags map should contain all tags from the instance.
	if vm.Tags[tags.TagMint] != "true" {
		t.Errorf("Tags[%q] = %q, want %q", tags.TagMint, vm.Tags[tags.TagMint], "true")
	}
	if vm.Tags[tags.TagOwner] != "alice" {
		t.Errorf("Tags[%q] = %q, want %q", tags.TagOwner, vm.Tags[tags.TagOwner], "alice")
	}
	if vm.Tags[tags.TagName] != "mint/alice/default" {
		t.Errorf("Tags[%q] = %q, want %q", tags.TagName, vm.Tags[tags.TagName], "mint/alice/default")
	}
}

// ---------------------------------------------------------------------------
// Volume tag parsing tests
// ---------------------------------------------------------------------------

func TestVMParseVolumeTagsFromInstance(t *testing.T) {
	now := time.Now()
	inst := makeInstance("i-vol", "running", "1.2.3.4", "m6i.xlarge", "default", "alice", "complete", now)
	inst.Tags = append(inst.Tags,
		ec2types.Tag{Key: aws.String(tags.TagRootVolumeGB), Value: aws.String("200")},
		ec2types.Tag{Key: aws.String(tags.TagProjectVolumeGB), Value: aws.String("50")},
	)

	mock := &mockDescribeInstances{
		output: &ec2.DescribeInstancesOutput{
			Reservations: []ec2types.Reservation{makeReservation(inst)},
		},
	}

	vm, err := FindVM(context.Background(), mock, "alice", "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vm.RootVolumeGB != 200 {
		t.Errorf("RootVolumeGB = %d, want 200", vm.RootVolumeGB)
	}
	if vm.ProjectVolumeGB != 50 {
		t.Errorf("ProjectVolumeGB = %d, want 50", vm.ProjectVolumeGB)
	}
}

func TestVMParseVolumeTagsMissing(t *testing.T) {
	now := time.Now()
	inst := makeInstance("i-novol", "running", "1.2.3.4", "m6i.xlarge", "default", "alice", "complete", now)

	mock := &mockDescribeInstances{
		output: &ec2.DescribeInstancesOutput{
			Reservations: []ec2types.Reservation{makeReservation(inst)},
		},
	}

	vm, err := FindVM(context.Background(), mock, "alice", "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vm.RootVolumeGB != 0 {
		t.Errorf("RootVolumeGB = %d, want 0", vm.RootVolumeGB)
	}
	if vm.ProjectVolumeGB != 0 {
		t.Errorf("ProjectVolumeGB = %d, want 0", vm.ProjectVolumeGB)
	}
}

func TestVMParseVolumeTagsInvalidIgnored(t *testing.T) {
	now := time.Now()
	inst := makeInstance("i-bad", "running", "1.2.3.4", "m6i.xlarge", "default", "alice", "complete", now)
	inst.Tags = append(inst.Tags,
		ec2types.Tag{Key: aws.String(tags.TagRootVolumeGB), Value: aws.String("not-a-number")},
		ec2types.Tag{Key: aws.String(tags.TagProjectVolumeGB), Value: aws.String("")},
	)

	mock := &mockDescribeInstances{
		output: &ec2.DescribeInstancesOutput{
			Reservations: []ec2types.Reservation{makeReservation(inst)},
		},
	}

	vm, err := FindVM(context.Background(), mock, "alice", "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vm.RootVolumeGB != 0 {
		t.Errorf("RootVolumeGB = %d, want 0 (invalid tag should be ignored)", vm.RootVolumeGB)
	}
	if vm.ProjectVolumeGB != 0 {
		t.Errorf("ProjectVolumeGB = %d, want 0 (empty tag should be ignored)", vm.ProjectVolumeGB)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

package provision

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/SpiceLabsHQ/Mint/internal/tags"
)

// ---------------------------------------------------------------------------
// Inline mocks for bootstrap poll
// ---------------------------------------------------------------------------

type mockPollDescribeInstances struct {
	// responses is a queue of responses; each call pops the first entry.
	responses []describeResponse
	calls     int
}

type describeResponse struct {
	output *ec2.DescribeInstancesOutput
	err    error
}

func (m *mockPollDescribeInstances) DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	idx := m.calls
	m.calls++
	if idx < len(m.responses) {
		return m.responses[idx].output, m.responses[idx].err
	}
	// Default: return pending if exhausted
	return m.responses[len(m.responses)-1].output, m.responses[len(m.responses)-1].err
}

type mockPollStopInstances struct {
	called bool
	input  *ec2.StopInstancesInput
	err    error
}

func (m *mockPollStopInstances) StopInstances(ctx context.Context, params *ec2.StopInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StopInstancesOutput, error) {
	m.called = true
	m.input = params
	return &ec2.StopInstancesOutput{}, m.err
}

type mockPollTerminateInstances struct {
	called bool
	input  *ec2.TerminateInstancesInput
	err    error
}

func (m *mockPollTerminateInstances) TerminateInstances(ctx context.Context, params *ec2.TerminateInstancesInput, optFns ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error) {
	m.called = true
	m.input = params
	return &ec2.TerminateInstancesOutput{}, m.err
}

type mockPollCreateTags struct {
	called bool
	input  *ec2.CreateTagsInput
	err    error
}

func (m *mockPollCreateTags) CreateTags(ctx context.Context, params *ec2.CreateTagsInput, optFns ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error) {
	m.called = true
	m.input = params
	return &ec2.CreateTagsOutput{}, m.err
}

// ---------------------------------------------------------------------------
// Helper: build a VM DescribeInstances response with a given bootstrap status
// ---------------------------------------------------------------------------

func vmResponse(instanceID, bootstrapStatus string) *ec2.DescribeInstancesOutput {
	return &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{
			Instances: []ec2types.Instance{{
				InstanceId:   aws.String(instanceID),
				InstanceType: ec2types.InstanceTypeM6iXlarge,
				State: &ec2types.InstanceState{
					Name: ec2types.InstanceStateNameRunning,
				},
				Tags: []ec2types.Tag{
					{Key: aws.String(tags.TagMint), Value: aws.String("true")},
					{Key: aws.String(tags.TagVM), Value: aws.String("default")},
					{Key: aws.String(tags.TagOwner), Value: aws.String("alice")},
					{Key: aws.String(tags.TagBootstrap), Value: aws.String(bootstrapStatus)},
				},
			}},
		}},
	}
}

// fastPollConfig returns a PollConfig with very short intervals for testing.
func fastPollConfig() PollConfig {
	return PollConfig{
		Interval: 1 * time.Millisecond,
		Timeout:  5 * time.Millisecond,
	}
}

// vmResponseWithPhase builds a DescribeInstances response that includes both
// a bootstrap status tag and an optional failure-phase tag. Pass an empty
// string for phase to omit the tag (simulates older bootstrap scripts or
// successful bootstrap without phase information).
func vmResponseWithPhase(instanceID, bootstrapStatus, phase string) *ec2.DescribeInstancesOutput {
	instanceTags := []ec2types.Tag{
		{Key: aws.String(tags.TagMint), Value: aws.String("true")},
		{Key: aws.String(tags.TagVM), Value: aws.String("default")},
		{Key: aws.String(tags.TagOwner), Value: aws.String("alice")},
		{Key: aws.String(tags.TagBootstrap), Value: aws.String(bootstrapStatus)},
	}
	if phase != "" {
		instanceTags = append(instanceTags, ec2types.Tag{
			Key:   aws.String(tags.TagBootstrapFailurePhase),
			Value: aws.String(phase),
		})
	}
	return &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{
			Instances: []ec2types.Instance{{
				InstanceId:   aws.String(instanceID),
				InstanceType: ec2types.InstanceTypeM6iXlarge,
				State: &ec2types.InstanceState{
					Name: ec2types.InstanceStateNameRunning,
				},
				Tags: instanceTags,
			}},
		}},
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestBootstrapPoller(t *testing.T) {
	// interactiveTTY simulates a TTY-connected stdin for tests that exercise
	// the interactive prompt path.
	interactiveTTY := func() bool { return true }

	tests := []struct {
		name               string
		responses          []describeResponse
		userInput          string
		pollConfig         PollConfig
		isTerminal         func() bool // nil means use real default (never reaches handleTimeout in these cases)
		wantErr            bool
		wantErrContain     string
		wantOutputContain  string
		wantStopCalled     bool
		wantTermCalled     bool
		wantTagCalled      bool
		stopErr            error
		termErr            error
		tagErr             error
	}{
		{
			name: "complete on first check",
			responses: []describeResponse{
				{output: vmResponse("i-abc123", tags.BootstrapComplete)},
			},
			pollConfig:        fastPollConfig(),
			wantOutputContain: "Bootstrap complete",
		},
		{
			name: "complete after 3 polls",
			responses: []describeResponse{
				{output: vmResponse("i-abc123", tags.BootstrapPending)},
				{output: vmResponse("i-abc123", tags.BootstrapPending)},
				{output: vmResponse("i-abc123", tags.BootstrapComplete)},
			},
			pollConfig:        PollConfig{Interval: 1 * time.Millisecond, Timeout: 50 * time.Millisecond},
			wantOutputContain: "Bootstrap complete",
		},
		{
			name: "timeout triggers prompt - option 1 stop",
			responses: []describeResponse{
				{output: vmResponse("i-abc123", tags.BootstrapPending)},
			},
			pollConfig:        fastPollConfig(),
			isTerminal:        interactiveTTY,
			userInput:         "1\n",
			wantStopCalled:    true,
			wantOutputContain: "Stopping instance",
		},
		{
			name: "timeout triggers prompt - option 2 terminate and tag failed",
			responses: []describeResponse{
				{output: vmResponse("i-abc123", tags.BootstrapPending)},
			},
			pollConfig:        fastPollConfig(),
			isTerminal:        interactiveTTY,
			userInput:         "2\n",
			wantTermCalled:    true,
			wantTagCalled:     true,
			wantOutputContain: "Terminating instance",
		},
		{
			name: "timeout triggers prompt - option 3 leave running",
			responses: []describeResponse{
				{output: vmResponse("i-abc123", tags.BootstrapPending)},
			},
			pollConfig:        fastPollConfig(),
			isTerminal:        interactiveTTY,
			userInput:         "3\n",
			wantOutputContain: "Leaving instance running",
		},
		{
			name: "timeout stop error",
			responses: []describeResponse{
				{output: vmResponse("i-abc123", tags.BootstrapPending)},
			},
			pollConfig:     fastPollConfig(),
			isTerminal:     interactiveTTY,
			userInput:      "1\n",
			stopErr:        fmt.Errorf("stop denied"),
			wantErr:        true,
			wantErrContain: "stop denied",
			wantStopCalled: true,
		},
		{
			name: "timeout terminate error",
			responses: []describeResponse{
				{output: vmResponse("i-abc123", tags.BootstrapPending)},
			},
			pollConfig:     fastPollConfig(),
			isTerminal:     interactiveTTY,
			userInput:      "2\n",
			termErr:        fmt.Errorf("terminate denied"),
			wantErr:        true,
			wantErrContain: "terminate denied",
			wantTermCalled: true,
		},
		{
			name: "timeout tag error after terminate",
			responses: []describeResponse{
				{output: vmResponse("i-abc123", tags.BootstrapPending)},
			},
			pollConfig:     fastPollConfig(),
			isTerminal:     interactiveTTY,
			userInput:      "2\n",
			tagErr:         fmt.Errorf("tag denied"),
			wantErr:        true,
			wantErrContain: "tag denied",
			wantTermCalled: true,
			wantTagCalled:  true,
		},
		{
			name: "context cancellation",
			responses: []describeResponse{
				{output: vmResponse("i-abc123", tags.BootstrapPending)},
			},
			// We will use a pre-cancelled context, so pollConfig doesn't matter much.
			pollConfig:     PollConfig{Interval: 1 * time.Second, Timeout: 1 * time.Minute},
			wantErr:        true,
			wantErrContain: "context",
		},
		{
			name: "describe error during poll",
			responses: []describeResponse{
				{err: fmt.Errorf("describe throttled")},
			},
			pollConfig:        fastPollConfig(),
			isTerminal:        interactiveTTY,
			userInput:         "3\n", // timeout prompt will appear since we don't get complete
			wantOutputContain: "Leaving instance running",
		},
		{
			name: "bootstrap already failed tag returns immediately",
			responses: []describeResponse{
				{output: vmResponse("i-abc123", tags.BootstrapFailed)},
			},
			pollConfig:     fastPollConfig(),
			wantErr:        true,
			wantErrContain: "bootstrap failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			descMock := &mockPollDescribeInstances{responses: tt.responses}
			stopMock := &mockPollStopInstances{err: tt.stopErr}
			termMock := &mockPollTerminateInstances{err: tt.termErr}
			tagMock := &mockPollCreateTags{err: tt.tagErr}

			var output bytes.Buffer
			input := bytes.NewBufferString(tt.userInput)

			poller := NewBootstrapPoller(descMock, stopMock, termMock, tagMock, &output, input)
			poller.Config = tt.pollConfig
			if tt.isTerminal != nil {
				poller.isTerminal = tt.isTerminal
			}

			var ctx context.Context
			var cancel context.CancelFunc

			if tt.name == "context cancellation" {
				ctx, cancel = context.WithCancel(context.Background())
				cancel() // cancel immediately
			} else {
				ctx = context.Background()
				cancel = func() {}
			}
			defer cancel()

			err := poller.Poll(ctx, "alice", "default", "i-abc123")

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.wantErrContain != "" && !strings.Contains(err.Error(), tt.wantErrContain) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.wantErrContain)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}

			if tt.wantOutputContain != "" {
				if !strings.Contains(output.String(), tt.wantOutputContain) {
					t.Errorf("output %q does not contain %q", output.String(), tt.wantOutputContain)
				}
			}

			if tt.wantStopCalled != stopMock.called {
				t.Errorf("StopInstances called = %v, want %v", stopMock.called, tt.wantStopCalled)
			}
			if tt.wantTermCalled != termMock.called {
				t.Errorf("TerminateInstances called = %v, want %v", termMock.called, tt.wantTermCalled)
			}
			if tt.wantTagCalled != tagMock.called {
				t.Errorf("CreateTags called = %v, want %v", tagMock.called, tt.wantTagCalled)
			}

			// Verify correct instance ID was passed to stop/terminate.
			if stopMock.called && stopMock.input != nil {
				if len(stopMock.input.InstanceIds) != 1 || stopMock.input.InstanceIds[0] != "i-abc123" {
					t.Errorf("StopInstances instanceID = %v, want [i-abc123]", stopMock.input.InstanceIds)
				}
			}
			if termMock.called && termMock.input != nil {
				if len(termMock.input.InstanceIds) != 1 || termMock.input.InstanceIds[0] != "i-abc123" {
					t.Errorf("TerminateInstances instanceID = %v, want [i-abc123]", termMock.input.InstanceIds)
				}
			}

			// Verify tag contains bootstrap=failed when tagging.
			if tagMock.called && tagMock.input != nil {
				foundFailedTag := false
				for _, tag := range tagMock.input.Tags {
					if aws.ToString(tag.Key) == tags.TagBootstrap && aws.ToString(tag.Value) == tags.BootstrapFailed {
						foundFailedTag = true
					}
				}
				if !foundFailedTag {
					t.Error("CreateTags should include mint:bootstrap=failed tag")
				}
				if len(tagMock.input.Resources) != 1 || tagMock.input.Resources[0] != "i-abc123" {
					t.Errorf("CreateTags resources = %v, want [i-abc123]", tagMock.input.Resources)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Non-interactive (non-TTY) timeout tests
// ---------------------------------------------------------------------------

// TestHandleTimeoutNonInteractive verifies that when stdin is not a terminal,
// handleTimeout skips the prompt, emits a clear message, and returns a non-nil
// error so that mint up exits non-zero when bootstrap times out in non-TTY mode.
func TestHandleTimeoutNonInteractive(t *testing.T) {
	descMock := &mockPollDescribeInstances{
		responses: []describeResponse{
			{output: vmResponse("i-abc123", tags.BootstrapPending)},
		},
	}
	stopMock := &mockPollStopInstances{}
	termMock := &mockPollTerminateInstances{}
	tagMock := &mockPollCreateTags{}

	var output bytes.Buffer
	// bytes.NewReader simulates EOF / non-TTY stdin (no input available).
	input := bytes.NewReader([]byte{})

	poller := NewBootstrapPoller(descMock, stopMock, termMock, tagMock, &output, input)
	poller.Config = fastPollConfig()
	// Force non-interactive mode: isTerminal returns false.
	poller.isTerminal = func() bool { return false }

	err := poller.Poll(context.Background(), "alice", "default", "i-abc123")
	if err == nil {
		t.Fatal("expected non-nil error in non-interactive timeout, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error %q does not contain %q", err.Error(), "timed out")
	}
	if !strings.Contains(err.Error(), "i-abc123") {
		t.Errorf("error %q does not contain instance ID %q", err.Error(), "i-abc123")
	}

	got := output.String()
	wantMsg := fmt.Sprintf("Bootstrap timed out. Instance %s left running — SSH in or run 'mint doctor' to investigate.", "i-abc123")
	if !strings.Contains(got, wantMsg) {
		t.Errorf("output %q does not contain expected non-interactive message %q", got, wantMsg)
	}

	// No AWS actions should have been taken.
	if stopMock.called {
		t.Error("StopInstances should not be called in non-interactive mode")
	}
	if termMock.called {
		t.Error("TerminateInstances should not be called in non-interactive mode")
	}
	if tagMock.called {
		t.Error("CreateTags should not be called in non-interactive mode")
	}
}

// TestHandleTimeoutInteractivePathUnchanged confirms the existing interactive
// prompt still works correctly when isTerminal returns true.
func TestHandleTimeoutInteractivePathUnchanged(t *testing.T) {
	tests := []struct {
		name              string
		userInput         string
		wantOutputContain string
		wantStopCalled    bool
		wantTermCalled    bool
	}{
		{
			name:              "interactive option 1 stop",
			userInput:         "1\n",
			wantOutputContain: "Stopping instance",
			wantStopCalled:    true,
		},
		{
			name:              "interactive option 3 leave running",
			userInput:         "3\n",
			wantOutputContain: "Leaving instance running",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			descMock := &mockPollDescribeInstances{
				responses: []describeResponse{
					{output: vmResponse("i-abc123", tags.BootstrapPending)},
				},
			}
			stopMock := &mockPollStopInstances{}
			termMock := &mockPollTerminateInstances{}
			tagMock := &mockPollCreateTags{}

			var output bytes.Buffer
			input := bytes.NewBufferString(tt.userInput)

			poller := NewBootstrapPoller(descMock, stopMock, termMock, tagMock, &output, input)
			poller.Config = fastPollConfig()
			// Force interactive mode: isTerminal returns true.
			poller.isTerminal = func() bool { return true }

			err := poller.Poll(context.Background(), "alice", "default", "i-abc123")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantOutputContain != "" && !strings.Contains(output.String(), tt.wantOutputContain) {
				t.Errorf("output %q does not contain %q", output.String(), tt.wantOutputContain)
			}
			if tt.wantStopCalled != stopMock.called {
				t.Errorf("StopInstances called = %v, want %v", stopMock.called, tt.wantStopCalled)
			}
		})
	}
}

func TestBootstrapPollerDefaultConfig(t *testing.T) {
	poller := NewBootstrapPoller(
		&mockPollDescribeInstances{responses: []describeResponse{{output: vmResponse("i-abc123", tags.BootstrapComplete)}}},
		&mockPollStopInstances{},
		&mockPollTerminateInstances{},
		&mockPollCreateTags{},
		&bytes.Buffer{},
		&bytes.Buffer{},
	)

	if poller.Config.Interval != DefaultPollInterval {
		t.Errorf("default Interval = %v, want %v", poller.Config.Interval, DefaultPollInterval)
	}
	if poller.Config.Timeout != DefaultPollTimeout {
		t.Errorf("default Timeout = %v, want %v", poller.Config.Timeout, DefaultPollTimeout)
	}
}

func TestBootstrapPollerProgressOutput(t *testing.T) {
	descMock := &mockPollDescribeInstances{
		responses: []describeResponse{
			{output: vmResponse("i-abc123", tags.BootstrapPending)},
			{output: vmResponse("i-abc123", tags.BootstrapPending)},
			{output: vmResponse("i-abc123", tags.BootstrapComplete)},
		},
	}

	var output bytes.Buffer
	poller := NewBootstrapPoller(descMock, &mockPollStopInstances{}, &mockPollTerminateInstances{}, &mockPollCreateTags{}, &output, &bytes.Buffer{})
	poller.Config = PollConfig{Interval: 1 * time.Millisecond, Timeout: 50 * time.Millisecond}

	err := poller.Poll(context.Background(), "alice", "default", "i-abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Output should contain progress messages with timing.
	if !strings.Contains(output.String(), "Waiting for bootstrap") {
		t.Errorf("output should contain progress messages, got: %q", output.String())
	}
}

// ---------------------------------------------------------------------------
// Bootstrap failure phase tag tests
// ---------------------------------------------------------------------------

// TestBootstrapFailedWithPhase asserts that when mint:bootstrap=failed and
// mint:bootstrap-failure-phase is present, Poll returns an error message that
// includes the phase name.
func TestBootstrapFailedWithPhase(t *testing.T) {
	phases := []string{
		"packages",
		"docker",
		"efs-mount",
		"systemd-units",
		"drift-check",
		"user-script",
	}

	for _, phase := range phases {
		phase := phase
		t.Run("phase_"+phase, func(t *testing.T) {
			descMock := &mockPollDescribeInstances{
				responses: []describeResponse{
					{output: vmResponseWithPhase("i-abc123", tags.BootstrapFailed, phase)},
				},
			}

			var output bytes.Buffer
			poller := NewBootstrapPoller(
				descMock,
				&mockPollStopInstances{},
				&mockPollTerminateInstances{},
				&mockPollCreateTags{},
				&output,
				&bytes.Buffer{},
			)
			poller.Config = fastPollConfig()

			err := poller.Poll(context.Background(), "alice", "default", "i-abc123")
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), phase) {
				t.Errorf("error %q does not contain phase %q", err.Error(), phase)
			}
			if !strings.Contains(err.Error(), "i-abc123") {
				t.Errorf("error %q does not contain instance ID", err.Error())
			}
		})
	}
}

// TestBootstrapFailedWithoutPhase asserts that when mint:bootstrap=failed but
// mint:bootstrap-failure-phase is absent (e.g. older bootstrap scripts), Poll
// still returns a clear error without panicking or producing an empty message.
func TestBootstrapFailedWithoutPhase(t *testing.T) {
	descMock := &mockPollDescribeInstances{
		responses: []describeResponse{
			// vmResponse does not include the failure-phase tag.
			{output: vmResponse("i-abc123", tags.BootstrapFailed)},
		},
	}

	var output bytes.Buffer
	poller := NewBootstrapPoller(
		descMock,
		&mockPollStopInstances{},
		&mockPollTerminateInstances{},
		&mockPollCreateTags{},
		&output,
		&bytes.Buffer{},
	)
	poller.Config = fastPollConfig()

	err := poller.Poll(context.Background(), "alice", "default", "i-abc123")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// Must mention the instance and "bootstrap failed"; must not be empty.
	if !strings.Contains(err.Error(), "bootstrap failed") {
		t.Errorf("error %q does not contain %q", err.Error(), "bootstrap failed")
	}
	if !strings.Contains(err.Error(), "i-abc123") {
		t.Errorf("error %q does not contain instance ID", err.Error())
	}
}

// TestBootstrapFailedPhaseInTickerLoop asserts phase is included in the error
// when bootstrap=failed is detected during a polling tick (not only on the
// initial check).
func TestBootstrapFailedPhaseInTickerLoop(t *testing.T) {
	descMock := &mockPollDescribeInstances{
		responses: []describeResponse{
			{output: vmResponse("i-abc123", tags.BootstrapPending)},
			{output: vmResponseWithPhase("i-abc123", tags.BootstrapFailed, "efs-mount")},
		},
	}

	var output bytes.Buffer
	poller := NewBootstrapPoller(
		descMock,
		&mockPollStopInstances{},
		&mockPollTerminateInstances{},
		&mockPollCreateTags{},
		&output,
		&bytes.Buffer{},
	)
	poller.Config = PollConfig{Interval: 1 * time.Millisecond, Timeout: 50 * time.Millisecond}

	err := poller.Poll(context.Background(), "alice", "default", "i-abc123")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "efs-mount") {
		t.Errorf("error %q does not contain phase %q", err.Error(), "efs-mount")
	}
}

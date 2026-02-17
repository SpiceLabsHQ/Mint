package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/nicholasgasior/mint/internal/cli"
	mintaws "github.com/nicholasgasior/mint/internal/aws"
	"github.com/nicholasgasior/mint/internal/provision"
	"github.com/spf13/cobra"
)

// ---------------------------------------------------------------------------
// Inline mocks for cmd-level up tests
// ---------------------------------------------------------------------------

type stubUpDescribeInstances struct {
	output *ec2.DescribeInstancesOutput
	err    error
}

func (s *stubUpDescribeInstances) DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return s.output, s.err
}

type stubUpStartInstances struct {
	output *ec2.StartInstancesOutput
	err    error
}

func (s *stubUpStartInstances) StartInstances(ctx context.Context, params *ec2.StartInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error) {
	return s.output, s.err
}

type stubUpRunInstances struct {
	output *ec2.RunInstancesOutput
	err    error
}

func (s *stubUpRunInstances) RunInstances(ctx context.Context, params *ec2.RunInstancesInput, optFns ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error) {
	return s.output, s.err
}

type stubUpDescribeSGs struct {
	outputs []*ec2.DescribeSecurityGroupsOutput
	errs    []error
	calls   int
}

func (s *stubUpDescribeSGs) DescribeSecurityGroups(ctx context.Context, params *ec2.DescribeSecurityGroupsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error) {
	idx := s.calls
	s.calls++
	if idx < len(s.outputs) {
		var err error
		if idx < len(s.errs) {
			err = s.errs[idx]
		}
		return s.outputs[idx], err
	}
	return nil, fmt.Errorf("unexpected call %d", idx)
}

type stubUpDescribeSubnets struct {
	output *ec2.DescribeSubnetsOutput
	err    error
}

func (s *stubUpDescribeSubnets) DescribeSubnets(ctx context.Context, params *ec2.DescribeSubnetsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error) {
	return s.output, s.err
}

type stubUpCreateVolume struct {
	output *ec2.CreateVolumeOutput
	err    error
}

func (s *stubUpCreateVolume) CreateVolume(ctx context.Context, params *ec2.CreateVolumeInput, optFns ...func(*ec2.Options)) (*ec2.CreateVolumeOutput, error) {
	return s.output, s.err
}

type stubUpAttachVolume struct {
	output *ec2.AttachVolumeOutput
	err    error
}

func (s *stubUpAttachVolume) AttachVolume(ctx context.Context, params *ec2.AttachVolumeInput, optFns ...func(*ec2.Options)) (*ec2.AttachVolumeOutput, error) {
	return s.output, s.err
}

type stubUpAllocateAddress struct {
	output *ec2.AllocateAddressOutput
	err    error
}

func (s *stubUpAllocateAddress) AllocateAddress(ctx context.Context, params *ec2.AllocateAddressInput, optFns ...func(*ec2.Options)) (*ec2.AllocateAddressOutput, error) {
	return s.output, s.err
}

type stubUpAssociateAddress struct {
	output *ec2.AssociateAddressOutput
	err    error
}

func (s *stubUpAssociateAddress) AssociateAddress(ctx context.Context, params *ec2.AssociateAddressInput, optFns ...func(*ec2.Options)) (*ec2.AssociateAddressOutput, error) {
	return s.output, s.err
}

type stubUpDescribeAddresses struct {
	output *ec2.DescribeAddressesOutput
	err    error
}

func (s *stubUpDescribeAddresses) DescribeAddresses(ctx context.Context, params *ec2.DescribeAddressesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeAddressesOutput, error) {
	return s.output, s.err
}

type stubUpCreateTags struct {
	output *ec2.CreateTagsOutput
	err    error
}

func (s *stubUpCreateTags) CreateTags(ctx context.Context, params *ec2.CreateTagsInput, optFns ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error) {
	return s.output, s.err
}

type stubUpGetParameter struct {
	output *ssm.GetParameterOutput
	err    error
}

func (s *stubUpGetParameter) GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	return s.output, s.err
}

// ---------------------------------------------------------------------------
// Helper: build a test Provisioner with happy-path stubs
// ---------------------------------------------------------------------------

func newTestProvisioner() *provision.Provisioner {
	p := provision.NewProvisioner(
		&stubUpDescribeInstances{output: &ec2.DescribeInstancesOutput{}},
		&stubUpStartInstances{output: &ec2.StartInstancesOutput{}},
		&stubUpRunInstances{output: &ec2.RunInstancesOutput{
			Instances: []ec2types.Instance{
				{InstanceId: aws.String("i-test123")},
			},
		}},
		&stubUpDescribeSGs{
			outputs: []*ec2.DescribeSecurityGroupsOutput{
				{SecurityGroups: []ec2types.SecurityGroup{{GroupId: aws.String("sg-user")}}},
				{SecurityGroups: []ec2types.SecurityGroup{{GroupId: aws.String("sg-admin")}}},
			},
			errs: []error{nil, nil},
		},
		&stubUpDescribeSubnets{output: &ec2.DescribeSubnetsOutput{
			Subnets: []ec2types.Subnet{{
				SubnetId:         aws.String("subnet-test"),
				AvailabilityZone: aws.String("us-east-1a"),
			}},
		}},
		&stubUpCreateVolume{output: &ec2.CreateVolumeOutput{
			VolumeId: aws.String("vol-test"),
		}},
		&stubUpAttachVolume{output: &ec2.AttachVolumeOutput{}},
		&stubUpAllocateAddress{output: &ec2.AllocateAddressOutput{
			AllocationId: aws.String("eipalloc-test"),
			PublicIp:     aws.String("54.10.20.30"),
		}},
		&stubUpAssociateAddress{output: &ec2.AssociateAddressOutput{}},
		&stubUpDescribeAddresses{output: &ec2.DescribeAddressesOutput{
			Addresses: []ec2types.Address{},
		}},
		&stubUpCreateTags{output: &ec2.CreateTagsOutput{}},
		&stubUpGetParameter{},
	)
	p.WithBootstrapVerifier(func(content []byte) error { return nil })
	p.WithAMIResolver(func(ctx context.Context, client mintaws.GetParameterAPI) (string, error) {
		return "ami-test", nil
	})
	return p
}

func newTestUpDeps() *upDeps {
	return &upDeps{
		provisioner:     newTestProvisioner(),
		owner:           "testuser",
		ownerARN:        "arn:aws:iam::123:user/testuser",
		bootstrapScript: []byte("#!/bin/bash\necho test"),
		instanceType:    "m6i.xlarge",
		volumeSize:      50,
	}
}

// ---------------------------------------------------------------------------
// Tests: cmd-level up command
// ---------------------------------------------------------------------------

func TestUpCommandHumanOutput(t *testing.T) {
	buf := new(bytes.Buffer)
	cmd := &cobra.Command{}
	cmd.SetOut(buf)

	cliCtx := &cli.CLIContext{VM: "default"}
	ctx := cli.WithContext(context.Background(), cliCtx)
	cmd.SetContext(ctx)

	deps := newTestUpDeps()
	err := upWithProvisioner(ctx, cmd, cliCtx, deps, "default")
	if err != nil {
		t.Fatalf("upWithProvisioner error: %v", err)
	}

	output := buf.String()

	expectations := []string{
		"i-test123",
		"54.10.20.30",
		"vol-test",
		"eipalloc-test",
		"Bootstrap complete",
	}

	for _, exp := range expectations {
		if !strings.Contains(output, exp) {
			t.Errorf("output missing %q, got:\n%s", exp, output)
		}
	}
}

func TestUpCommandJSONOutput(t *testing.T) {
	buf := new(bytes.Buffer)
	cmd := &cobra.Command{}
	cmd.SetOut(buf)

	cliCtx := &cli.CLIContext{JSON: true, VM: "default"}
	ctx := cli.WithContext(context.Background(), cliCtx)
	cmd.SetContext(ctx)

	deps := newTestUpDeps()
	err := upWithProvisioner(ctx, cmd, cliCtx, deps, "default")
	if err != nil {
		t.Fatalf("upWithProvisioner error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v\nOutput: %s", err, buf.String())
	}

	expectedKeys := []string{"instance_id", "public_ip", "volume_id", "allocation_id", "restarted"}
	for _, key := range expectedKeys {
		if _, ok := result[key]; !ok {
			t.Errorf("JSON output missing key %q", key)
		}
	}

	if result["instance_id"] != "i-test123" {
		t.Errorf("instance_id = %v, want i-test123", result["instance_id"])
	}
	if result["restarted"] != false {
		t.Errorf("restarted = %v, want false", result["restarted"])
	}
}

func TestUpCommandRestartedOutput(t *testing.T) {
	buf := new(bytes.Buffer)
	cmd := &cobra.Command{}
	cmd.SetOut(buf)

	cliCtx := &cli.CLIContext{VM: "default"}
	ctx := cli.WithContext(context.Background(), cliCtx)
	cmd.SetContext(ctx)

	// Build a provisioner that finds a stopped VM.
	p := provision.NewProvisioner(
		&stubUpDescribeInstances{output: &ec2.DescribeInstancesOutput{
			Reservations: []ec2types.Reservation{{
				Instances: []ec2types.Instance{{
					InstanceId:      aws.String("i-stopped1"),
					InstanceType:    ec2types.InstanceTypeM6iXlarge,
					PublicIpAddress: aws.String("54.0.0.1"),
					State: &ec2types.InstanceState{
						Name: ec2types.InstanceStateNameStopped,
					},
					Tags: []ec2types.Tag{
						{Key: aws.String("mint:vm"), Value: aws.String("default")},
						{Key: aws.String("mint:owner"), Value: aws.String("testuser")},
					},
				}},
			}},
		}},
		&stubUpStartInstances{output: &ec2.StartInstancesOutput{}},
		&stubUpRunInstances{output: &ec2.RunInstancesOutput{}},
		&stubUpDescribeSGs{outputs: []*ec2.DescribeSecurityGroupsOutput{}, errs: []error{}},
		&stubUpDescribeSubnets{output: &ec2.DescribeSubnetsOutput{}},
		&stubUpCreateVolume{output: &ec2.CreateVolumeOutput{}},
		&stubUpAttachVolume{output: &ec2.AttachVolumeOutput{}},
		&stubUpAllocateAddress{output: &ec2.AllocateAddressOutput{}},
		&stubUpAssociateAddress{output: &ec2.AssociateAddressOutput{}},
		&stubUpDescribeAddresses{output: &ec2.DescribeAddressesOutput{}},
		&stubUpCreateTags{output: &ec2.CreateTagsOutput{}},
		&stubUpGetParameter{},
	)
	p.WithBootstrapVerifier(func(content []byte) error { return nil })
	p.WithAMIResolver(func(ctx context.Context, client mintaws.GetParameterAPI) (string, error) {
		return "ami-test", nil
	})

	deps := &upDeps{
		provisioner:     p,
		owner:           "testuser",
		ownerARN:        "arn:aws:iam::123:user/testuser",
		bootstrapScript: []byte("#!/bin/bash"),
		instanceType:    "m6i.xlarge",
		volumeSize:      50,
	}

	err := upWithProvisioner(ctx, cmd, cliCtx, deps, "default")
	if err != nil {
		t.Fatalf("upWithProvisioner error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "restarted") {
		t.Errorf("output should contain 'restarted', got:\n%s", output)
	}
	if !strings.Contains(output, "i-stopped1") {
		t.Errorf("output should contain instance ID, got:\n%s", output)
	}
}

func TestUpCommandError(t *testing.T) {
	buf := new(bytes.Buffer)
	cmd := &cobra.Command{}
	cmd.SetOut(buf)

	cliCtx := &cli.CLIContext{VM: "default"}
	ctx := cli.WithContext(context.Background(), cliCtx)
	cmd.SetContext(ctx)

	// Build a provisioner that fails on bootstrap verification.
	p := provision.NewProvisioner(
		&stubUpDescribeInstances{output: &ec2.DescribeInstancesOutput{}},
		&stubUpStartInstances{output: &ec2.StartInstancesOutput{}},
		&stubUpRunInstances{output: &ec2.RunInstancesOutput{}},
		&stubUpDescribeSGs{outputs: []*ec2.DescribeSecurityGroupsOutput{}, errs: []error{}},
		&stubUpDescribeSubnets{output: &ec2.DescribeSubnetsOutput{}},
		&stubUpCreateVolume{output: &ec2.CreateVolumeOutput{}},
		&stubUpAttachVolume{output: &ec2.AttachVolumeOutput{}},
		&stubUpAllocateAddress{output: &ec2.AllocateAddressOutput{}},
		&stubUpAssociateAddress{output: &ec2.AssociateAddressOutput{}},
		&stubUpDescribeAddresses{output: &ec2.DescribeAddressesOutput{}},
		&stubUpCreateTags{output: &ec2.CreateTagsOutput{}},
		&stubUpGetParameter{},
	)
	p.WithBootstrapVerifier(func(content []byte) error {
		return fmt.Errorf("hash mismatch")
	})
	p.WithAMIResolver(func(ctx context.Context, client mintaws.GetParameterAPI) (string, error) {
		return "ami-test", nil
	})

	deps := &upDeps{
		provisioner:     p,
		owner:           "testuser",
		ownerARN:        "arn:aws:iam::123:user/testuser",
		bootstrapScript: []byte("#!/bin/bash"),
		instanceType:    "m6i.xlarge",
		volumeSize:      50,
	}

	err := upWithProvisioner(ctx, cmd, cliCtx, deps, "default")
	if err == nil {
		t.Fatal("expected error on bootstrap verification failure")
	}
	if !strings.Contains(err.Error(), "bootstrap verification") {
		t.Errorf("error = %q, want substring %q", err.Error(), "bootstrap verification")
	}
}

func TestUpCommandVerboseOutput(t *testing.T) {
	buf := new(bytes.Buffer)

	deps := newTestUpDeps()
	cmd := newUpCommandWithDeps(deps)
	root := newTestRoot()
	root.AddCommand(cmd)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"up", "--verbose"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Provisioning VM") {
		t.Errorf("verbose output should contain 'Provisioning VM', got:\n%s", output)
	}
}

func TestUpCommandNilDeps(t *testing.T) {
	cmd := newUpCommandWithDeps(nil)
	root := newTestRoot()
	root.AddCommand(cmd)

	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"up"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error when deps is nil")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("error = %q, want substring %q", err.Error(), "not configured")
	}
}

func TestUpCommandRegistered(t *testing.T) {
	rootCmd := NewRootCommand()

	found := false
	for _, cmd := range rootCmd.Commands() {
		if cmd.Use == "up" {
			found = true
			break
		}
	}

	if !found {
		t.Error("up command not registered on root command")
	}
}

// ---------------------------------------------------------------------------
// Tests: Bootstrap polling output
// ---------------------------------------------------------------------------

func TestUpCommandBootstrapCompleteOutput(t *testing.T) {
	buf := new(bytes.Buffer)
	cmd := &cobra.Command{}
	cmd.SetOut(buf)

	cliCtx := &cli.CLIContext{VM: "default"}
	ctx := cli.WithContext(context.Background(), cliCtx)
	cmd.SetContext(ctx)

	// Set a poll func that succeeds.
	deps := newTestUpDeps()
	deps.provisioner.WithBootstrapPollFunc(func(ctx context.Context, owner, vmName, instanceID string) error {
		return nil
	})

	err := upWithProvisioner(ctx, cmd, cliCtx, deps, "default")
	if err != nil {
		t.Fatalf("upWithProvisioner error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Bootstrap complete. VM is ready.") {
		t.Errorf("output should contain 'Bootstrap complete. VM is ready.', got:\n%s", output)
	}
}

func TestUpCommandBootstrapErrorOutput(t *testing.T) {
	buf := new(bytes.Buffer)
	cmd := &cobra.Command{}
	cmd.SetOut(buf)

	cliCtx := &cli.CLIContext{VM: "default"}
	ctx := cli.WithContext(context.Background(), cliCtx)
	cmd.SetContext(ctx)

	// Set a poll func that fails.
	deps := newTestUpDeps()
	deps.provisioner.WithBootstrapPollFunc(func(ctx context.Context, owner, vmName, instanceID string) error {
		return fmt.Errorf("bootstrap timed out after 7m0s")
	})

	err := upWithProvisioner(ctx, cmd, cliCtx, deps, "default")
	if err != nil {
		t.Fatalf("upWithProvisioner error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Bootstrap warning:") {
		t.Errorf("output should contain 'Bootstrap warning:', got:\n%s", output)
	}
	if !strings.Contains(output, "bootstrap timed out") {
		t.Errorf("output should contain the error message, got:\n%s", output)
	}
	// Should still contain resource info.
	if !strings.Contains(output, "i-test123") {
		t.Errorf("output should still contain instance ID, got:\n%s", output)
	}
}

func TestUpCommandBootstrapErrorJSON(t *testing.T) {
	buf := new(bytes.Buffer)
	cmd := &cobra.Command{}
	cmd.SetOut(buf)

	cliCtx := &cli.CLIContext{JSON: true, VM: "default"}
	ctx := cli.WithContext(context.Background(), cliCtx)
	cmd.SetContext(ctx)

	deps := newTestUpDeps()
	deps.provisioner.WithBootstrapPollFunc(func(ctx context.Context, owner, vmName, instanceID string) error {
		return fmt.Errorf("poll timeout")
	})

	err := upWithProvisioner(ctx, cmd, cliCtx, deps, "default")
	if err != nil {
		t.Fatalf("upWithProvisioner error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v\nOutput: %s", err, buf.String())
	}

	if result["bootstrap_error"] != "poll timeout" {
		t.Errorf("bootstrap_error = %v, want %q", result["bootstrap_error"], "poll timeout")
	}
}

func TestUpCommandBootstrapSuccessJSONNoError(t *testing.T) {
	buf := new(bytes.Buffer)
	cmd := &cobra.Command{}
	cmd.SetOut(buf)

	cliCtx := &cli.CLIContext{JSON: true, VM: "default"}
	ctx := cli.WithContext(context.Background(), cliCtx)
	cmd.SetContext(ctx)

	deps := newTestUpDeps()
	deps.provisioner.WithBootstrapPollFunc(func(ctx context.Context, owner, vmName, instanceID string) error {
		return nil
	})

	err := upWithProvisioner(ctx, cmd, cliCtx, deps, "default")
	if err != nil {
		t.Fatalf("upWithProvisioner error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v\nOutput: %s", err, buf.String())
	}

	if _, ok := result["bootstrap_error"]; ok {
		t.Error("JSON output should NOT contain bootstrap_error key when bootstrap succeeds")
	}
}

// ---------------------------------------------------------------------------
// Tests: SSH config auto-generation after mint up
// ---------------------------------------------------------------------------

func TestUpCommandWritesSSHConfigOnSuccess(t *testing.T) {
	buf := new(bytes.Buffer)
	cmd := &cobra.Command{}
	cmd.SetOut(buf)

	cliCtx := &cli.CLIContext{VM: "default"}
	ctx := cli.WithContext(context.Background(), cliCtx)
	cmd.SetContext(ctx)

	// Use a temp file for SSH config.
	tmpDir := t.TempDir()
	sshConfigPath := tmpDir + "/config"
	os.WriteFile(sshConfigPath, []byte(""), 0644)

	// Stub DescribeInstances to return the VM with AZ info for SSH config generation.
	describeStub := &stubUpDescribeInstances{output: &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{
			Instances: []ec2types.Instance{{
				InstanceId:      aws.String("i-test123"),
				PublicIpAddress: aws.String("54.10.20.30"),
				InstanceType:    ec2types.InstanceTypeM6iXlarge,
				State: &ec2types.InstanceState{
					Name: ec2types.InstanceStateNameRunning,
				},
				Placement: &ec2types.Placement{
					AvailabilityZone: aws.String("us-east-1a"),
				},
				Tags: []ec2types.Tag{
					{Key: aws.String("mint:vm"), Value: aws.String("default")},
					{Key: aws.String("mint:owner"), Value: aws.String("testuser")},
				},
			}},
		}},
	}}

	deps := newTestUpDeps()
	deps.sshConfigApproved = true
	deps.sshConfigPath = sshConfigPath
	deps.describe = describeStub
	deps.owner = "testuser"

	err := runUp(cmd, deps)
	if err != nil {
		t.Fatalf("runUp error: %v", err)
	}

	// Verify SSH config was written.
	data, readErr := os.ReadFile(sshConfigPath)
	if readErr != nil {
		t.Fatalf("reading ssh config: %v", readErr)
	}
	content := string(data)

	if !strings.Contains(content, "Host mint-default") {
		t.Errorf("SSH config should contain 'Host mint-default', got:\n%s", content)
	}
	if !strings.Contains(content, "54.10.20.30") {
		t.Errorf("SSH config should contain the public IP, got:\n%s", content)
	}
}

func TestUpCommandSkipsSSHConfigWhenNotApproved(t *testing.T) {
	buf := new(bytes.Buffer)
	cmd := &cobra.Command{}
	cmd.SetOut(buf)

	cliCtx := &cli.CLIContext{VM: "default"}
	ctx := cli.WithContext(context.Background(), cliCtx)
	cmd.SetContext(ctx)

	tmpDir := t.TempDir()
	sshConfigPath := tmpDir + "/config"
	os.WriteFile(sshConfigPath, []byte(""), 0644)

	deps := newTestUpDeps()
	deps.sshConfigApproved = false
	deps.sshConfigPath = sshConfigPath

	err := runUp(cmd, deps)
	if err != nil {
		t.Fatalf("runUp error: %v", err)
	}

	// SSH config file should remain empty.
	data, readErr := os.ReadFile(sshConfigPath)
	if readErr != nil {
		t.Fatalf("reading ssh config: %v", readErr)
	}
	if len(data) > 0 {
		t.Errorf("SSH config should be empty when not approved, got:\n%s", string(data))
	}
}

func TestUpCommandSSHConfigWriteFailureIsWarning(t *testing.T) {
	buf := new(bytes.Buffer)
	cmd := &cobra.Command{}
	cmd.SetOut(buf)

	cliCtx := &cli.CLIContext{VM: "default"}
	ctx := cli.WithContext(context.Background(), cliCtx)
	cmd.SetContext(ctx)

	// Use a non-existent directory to force write failure.
	sshConfigPath := "/nonexistent/dir/config"

	describeStub := &stubUpDescribeInstances{output: &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{
			Instances: []ec2types.Instance{{
				InstanceId:      aws.String("i-test123"),
				PublicIpAddress: aws.String("54.10.20.30"),
				InstanceType:    ec2types.InstanceTypeM6iXlarge,
				State: &ec2types.InstanceState{
					Name: ec2types.InstanceStateNameRunning,
				},
				Placement: &ec2types.Placement{
					AvailabilityZone: aws.String("us-east-1a"),
				},
				Tags: []ec2types.Tag{
					{Key: aws.String("mint:vm"), Value: aws.String("default")},
					{Key: aws.String("mint:owner"), Value: aws.String("testuser")},
				},
			}},
		}},
	}}

	deps := newTestUpDeps()
	deps.sshConfigApproved = true
	deps.sshConfigPath = sshConfigPath
	deps.describe = describeStub
	deps.owner = "testuser"

	// Should succeed despite SSH config write failure.
	err := runUp(cmd, deps)
	if err != nil {
		t.Fatalf("runUp should not fail when SSH config write fails, got: %v", err)
	}

	// Should print a warning about the failure.
	output := buf.String()
	if !strings.Contains(output, "Warning") && !strings.Contains(output, "warning") && !strings.Contains(output, "ssh config") {
		t.Errorf("output should contain a warning about SSH config failure, got:\n%s", output)
	}
}

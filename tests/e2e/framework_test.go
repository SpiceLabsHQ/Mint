// Package e2e_test contains end-to-end workflow tests for the mint CLI.
//
// These tests exercise the full command pipeline (cobra → cmd → internal
// packages) using real in-process execution with a mock AWS layer. No real
// AWS calls are made — all AWS dependencies are stubbed via the narrow
// interfaces defined in internal/aws.
//
// Design: a testEnv builds a cobra command tree that mirrors the real mint CLI
// but wires mock AWS clients (stubs returning deterministic responses) instead
// of real SDK clients. For non-AWS commands (version, config, config set), the
// real cmd.NewRootCommand() is used directly since those commands make no AWS
// calls.
package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/efs"
	efstypes "github.com/aws/aws-sdk-go-v2/service/efs/types"
	"github.com/spf13/cobra"

	"github.com/nicholasgasior/mint/internal/cli"
	mintaws "github.com/nicholasgasior/mint/internal/aws"
	"github.com/nicholasgasior/mint/internal/provision"
	"github.com/nicholasgasior/mint/internal/vm"
)

// ---------------------------------------------------------------------------
// testEnv — end-to-end test harness
// ---------------------------------------------------------------------------

// testEnv holds a complete, mock-backed command tree and provides a
// RunCommand helper for executing commands and inspecting output.
type testEnv struct {
	t    *testing.T
	root *cobra.Command
}

// RunCommand executes the command defined by args against the test harness.
// It returns stdout, stderr, and any execution error.
func (e *testEnv) RunCommand(args []string) (stdout, stderr string, err error) {
	e.t.Helper()
	outBuf := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	e.root.SetOut(outBuf)
	e.root.SetErr(errBuf)
	e.root.SetArgs(args)
	execErr := e.root.Execute()
	return outBuf.String(), errBuf.String(), execErr
}

// ---------------------------------------------------------------------------
// Stub AWS clients — implement narrow interfaces from internal/aws
// ---------------------------------------------------------------------------

// stubDescribeInstances returns a fixed output on every call.
type stubDescribeInstances struct {
	output *ec2.DescribeInstancesOutput
	err    error
}

func (s *stubDescribeInstances) DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return s.output, s.err
}

// stubStartInstances always succeeds.
type stubStartInstances struct{}

func (s *stubStartInstances) StartInstances(ctx context.Context, params *ec2.StartInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error) {
	return &ec2.StartInstancesOutput{}, nil
}

// stubStopInstances always succeeds.
type stubStopInstances struct{}

func (s *stubStopInstances) StopInstances(ctx context.Context, params *ec2.StopInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StopInstancesOutput, error) {
	return &ec2.StopInstancesOutput{}, nil
}

// stubRunInstances returns one instance with the configured ID and a BDM volume.
type stubRunInstances struct {
	instanceID string
	volumeID   string
}

func (s *stubRunInstances) RunInstances(ctx context.Context, params *ec2.RunInstancesInput, optFns ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error) {
	return &ec2.RunInstancesOutput{
		Instances: []ec2types.Instance{{
			InstanceId: aws.String(s.instanceID),
			BlockDeviceMappings: []ec2types.InstanceBlockDeviceMapping{{
				DeviceName: aws.String("/dev/xvdf"),
				Ebs: &ec2types.EbsInstanceBlockDevice{VolumeId: aws.String(s.volumeID)},
			}},
		}},
	}, nil
}

// captureRunInstances records the RunInstances input for IOPS assertion.
type captureRunInstances struct {
	instanceID string
	volumeID   string
	lastInput  *ec2.RunInstancesInput
}

func (c *captureRunInstances) RunInstances(ctx context.Context, params *ec2.RunInstancesInput, optFns ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error) {
	c.lastInput = params
	return &ec2.RunInstancesOutput{
		Instances: []ec2types.Instance{{
			InstanceId: aws.String(c.instanceID),
			BlockDeviceMappings: []ec2types.InstanceBlockDeviceMapping{{
				DeviceName: aws.String("/dev/xvdf"),
				Ebs: &ec2types.EbsInstanceBlockDevice{VolumeId: aws.String(c.volumeID)},
			}},
		}},
	}, nil
}

// stubDescribeSGsDouble returns two security groups (user then admin) on successive calls.
type stubDescribeSGsDouble struct{ calls int }

func (s *stubDescribeSGsDouble) DescribeSecurityGroups(ctx context.Context, params *ec2.DescribeSecurityGroupsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error) {
	s.calls++
	if s.calls == 1 {
		return &ec2.DescribeSecurityGroupsOutput{
			SecurityGroups: []ec2types.SecurityGroup{{GroupId: aws.String("sg-user-e2e")}},
		}, nil
	}
	return &ec2.DescribeSecurityGroupsOutput{
		SecurityGroups: []ec2types.SecurityGroup{{GroupId: aws.String("sg-admin-e2e")}},
	}, nil
}

// stubDescribeSubnets returns a single subnet.
type stubDescribeSubnets struct{}

func (s *stubDescribeSubnets) DescribeSubnets(ctx context.Context, params *ec2.DescribeSubnetsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error) {
	return &ec2.DescribeSubnetsOutput{
		Subnets: []ec2types.Subnet{{
			SubnetId:         aws.String("subnet-e2e"),
			AvailabilityZone: aws.String("us-east-1a"),
		}},
	}, nil
}

// stubCreateVolume returns a volume with the configured ID and records the last input.
type stubCreateVolume struct {
	volumeID  string
	lastInput *ec2.CreateVolumeInput
}

func (s *stubCreateVolume) CreateVolume(ctx context.Context, params *ec2.CreateVolumeInput, optFns ...func(*ec2.Options)) (*ec2.CreateVolumeOutput, error) {
	s.lastInput = params
	return &ec2.CreateVolumeOutput{VolumeId: aws.String(s.volumeID)}, nil
}

// stubAttachVolume always succeeds.
type stubAttachVolume struct{}

func (s *stubAttachVolume) AttachVolume(ctx context.Context, params *ec2.AttachVolumeInput, optFns ...func(*ec2.Options)) (*ec2.AttachVolumeOutput, error) {
	return &ec2.AttachVolumeOutput{}, nil
}

// stubAllocateAddress returns a fixed EIP.
type stubAllocateAddress struct {
	allocationID string
	publicIP     string
}

func (s *stubAllocateAddress) AllocateAddress(ctx context.Context, params *ec2.AllocateAddressInput, optFns ...func(*ec2.Options)) (*ec2.AllocateAddressOutput, error) {
	return &ec2.AllocateAddressOutput{
		AllocationId: aws.String(s.allocationID),
		PublicIp:     aws.String(s.publicIP),
	}, nil
}

// stubAssociateAddress always succeeds.
type stubAssociateAddress struct{}

func (s *stubAssociateAddress) AssociateAddress(ctx context.Context, params *ec2.AssociateAddressInput, optFns ...func(*ec2.Options)) (*ec2.AssociateAddressOutput, error) {
	return &ec2.AssociateAddressOutput{}, nil
}

// stubDescribeAddresses returns no existing addresses (EIP limit not hit).
type stubDescribeAddresses struct{}

func (s *stubDescribeAddresses) DescribeAddresses(ctx context.Context, params *ec2.DescribeAddressesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeAddressesOutput, error) {
	return &ec2.DescribeAddressesOutput{Addresses: []ec2types.Address{}}, nil
}

// stubCreateTags always succeeds.
type stubCreateTags struct{}

func (s *stubCreateTags) CreateTags(ctx context.Context, params *ec2.CreateTagsInput, optFns ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error) {
	return &ec2.CreateTagsOutput{}, nil
}

// stubDescribeImages always returns empty (unused in e2e tests — AMI resolver is overridden).
type stubDescribeImages struct{}

func (s *stubDescribeImages) DescribeImages(ctx context.Context, params *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
	return &ec2.DescribeImagesOutput{}, nil
}

// stubDescribeFileSystems returns a single admin EFS filesystem.
type stubDescribeFileSystems struct{ filesystemID string }

func (s *stubDescribeFileSystems) DescribeFileSystems(ctx context.Context, params *efs.DescribeFileSystemsInput, optFns ...func(*efs.Options)) (*efs.DescribeFileSystemsOutput, error) {
	return &efs.DescribeFileSystemsOutput{
		FileSystems: []efstypes.FileSystemDescription{{
			FileSystemId: aws.String(s.filesystemID),
			Tags: []efstypes.Tag{
				{Key: aws.String("mint"), Value: aws.String("true")},
				{Key: aws.String("mint:component"), Value: aws.String("admin")},
			},
		}},
	}, nil
}

// stubDescribeFileSystemsWithError always returns the configured error.
type stubDescribeFileSystemsWithError struct{ err error }

func (s *stubDescribeFileSystemsWithError) DescribeFileSystems(ctx context.Context, params *efs.DescribeFileSystemsInput, optFns ...func(*efs.Options)) (*efs.DescribeFileSystemsOutput, error) {
	return nil, s.err
}

// ---------------------------------------------------------------------------
// Fake EC2 instance builders
// ---------------------------------------------------------------------------

// makeE2EInstance creates an EC2 instance value for use in describe stubs.
func makeE2EInstance(instanceID, vmName, owner, state, publicIP, instanceType string) ec2types.Instance {
	inst := ec2types.Instance{
		InstanceId:   aws.String(instanceID),
		InstanceType: ec2types.InstanceType(instanceType),
		LaunchTime:   aws.Time(time.Now().Add(-10 * time.Minute)),
		State: &ec2types.InstanceState{
			Name: ec2types.InstanceStateName(state),
		},
		Placement: &ec2types.Placement{
			AvailabilityZone: aws.String("us-east-1a"),
		},
		Tags: []ec2types.Tag{
			{Key: aws.String("mint"), Value: aws.String("true")},
			{Key: aws.String("mint:vm"), Value: aws.String(vmName)},
			{Key: aws.String("mint:owner"), Value: aws.String(owner)},
			{Key: aws.String("mint:bootstrap"), Value: aws.String("complete")},
		},
	}
	if publicIP != "" {
		inst.PublicIpAddress = aws.String(publicIP)
	}
	return inst
}

// makeE2EDescribeOutput wraps instances in a DescribeInstancesOutput.
func makeE2EDescribeOutput(instances ...ec2types.Instance) *ec2.DescribeInstancesOutput {
	return &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{Instances: instances}},
	}
}

// ---------------------------------------------------------------------------
// Provisioner builders
// ---------------------------------------------------------------------------

// newFreshProvisioner returns a Provisioner that will provision a brand-new VM.
// DescribeInstances returns empty (no existing VM), so RunInstances is called.
func newFreshProvisioner(instanceID, volumeID, allocationID, publicIP string) *provision.Provisioner {
	p := provision.NewProvisioner(
		&stubDescribeInstances{output: &ec2.DescribeInstancesOutput{}},
		&stubStartInstances{},
		&stubRunInstances{instanceID: instanceID, volumeID: volumeID},
		&stubDescribeSGsDouble{},
		&stubDescribeSubnets{},
		&stubCreateVolume{volumeID: volumeID},
		&stubAttachVolume{},
		&stubAllocateAddress{allocationID: allocationID, publicIP: publicIP},
		&stubAssociateAddress{},
		&stubDescribeAddresses{},
		&stubCreateTags{},
		&stubDescribeImages{},
	)
	p.WithBootstrapVerifier(func(content []byte) error { return nil })
	p.WithAMIResolver(func(ctx context.Context, client mintaws.DescribeImagesAPI) (string, error) {
		return "ami-e2e-test", nil
	})
	return p
}

// newRestartProvisioner returns a Provisioner that will restart a stopped VM.
// DescribeInstances returns a stopped VM, so StartInstances is called.
func newRestartProvisioner(instanceID, vmName, owner, publicIP string) *provision.Provisioner {
	p := provision.NewProvisioner(
		&stubDescribeInstances{
			output: makeE2EDescribeOutput(
				makeE2EInstance(instanceID, vmName, owner, "stopped", publicIP, "m6i.xlarge"),
			),
		},
		&stubStartInstances{},
		&stubRunInstances{instanceID: instanceID},
		&stubDescribeSGsDouble{},
		&stubDescribeSubnets{},
		&stubCreateVolume{volumeID: "vol-restart-e2e"},
		&stubAttachVolume{},
		&stubAllocateAddress{allocationID: "eipalloc-restart-e2e", publicIP: publicIP},
		&stubAssociateAddress{},
		&stubDescribeAddresses{},
		&stubCreateTags{},
		&stubDescribeImages{},
	)
	p.WithBootstrapVerifier(func(content []byte) error { return nil })
	p.WithAMIResolver(func(ctx context.Context, client mintaws.DescribeImagesAPI) (string, error) {
		return "ami-e2e-test", nil
	})
	return p
}

// newFreshProvisionerCapturingRun is like newFreshProvisioner but accepts a
// caller-owned captureRunInstances so the caller can inspect lastInput after
// the provision completes (e.g., to assert IOPS in BlockDeviceMappings).
func newFreshProvisionerCapturingRun(ri *captureRunInstances, allocationID, publicIP string) *provision.Provisioner {
	p := provision.NewProvisioner(
		&stubDescribeInstances{output: &ec2.DescribeInstancesOutput{}},
		&stubStartInstances{},
		ri,
		&stubDescribeSGsDouble{},
		&stubDescribeSubnets{},
		&stubCreateVolume{volumeID: ri.volumeID},
		&stubAttachVolume{},
		&stubAllocateAddress{allocationID: allocationID, publicIP: publicIP},
		&stubAssociateAddress{},
		&stubDescribeAddresses{},
		&stubCreateTags{},
		&stubDescribeImages{},
	)
	p.WithBootstrapVerifier(func(content []byte) error { return nil })
	p.WithAMIResolver(func(ctx context.Context, client mintaws.DescribeImagesAPI) (string, error) {
		return "ami-e2e-test", nil
	})
	return p
}

// ---------------------------------------------------------------------------
// e2eConfig — dependency container for test command tree
// ---------------------------------------------------------------------------

// e2eConfig holds all injected dependencies for the mock-backed command tree.
type e2eConfig struct {
	// "up" command
	provisioner         *provision.Provisioner
	describeFileSystems mintaws.DescribeFileSystemsAPI

	// "down" command
	describeForDown mintaws.DescribeInstancesAPI
	stopInstances   mintaws.StopInstancesAPI

	// "list" command
	describeForList mintaws.DescribeInstancesAPI

	// "status" command
	describeForStatus mintaws.DescribeInstancesAPI

	// Shared identity
	owner    string
	ownerARN string
}

// ---------------------------------------------------------------------------
// newE2ERoot — builds the test command tree
// ---------------------------------------------------------------------------

// newE2ERoot constructs a complete mint-like cobra command tree with all
// global flags and mock-backed subcommands. It exercises the full cobra
// routing and flag-parsing pipeline while using deterministic AWS stubs.
func newE2ERoot(t *testing.T, cfg *e2eConfig) *cobra.Command {
	t.Helper()

	root := &cobra.Command{
		Use:           "mint",
		Short:         "Provision and manage EC2-based development environments",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Build and store CLI context (same as real root's PersistentPreRunE,
			// but skip real AWS initialization — stubs are wired per-command).
			cliCtx := cli.NewCLIContext(cmd)
			cmd.SetContext(cli.WithContext(context.Background(), cliCtx))
			return nil
		},
	}

	// Global flags — identical to cmd.NewRootCommand()
	root.PersistentFlags().Bool("verbose", false, "Show progress steps")
	root.PersistentFlags().Bool("debug", false, "Show AWS SDK details")
	root.PersistentFlags().Bool("json", false, "Machine-readable JSON output")
	root.PersistentFlags().Bool("yes", false, "Skip confirmation on destructive operations")
	root.PersistentFlags().String("vm", "default", "Target VM name")

	// Register mock-backed AWS commands
	root.AddCommand(newE2EUpCommand(cfg))
	root.AddCommand(newE2EDownCommand(cfg))
	root.AddCommand(newE2EListCommand(cfg))
	root.AddCommand(newE2EStatusCommand(cfg))

	return root
}

// ---------------------------------------------------------------------------
// Mock-backed "up" command
// ---------------------------------------------------------------------------

// newE2EUpCommand builds a "mint up" cobra command backed by injected stubs.
// It calls provision.Provisioner.Run() — the same real internal logic used by
// the production command — just with mock AWS transport.
func newE2EUpCommand(cfg *e2eConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Provision or start the VM",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			cliCtx := cli.FromCommand(cmd)
			vmName := "default"
			jsonOutput := false
			if cliCtx != nil {
				vmName = cliCtx.VM
				jsonOutput = cliCtx.JSON
			}

			// Discover EFS filesystem (same logic as real cmd/up.go)
			efsOut, err := cfg.describeFileSystems.DescribeFileSystems(ctx, &efs.DescribeFileSystemsInput{})
			if err != nil {
				return fmt.Errorf("discovering EFS: %w", err)
			}
			if len(efsOut.FileSystems) == 0 {
				return fmt.Errorf("no admin EFS found — run 'mint init' first")
			}
			efsID := aws.ToString(efsOut.FileSystems[0].FileSystemId)

			// Run the real provision logic with injected stubs
			volumeIOPS, _ := cmd.Flags().GetInt32("volume-iops")
			provCfg := provision.ProvisionConfig{
				InstanceType:    "m6i.xlarge",
				VolumeSize:      50,
				VolumeIOPS:      volumeIOPS,
				BootstrapScript: []byte("#!/bin/bash\necho test"),
				EFSID:           efsID,
			}

			result, err := cfg.provisioner.Run(ctx, cfg.owner, cfg.ownerARN, vmName, provCfg)
			if err != nil {
				return err
			}

			return printE2EUpResult(cmd, result, jsonOutput)
		},
	}
	cmd.Flags().Int32("volume-iops", 0, "IOPS for the project EBS volume")
	return cmd
}

// printE2EUpResult outputs the provision result — same logic as cmd/up.go's
// printUpHuman / printUpJSON, replicated here since those are unexported.
func printE2EUpResult(cmd *cobra.Command, result *provision.ProvisionResult, jsonOutput bool) error {
	if jsonOutput {
		data := map[string]any{
			"instance_id":   result.InstanceID,
			"public_ip":     result.PublicIP,
			"volume_id":     result.VolumeID,
			"allocation_id": result.AllocationID,
			"restarted":     result.Restarted,
		}
		if result.BootstrapError != nil {
			data["bootstrap_error"] = result.BootstrapError.Error()
		}
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(data)
	}

	w := cmd.OutOrStdout()
	if result.Restarted {
		fmt.Fprintf(w, "VM %s restarted.\n", result.InstanceID)
		if result.PublicIP != "" {
			fmt.Fprintf(w, "IP            %s\n", result.PublicIP)
		}
		return nil
	}
	fmt.Fprintf(w, "Instance      %s\n", result.InstanceID)
	if result.PublicIP != "" {
		fmt.Fprintf(w, "IP            %s\n", result.PublicIP)
	}
	if result.VolumeID != "" {
		fmt.Fprintf(w, "Volume        %s\n", result.VolumeID)
	}
	if result.AllocationID != "" {
		fmt.Fprintf(w, "EIP           %s\n", result.AllocationID)
	}
	if result.BootstrapError != nil {
		fmt.Fprintf(w, "\nBootstrap warning: %v\n", result.BootstrapError)
	} else {
		fmt.Fprintln(w, "\nBootstrap complete. VM is ready.")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Mock-backed "down" command
// ---------------------------------------------------------------------------

// newE2EDownCommand builds a "mint down" cobra command backed by injected stubs.
// It calls vm.FindVM() — the same real internal logic used by production.
func newE2EDownCommand(cfg *e2eConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Stop the VM instance",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			cliCtx := cli.FromCommand(cmd)
			vmName := "default"
			if cliCtx != nil {
				vmName = cliCtx.VM
			}

			found, err := vm.FindVM(ctx, cfg.describeForDown, cfg.owner, vmName)
			if err != nil {
				return fmt.Errorf("discovering VM: %w", err)
			}
			if found == nil {
				return fmt.Errorf("no VM %q found — run mint up first to create one", vmName)
			}

			if found.State == "stopped" || found.State == "stopping" {
				fmt.Fprintf(cmd.OutOrStdout(), "VM %q (%s) is already stopped.\n", vmName, found.ID)
				return nil
			}

			_, err = cfg.stopInstances.StopInstances(ctx, &ec2.StopInstancesInput{
				InstanceIds: []string{found.ID},
			})
			if err != nil {
				return fmt.Errorf("stopping instance %s: %w", found.ID, err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "VM %q (%s) stopped. Volumes and Elastic IP persist.\n", vmName, found.ID)
			return nil
		},
	}
}

// ---------------------------------------------------------------------------
// Mock-backed "list" command
// ---------------------------------------------------------------------------

// vmListJSON mirrors the shape of cmd.listJSON for e2e output validation.
type vmListJSON struct {
	VMs             []vmItemJSON `json:"vms"`
	UpdateAvailable bool         `json:"update_available"`
	LatestVersion   *string      `json:"latest_version"`
}

type vmItemJSON struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	State           string `json:"state"`
	PublicIP        string `json:"public_ip,omitempty"`
	InstanceType    string `json:"instance_type"`
	BootstrapStatus string `json:"bootstrap_status"`
}

// newE2EListCommand builds a "mint list" cobra command backed by injected stubs.
// It calls vm.ListVMs() — the same real internal logic used by production.
func newE2EListCommand(cfg *e2eConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all VMs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			cliCtx := cli.FromCommand(cmd)
			jsonOutput := cliCtx != nil && cliCtx.JSON

			vms, err := vm.ListVMs(ctx, cfg.describeForList, cfg.owner)
			if err != nil {
				return fmt.Errorf("listing VMs: %w", err)
			}

			w := cmd.OutOrStdout()

			if jsonOutput {
				items := make([]vmItemJSON, 0, len(vms))
				for _, v := range vms {
					items = append(items, vmItemJSON{
						ID:              v.ID,
						Name:            v.Name,
						State:           v.State,
						PublicIP:        v.PublicIP,
						InstanceType:    v.InstanceType,
						BootstrapStatus: v.BootstrapStatus,
					})
				}
				out := vmListJSON{
					VMs:             items,
					UpdateAvailable: false,
					LatestVersion:   nil,
				}
				enc := json.NewEncoder(w)
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			}

			if len(vms) == 0 {
				fmt.Fprintln(w, "No VMs found.")
				return nil
			}
			for _, v := range vms {
				ip := v.PublicIP
				if ip == "" {
					ip = "-"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", v.Name, v.State, ip, v.InstanceType)
			}
			return nil
		},
	}
}

// ---------------------------------------------------------------------------
// Mock-backed "status" command
// ---------------------------------------------------------------------------

// newE2EStatusCommand builds a "mint status" cobra command backed by injected stubs.
// It calls vm.FindVM() — the same real internal logic used by production.
func newE2EStatusCommand(cfg *e2eConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show VM details",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			cliCtx := cli.FromCommand(cmd)
			vmName := "default"
			jsonOutput := false
			if cliCtx != nil {
				vmName = cliCtx.VM
				jsonOutput = cliCtx.JSON
			}

			found, err := vm.FindVM(ctx, cfg.describeForStatus, cfg.owner, vmName)
			if err != nil {
				return fmt.Errorf("finding VM: %w", err)
			}
			if found == nil {
				return fmt.Errorf("VM %q not found for owner %q", vmName, cfg.owner)
			}

			w := cmd.OutOrStdout()

			if jsonOutput {
				out := map[string]any{
					"id":               found.ID,
					"name":             found.Name,
					"state":            found.State,
					"public_ip":        found.PublicIP,
					"instance_type":    found.InstanceType,
					"bootstrap_status": found.BootstrapStatus,
				}
				enc := json.NewEncoder(w)
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			}

			ip := found.PublicIP
			if ip == "" {
				ip = "-"
			}
			fmt.Fprintf(w, "VM:        %s\n", found.Name)
			fmt.Fprintf(w, "ID:        %s\n", found.ID)
			fmt.Fprintf(w, "State:     %s\n", found.State)
			fmt.Fprintf(w, "IP:        %s\n", ip)
			fmt.Fprintf(w, "Type:      %s\n", found.InstanceType)
			fmt.Fprintf(w, "Bootstrap: %s\n", found.BootstrapStatus)
			return nil
		},
	}
}

// ---------------------------------------------------------------------------
// Test assertion helpers
// ---------------------------------------------------------------------------

// assertContains verifies that output contains all expected substrings.
func assertContains(t *testing.T, label, output string, substrings []string) {
	t.Helper()
	for _, s := range substrings {
		if !strings.Contains(output, s) {
			t.Errorf("[%s] output missing %q\nfull output:\n%s", label, s, output)
		}
	}
}

// requireNoError calls t.Fatal if err is non-nil.
func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

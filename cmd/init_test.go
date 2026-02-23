package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/efs"
	efstypes "github.com/aws/aws-sdk-go-v2/service/efs/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/nicholasgasior/mint/internal/cli"
	"github.com/nicholasgasior/mint/internal/config"
	"github.com/nicholasgasior/mint/internal/provision"
	"github.com/spf13/cobra"
)

// ---------------------------------------------------------------------------
// Inline mocks for cmd-level init tests
// ---------------------------------------------------------------------------

type stubDescribeVpcs struct {
	output *ec2.DescribeVpcsOutput
	err    error
}

func (s *stubDescribeVpcs) DescribeVpcs(ctx context.Context, params *ec2.DescribeVpcsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error) {
	return s.output, s.err
}

type stubDescribeSubnets struct {
	output *ec2.DescribeSubnetsOutput
	err    error
}

func (s *stubDescribeSubnets) DescribeSubnets(ctx context.Context, params *ec2.DescribeSubnetsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error) {
	return s.output, s.err
}

type stubDescribeFileSystems struct {
	output *efs.DescribeFileSystemsOutput
	err    error
}

func (s *stubDescribeFileSystems) DescribeFileSystems(ctx context.Context, params *efs.DescribeFileSystemsInput, optFns ...func(*efs.Options)) (*efs.DescribeFileSystemsOutput, error) {
	return s.output, s.err
}

type stubDescribeSecurityGroups struct {
	output *ec2.DescribeSecurityGroupsOutput
	err    error
}

func (s *stubDescribeSecurityGroups) DescribeSecurityGroups(ctx context.Context, params *ec2.DescribeSecurityGroupsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error) {
	return s.output, s.err
}

type stubCreateSecurityGroup struct {
	output *ec2.CreateSecurityGroupOutput
	err    error
}

func (s *stubCreateSecurityGroup) CreateSecurityGroup(ctx context.Context, params *ec2.CreateSecurityGroupInput, optFns ...func(*ec2.Options)) (*ec2.CreateSecurityGroupOutput, error) {
	return s.output, s.err
}

type stubAuthorizeIngress struct {
	output *ec2.AuthorizeSecurityGroupIngressOutput
	err    error
}

func (s *stubAuthorizeIngress) AuthorizeSecurityGroupIngress(ctx context.Context, params *ec2.AuthorizeSecurityGroupIngressInput, optFns ...func(*ec2.Options)) (*ec2.AuthorizeSecurityGroupIngressOutput, error) {
	return s.output, s.err
}

type stubCreateTags struct {
	output *ec2.CreateTagsOutput
	err    error
}

func (s *stubCreateTags) CreateTags(ctx context.Context, params *ec2.CreateTagsInput, optFns ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error) {
	return s.output, s.err
}

type stubDescribeAccessPoints struct {
	output *efs.DescribeAccessPointsOutput
	err    error
}

func (s *stubDescribeAccessPoints) DescribeAccessPoints(ctx context.Context, params *efs.DescribeAccessPointsInput, optFns ...func(*efs.Options)) (*efs.DescribeAccessPointsOutput, error) {
	return s.output, s.err
}

type stubCreateAccessPoint struct {
	output *efs.CreateAccessPointOutput
	err    error
}

func (s *stubCreateAccessPoint) CreateAccessPoint(ctx context.Context, params *efs.CreateAccessPointInput, optFns ...func(*efs.Options)) (*efs.CreateAccessPointOutput, error) {
	return s.output, s.err
}

type stubGetInstanceProfile struct {
	output *iam.GetInstanceProfileOutput
	err    error
}

func (s *stubGetInstanceProfile) GetInstanceProfile(ctx context.Context, params *iam.GetInstanceProfileInput, optFns ...func(*iam.Options)) (*iam.GetInstanceProfileOutput, error) {
	return s.output, s.err
}

// newTestInitializer builds an Initializer with happy-path stubs.
func newTestInitializer() *provision.Initializer {
	return provision.NewInitializer(
		&stubDescribeVpcs{output: &ec2.DescribeVpcsOutput{
			Vpcs: []ec2types.Vpc{{VpcId: aws.String("vpc-test"), IsDefault: aws.Bool(true)}},
		}},
		&stubDescribeSubnets{output: &ec2.DescribeSubnetsOutput{
			Subnets: []ec2types.Subnet{{SubnetId: aws.String("subnet-1"), MapPublicIpOnLaunch: aws.Bool(true)}},
		}},
		&stubDescribeFileSystems{output: &efs.DescribeFileSystemsOutput{
			FileSystems: []efstypes.FileSystemDescription{{
				FileSystemId: aws.String("fs-test"),
				Tags: []efstypes.Tag{
					{Key: aws.String("mint"), Value: aws.String("true")},
					{Key: aws.String("mint:component"), Value: aws.String("admin")},
				},
			}},
		}},
		&stubGetInstanceProfile{output: &iam.GetInstanceProfileOutput{
			InstanceProfile: &iamtypes.InstanceProfile{
				InstanceProfileName: aws.String("mint-vm"),
			},
		}},
		&stubDescribeSecurityGroups{output: &ec2.DescribeSecurityGroupsOutput{
			SecurityGroups: []ec2types.SecurityGroup{},
		}},
		&stubCreateSecurityGroup{output: &ec2.CreateSecurityGroupOutput{
			GroupId: aws.String("sg-test"),
		}},
		&stubAuthorizeIngress{output: &ec2.AuthorizeSecurityGroupIngressOutput{}},
		&stubCreateTags{output: &ec2.CreateTagsOutput{}},
		&stubDescribeAccessPoints{output: &efs.DescribeAccessPointsOutput{
			AccessPoints: []efstypes.AccessPointDescription{},
		}},
		&stubCreateAccessPoint{output: &efs.CreateAccessPointOutput{
			AccessPointId: aws.String("fsap-test"),
		}},
	)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestInitCommandHumanOutput(t *testing.T) {
	buf := new(bytes.Buffer)
	cmd := &cobra.Command{}
	cmd.SetOut(buf)

	cliCtx := &cli.CLIContext{VM: "default"}
	ctx := cli.WithContext(context.Background(), cliCtx)
	cmd.SetContext(ctx)

	initializer := newTestInitializer()
	err := initWithInitializer(ctx, cmd, cliCtx, initializer, "testuser", "arn:aws:iam::123:user/testuser", "default")
	if err != nil {
		t.Fatalf("initWithInitializer error: %v", err)
	}

	output := buf.String()

	expectations := []string{
		"vpc-test",
		"fs-test",
		"sg-test",
		"fsap-test",
		"created",
		"Initialization complete",
	}

	for _, exp := range expectations {
		if !strings.Contains(output, exp) {
			t.Errorf("output missing %q, got:\n%s", exp, output)
		}
	}
}

func TestInitCommandJSONOutput(t *testing.T) {
	buf := new(bytes.Buffer)
	cmd := &cobra.Command{}
	cmd.SetOut(buf)

	cliCtx := &cli.CLIContext{JSON: true, VM: "default"}
	ctx := cli.WithContext(context.Background(), cliCtx)
	cmd.SetContext(ctx)

	initializer := newTestInitializer()
	err := initWithInitializer(ctx, cmd, cliCtx, initializer, "testuser", "arn:aws:iam::123:user/testuser", "default")
	if err != nil {
		t.Fatalf("initWithInitializer error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v\nOutput: %s", err, buf.String())
	}

	expectedKeys := []string{"vpc_id", "efs_id", "security_group", "sg_created", "access_point_id", "ap_created"}
	for _, key := range expectedKeys {
		if _, ok := result[key]; !ok {
			t.Errorf("JSON output missing key %q", key)
		}
	}

	if result["vpc_id"] != "vpc-test" {
		t.Errorf("vpc_id = %v, want vpc-test", result["vpc_id"])
	}
	if result["sg_created"] != true {
		t.Errorf("sg_created = %v, want true", result["sg_created"])
	}
}

func TestInitCommandError(t *testing.T) {
	buf := new(bytes.Buffer)
	cmd := &cobra.Command{}
	cmd.SetOut(buf)

	cliCtx := &cli.CLIContext{VM: "default"}
	ctx := cli.WithContext(context.Background(), cliCtx)
	cmd.SetContext(ctx)

	// Build an initializer that will fail on VPC validation.
	initializer := provision.NewInitializer(
		&stubDescribeVpcs{output: &ec2.DescribeVpcsOutput{Vpcs: []ec2types.Vpc{}}},
		&stubDescribeSubnets{output: &ec2.DescribeSubnetsOutput{}},
		&stubDescribeFileSystems{output: &efs.DescribeFileSystemsOutput{}},
		&stubGetInstanceProfile{output: &iam.GetInstanceProfileOutput{
			InstanceProfile: &iamtypes.InstanceProfile{
				InstanceProfileName: aws.String("mint-vm"),
			},
		}},
		&stubDescribeSecurityGroups{output: &ec2.DescribeSecurityGroupsOutput{}},
		&stubCreateSecurityGroup{output: &ec2.CreateSecurityGroupOutput{}},
		&stubAuthorizeIngress{output: &ec2.AuthorizeSecurityGroupIngressOutput{}},
		&stubCreateTags{output: &ec2.CreateTagsOutput{}},
		&stubDescribeAccessPoints{output: &efs.DescribeAccessPointsOutput{}},
		&stubCreateAccessPoint{output: &efs.CreateAccessPointOutput{}},
	)

	err := initWithInitializer(ctx, cmd, cliCtx, initializer, "testuser", "arn:aws:iam::123:user/testuser", "default")
	if err == nil {
		t.Fatal("expected error when VPC validation fails")
	}
	if !strings.Contains(err.Error(), "no default VPC") {
		t.Errorf("error = %q, want substring %q", err.Error(), "no default VPC")
	}
}

func TestInitCommandIdempotentOutput(t *testing.T) {
	buf := new(bytes.Buffer)
	cmd := &cobra.Command{}
	cmd.SetOut(buf)

	cliCtx := &cli.CLIContext{VM: "default"}
	ctx := cli.WithContext(context.Background(), cliCtx)
	cmd.SetContext(ctx)

	// Build an initializer where SG and AP already exist.
	initializer := provision.NewInitializer(
		&stubDescribeVpcs{output: &ec2.DescribeVpcsOutput{
			Vpcs: []ec2types.Vpc{{VpcId: aws.String("vpc-test"), IsDefault: aws.Bool(true)}},
		}},
		&stubDescribeSubnets{output: &ec2.DescribeSubnetsOutput{
			Subnets: []ec2types.Subnet{{SubnetId: aws.String("subnet-1"), MapPublicIpOnLaunch: aws.Bool(true)}},
		}},
		&stubDescribeFileSystems{output: &efs.DescribeFileSystemsOutput{
			FileSystems: []efstypes.FileSystemDescription{{
				FileSystemId: aws.String("fs-test"),
				Tags: []efstypes.Tag{
					{Key: aws.String("mint"), Value: aws.String("true")},
					{Key: aws.String("mint:component"), Value: aws.String("admin")},
				},
			}},
		}},
		&stubGetInstanceProfile{output: &iam.GetInstanceProfileOutput{
			InstanceProfile: &iamtypes.InstanceProfile{
				InstanceProfileName: aws.String("mint-vm"),
			},
		}},
		&stubDescribeSecurityGroups{output: &ec2.DescribeSecurityGroupsOutput{
			SecurityGroups: []ec2types.SecurityGroup{
				{GroupId: aws.String("sg-existing")},
			},
		}},
		&stubCreateSecurityGroup{output: &ec2.CreateSecurityGroupOutput{}},
		&stubAuthorizeIngress{output: &ec2.AuthorizeSecurityGroupIngressOutput{}},
		&stubCreateTags{output: &ec2.CreateTagsOutput{}},
		&stubDescribeAccessPoints{output: &efs.DescribeAccessPointsOutput{
			AccessPoints: []efstypes.AccessPointDescription{{
				AccessPointId: aws.String("fsap-existing"),
				Tags: []efstypes.Tag{
					{Key: aws.String("mint"), Value: aws.String("true")},
					{Key: aws.String("mint:owner"), Value: aws.String("testuser")},
					{Key: aws.String("mint:component"), Value: aws.String("efs-access-point")},
				},
			}},
		}},
		&stubCreateAccessPoint{output: &efs.CreateAccessPointOutput{}},
	)

	err := initWithInitializer(ctx, cmd, cliCtx, initializer, "testuser", "arn:aws:iam::123:user/testuser", "default")
	if err != nil {
		t.Fatalf("initWithInitializer error: %v", err)
	}

	output := buf.String()

	if !strings.Contains(output, "exists") {
		t.Errorf("expected 'exists' in output for idempotent run, got:\n%s", output)
	}
	if strings.Contains(output, "(created)") {
		t.Errorf("expected no '(created)' in output for idempotent run, got:\n%s", output)
	}
}

func TestInitCommandRegistered(t *testing.T) {
	rootCmd := NewRootCommand()

	found := false
	for _, cmd := range rootCmd.Commands() {
		if cmd.Use == "init" {
			found = true
			break
		}
	}

	if !found {
		t.Error("init command not registered on root command")
	}
}

// ---------------------------------------------------------------------------
// Tests for effectiveAWSProfile profile-selection logic (Bug #156)
// ---------------------------------------------------------------------------

// TestEffectiveAWSProfileConfigFallback verifies that when the --profile CLI
// flag is empty, effectiveAWSProfile returns the aws_profile from config.toml.
func TestEffectiveAWSProfileConfigFallback(t *testing.T) {
	cliCtx := &cli.CLIContext{Profile: ""} // no --profile flag
	mintConfig := &config.Config{AWSProfile: "config-profile"}

	got := effectiveAWSProfile(cliCtx, mintConfig)
	if got != "config-profile" {
		t.Errorf("effectiveAWSProfile = %q, want %q", got, "config-profile")
	}
}

// TestEffectiveAWSProfileCLIFlagPrecedence verifies that the --profile CLI
// flag takes precedence over aws_profile set in config.toml.
func TestEffectiveAWSProfileCLIFlagPrecedence(t *testing.T) {
	cliCtx := &cli.CLIContext{Profile: "flag-profile"} // --profile set
	mintConfig := &config.Config{AWSProfile: "config-profile"}

	got := effectiveAWSProfile(cliCtx, mintConfig)
	if got != "flag-profile" {
		t.Errorf("effectiveAWSProfile = %q, want %q", got, "flag-profile")
	}
}

// TestEffectiveAWSProfileBothEmpty verifies that when neither --profile nor
// config aws_profile is set, effectiveAWSProfile returns "", allowing the AWS
// SDK to resolve the profile through its own default chain.
func TestEffectiveAWSProfileBothEmpty(t *testing.T) {
	cliCtx := &cli.CLIContext{Profile: ""}
	mintConfig := &config.Config{AWSProfile: ""}

	got := effectiveAWSProfile(cliCtx, mintConfig)
	if got != "" {
		t.Errorf("effectiveAWSProfile = %q, want empty string", got)
	}
}

// TestEffectiveAWSProfileNilCLICtx verifies that a nil cliCtx falls back to
// the config aws_profile value without panicking.
func TestEffectiveAWSProfileNilCLICtx(t *testing.T) {
	mintConfig := &config.Config{AWSProfile: "config-profile"}

	got := effectiveAWSProfile(nil, mintConfig)
	if got != "config-profile" {
		t.Errorf("effectiveAWSProfile = %q, want %q", got, "config-profile")
	}
}

// TestEffectiveAWSProfileNilMintConfig verifies that a nil mintConfig falls
// back to the CLI flag value without panicking.
func TestEffectiveAWSProfileNilMintConfig(t *testing.T) {
	cliCtx := &cli.CLIContext{Profile: "flag-profile"}

	got := effectiveAWSProfile(cliCtx, nil)
	if got != "flag-profile" {
		t.Errorf("effectiveAWSProfile = %q, want %q", got, "flag-profile")
	}
}

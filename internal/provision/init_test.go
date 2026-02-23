package provision

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/efs"
	efstypes "github.com/aws/aws-sdk-go-v2/service/efs/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	smithy "github.com/aws/smithy-go"
)

// ---------------------------------------------------------------------------
// Inline mocks
// ---------------------------------------------------------------------------

type mockDescribeVpcs struct {
	output *ec2.DescribeVpcsOutput
	err    error
}

func (m *mockDescribeVpcs) DescribeVpcs(ctx context.Context, params *ec2.DescribeVpcsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error) {
	return m.output, m.err
}

type mockDescribeSubnets struct {
	output *ec2.DescribeSubnetsOutput
	err    error
}

func (m *mockDescribeSubnets) DescribeSubnets(ctx context.Context, params *ec2.DescribeSubnetsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error) {
	return m.output, m.err
}

type mockDescribeFileSystems struct {
	output *efs.DescribeFileSystemsOutput
	err    error
}

func (m *mockDescribeFileSystems) DescribeFileSystems(ctx context.Context, params *efs.DescribeFileSystemsInput, optFns ...func(*efs.Options)) (*efs.DescribeFileSystemsOutput, error) {
	return m.output, m.err
}

type mockDescribeSecurityGroups struct {
	output *ec2.DescribeSecurityGroupsOutput
	err    error
}

func (m *mockDescribeSecurityGroups) DescribeSecurityGroups(ctx context.Context, params *ec2.DescribeSecurityGroupsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error) {
	return m.output, m.err
}

type mockCreateSecurityGroup struct {
	output *ec2.CreateSecurityGroupOutput
	err    error
}

func (m *mockCreateSecurityGroup) CreateSecurityGroup(ctx context.Context, params *ec2.CreateSecurityGroupInput, optFns ...func(*ec2.Options)) (*ec2.CreateSecurityGroupOutput, error) {
	return m.output, m.err
}

type mockAuthorizeIngress struct {
	output *ec2.AuthorizeSecurityGroupIngressOutput
	err    error
}

func (m *mockAuthorizeIngress) AuthorizeSecurityGroupIngress(ctx context.Context, params *ec2.AuthorizeSecurityGroupIngressInput, optFns ...func(*ec2.Options)) (*ec2.AuthorizeSecurityGroupIngressOutput, error) {
	return m.output, m.err
}

type mockCreateTags struct {
	output *ec2.CreateTagsOutput
	err    error
}

func (m *mockCreateTags) CreateTags(ctx context.Context, params *ec2.CreateTagsInput, optFns ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error) {
	return m.output, m.err
}

type mockDescribeAccessPoints struct {
	output *efs.DescribeAccessPointsOutput
	err    error
}

func (m *mockDescribeAccessPoints) DescribeAccessPoints(ctx context.Context, params *efs.DescribeAccessPointsInput, optFns ...func(*efs.Options)) (*efs.DescribeAccessPointsOutput, error) {
	return m.output, m.err
}

type mockCreateAccessPoint struct {
	output *efs.CreateAccessPointOutput
	err    error
}

func (m *mockCreateAccessPoint) CreateAccessPoint(ctx context.Context, params *efs.CreateAccessPointInput, optFns ...func(*efs.Options)) (*efs.CreateAccessPointOutput, error) {
	return m.output, m.err
}

type mockGetInstanceProfile struct {
	output *iam.GetInstanceProfileOutput
	err    error
}

func (m *mockGetInstanceProfile) GetInstanceProfile(ctx context.Context, params *iam.GetInstanceProfileInput, optFns ...func(*iam.Options)) (*iam.GetInstanceProfileOutput, error) {
	return m.output, m.err
}

// ---------------------------------------------------------------------------
// Helper: build an Initializer with all mocks wired up
// ---------------------------------------------------------------------------

type initMocks struct {
	vpcs            *mockDescribeVpcs
	subnets         *mockDescribeSubnets
	fileSystems     *mockDescribeFileSystems
	instanceProfile *mockGetInstanceProfile
	describeSGs     *mockDescribeSecurityGroups
	createSG        *mockCreateSecurityGroup
	authorizeIn     *mockAuthorizeIngress
	createTags      *mockCreateTags
	describeAPs     *mockDescribeAccessPoints
	createAP        *mockCreateAccessPoint
}

func newHappyMocks() *initMocks {
	vpcID := "vpc-abc123"
	return &initMocks{
		vpcs: &mockDescribeVpcs{
			output: &ec2.DescribeVpcsOutput{
				Vpcs: []ec2types.Vpc{
					{VpcId: aws.String(vpcID), IsDefault: aws.Bool(true)},
				},
			},
		},
		subnets: &mockDescribeSubnets{
			output: &ec2.DescribeSubnetsOutput{
				Subnets: []ec2types.Subnet{
					{SubnetId: aws.String("subnet-1"), MapPublicIpOnLaunch: aws.Bool(true)},
				},
			},
		},
		fileSystems: &mockDescribeFileSystems{
			output: &efs.DescribeFileSystemsOutput{
				FileSystems: []efstypes.FileSystemDescription{
					{
						FileSystemId: aws.String("fs-12345"),
						Tags: []efstypes.Tag{
							{Key: aws.String("mint"), Value: aws.String("true")},
							{Key: aws.String("mint:component"), Value: aws.String("admin")},
						},
					},
				},
			},
		},
		instanceProfile: &mockGetInstanceProfile{
			output: &iam.GetInstanceProfileOutput{
				InstanceProfile: &iamtypes.InstanceProfile{
					InstanceProfileName: aws.String("mint-instance-profile"),
				},
			},
		},
		describeSGs: &mockDescribeSecurityGroups{
			output: &ec2.DescribeSecurityGroupsOutput{
				SecurityGroups: []ec2types.SecurityGroup{},
			},
		},
		createSG: &mockCreateSecurityGroup{
			output: &ec2.CreateSecurityGroupOutput{
				GroupId: aws.String("sg-new123"),
			},
		},
		authorizeIn: &mockAuthorizeIngress{
			output: &ec2.AuthorizeSecurityGroupIngressOutput{},
		},
		createTags: &mockCreateTags{
			output: &ec2.CreateTagsOutput{},
		},
		describeAPs: &mockDescribeAccessPoints{
			output: &efs.DescribeAccessPointsOutput{
				AccessPoints: []efstypes.AccessPointDescription{},
			},
		},
		createAP: &mockCreateAccessPoint{
			output: &efs.CreateAccessPointOutput{
				AccessPointId: aws.String("fsap-new123"),
			},
		},
	}
}

func (m *initMocks) build() *Initializer {
	return NewInitializer(
		m.vpcs,
		m.subnets,
		m.fileSystems,
		m.instanceProfile,
		m.describeSGs,
		m.createSG,
		m.authorizeIn,
		m.createTags,
		m.describeAPs,
		m.createAP,
	)
}

// ---------------------------------------------------------------------------
// Tests: VPC validation
// ---------------------------------------------------------------------------

func TestValidateVPC(t *testing.T) {
	tests := []struct {
		name    string
		vpcs    *mockDescribeVpcs
		subnets *mockDescribeSubnets
		wantErr string
	}{
		{
			name: "default VPC found with public subnet",
			vpcs: &mockDescribeVpcs{
				output: &ec2.DescribeVpcsOutput{
					Vpcs: []ec2types.Vpc{
						{VpcId: aws.String("vpc-abc"), IsDefault: aws.Bool(true)},
					},
				},
			},
			subnets: &mockDescribeSubnets{
				output: &ec2.DescribeSubnetsOutput{
					Subnets: []ec2types.Subnet{
						{SubnetId: aws.String("subnet-1"), MapPublicIpOnLaunch: aws.Bool(true)},
					},
				},
			},
		},
		{
			name: "no default VPC",
			vpcs: &mockDescribeVpcs{
				output: &ec2.DescribeVpcsOutput{
					Vpcs: []ec2types.Vpc{},
				},
			},
			subnets: &mockDescribeSubnets{
				output: &ec2.DescribeSubnetsOutput{},
			},
			wantErr: "no default VPC found",
		},
		{
			name: "VPC API error",
			vpcs: &mockDescribeVpcs{
				err: errors.New("access denied"),
			},
			subnets: &mockDescribeSubnets{},
			wantErr: "describe VPCs",
		},
		{
			name: "no public subnets",
			vpcs: &mockDescribeVpcs{
				output: &ec2.DescribeVpcsOutput{
					Vpcs: []ec2types.Vpc{
						{VpcId: aws.String("vpc-abc"), IsDefault: aws.Bool(true)},
					},
				},
			},
			subnets: &mockDescribeSubnets{
				output: &ec2.DescribeSubnetsOutput{
					Subnets: []ec2types.Subnet{
						{SubnetId: aws.String("subnet-1"), MapPublicIpOnLaunch: aws.Bool(false)},
					},
				},
			},
			wantErr: "no public subnets",
		},
		{
			name: "subnets API error",
			vpcs: &mockDescribeVpcs{
				output: &ec2.DescribeVpcsOutput{
					Vpcs: []ec2types.Vpc{
						{VpcId: aws.String("vpc-abc"), IsDefault: aws.Bool(true)},
					},
				},
			},
			subnets: &mockDescribeSubnets{
				err: errors.New("throttled"),
			},
			wantErr: "describe subnets",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newHappyMocks()
			m.vpcs = tt.vpcs
			m.subnets = tt.subnets
			init := m.build()

			_, err := init.validateVPC(context.Background())

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
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
// Tests: EFS discovery
// ---------------------------------------------------------------------------

func TestDiscoverEFS(t *testing.T) {
	tests := []struct {
		name    string
		fs      *mockDescribeFileSystems
		wantErr string
		wantID  string
	}{
		{
			name: "admin EFS found",
			fs: &mockDescribeFileSystems{
				output: &efs.DescribeFileSystemsOutput{
					FileSystems: []efstypes.FileSystemDescription{
						{
							FileSystemId: aws.String("fs-admin1"),
							Tags: []efstypes.Tag{
								{Key: aws.String("mint"), Value: aws.String("true")},
								{Key: aws.String("mint:component"), Value: aws.String("admin")},
							},
						},
					},
				},
			},
			wantID: "fs-admin1",
		},
		{
			name: "no admin EFS",
			fs: &mockDescribeFileSystems{
				output: &efs.DescribeFileSystemsOutput{
					FileSystems: []efstypes.FileSystemDescription{},
				},
			},
			wantErr: "no admin EFS filesystem found",
		},
		{
			name: "EFS API error",
			fs: &mockDescribeFileSystems{
				err: errors.New("service unavailable"),
			},
			wantErr: "describe EFS file systems",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newHappyMocks()
			m.fileSystems = tt.fs
			init := m.build()

			fsID, err := init.discoverEFS(context.Background())

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if fsID != tt.wantID {
				t.Errorf("fsID = %q, want %q", fsID, tt.wantID)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Tests: Security group creation
// ---------------------------------------------------------------------------

func TestEnsureSecurityGroup(t *testing.T) {
	tests := []struct {
		name       string
		describeSG *mockDescribeSecurityGroups
		createSG   *mockCreateSecurityGroup
		authIn     *mockAuthorizeIngress
		createTags *mockCreateTags
		wantErr    string
		wantSkip   bool // true if SG already exists
	}{
		{
			name: "creates new security group",
			describeSG: &mockDescribeSecurityGroups{
				output: &ec2.DescribeSecurityGroupsOutput{
					SecurityGroups: []ec2types.SecurityGroup{},
				},
			},
			createSG: &mockCreateSecurityGroup{
				output: &ec2.CreateSecurityGroupOutput{
					GroupId: aws.String("sg-new"),
				},
			},
			authIn:     &mockAuthorizeIngress{output: &ec2.AuthorizeSecurityGroupIngressOutput{}},
			createTags: &mockCreateTags{output: &ec2.CreateTagsOutput{}},
		},
		{
			name: "skips existing security group",
			describeSG: &mockDescribeSecurityGroups{
				output: &ec2.DescribeSecurityGroupsOutput{
					SecurityGroups: []ec2types.SecurityGroup{
						{GroupId: aws.String("sg-existing")},
					},
				},
			},
			wantSkip: true,
		},
		{
			name: "describe SG API error",
			describeSG: &mockDescribeSecurityGroups{
				err: errors.New("describe failed"),
			},
			wantErr: "describe security groups",
		},
		{
			name: "create SG API error",
			describeSG: &mockDescribeSecurityGroups{
				output: &ec2.DescribeSecurityGroupsOutput{
					SecurityGroups: []ec2types.SecurityGroup{},
				},
			},
			createSG: &mockCreateSecurityGroup{
				err: errors.New("create failed"),
			},
			wantErr: "create security group",
		},
		{
			name: "authorize ingress API error",
			describeSG: &mockDescribeSecurityGroups{
				output: &ec2.DescribeSecurityGroupsOutput{
					SecurityGroups: []ec2types.SecurityGroup{},
				},
			},
			createSG: &mockCreateSecurityGroup{
				output: &ec2.CreateSecurityGroupOutput{
					GroupId: aws.String("sg-new"),
				},
			},
			authIn: &mockAuthorizeIngress{
				err: errors.New("auth failed"),
			},
			wantErr: "authorize ingress",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newHappyMocks()
			m.describeSGs = tt.describeSG
			if tt.createSG != nil {
				m.createSG = tt.createSG
			}
			if tt.authIn != nil {
				m.authorizeIn = tt.authIn
			}
			if tt.createTags != nil {
				m.createTags = tt.createTags
			}
			init := m.build()

			_, err := init.ensureSecurityGroup(context.Background(), "vpc-abc", "testowner", "arn:aws:iam::123456789012:user/testowner", "default")

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
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
// Tests: Access point creation
// ---------------------------------------------------------------------------

func TestEnsureAccessPoint(t *testing.T) {
	tests := []struct {
		name      string
		descAPs   *mockDescribeAccessPoints
		createAP  *mockCreateAccessPoint
		wantErr   string
		wantSkip  bool
	}{
		{
			name: "creates new access point",
			descAPs: &mockDescribeAccessPoints{
				output: &efs.DescribeAccessPointsOutput{
					AccessPoints: []efstypes.AccessPointDescription{},
				},
			},
			createAP: &mockCreateAccessPoint{
				output: &efs.CreateAccessPointOutput{
					AccessPointId: aws.String("fsap-new"),
				},
			},
		},
		{
			name: "skips existing access point",
			descAPs: &mockDescribeAccessPoints{
				output: &efs.DescribeAccessPointsOutput{
					AccessPoints: []efstypes.AccessPointDescription{
						{
							AccessPointId: aws.String("fsap-existing"),
							Tags: []efstypes.Tag{
								{Key: aws.String("mint"), Value: aws.String("true")},
								{Key: aws.String("mint:owner"), Value: aws.String("testowner")},
								{Key: aws.String("mint:component"), Value: aws.String("efs-access-point")},
							},
						},
					},
				},
			},
			wantSkip: true,
		},
		{
			name: "describe AP API error",
			descAPs: &mockDescribeAccessPoints{
				err: errors.New("describe failed"),
			},
			wantErr: "describe access points",
		},
		{
			name: "create AP API error",
			descAPs: &mockDescribeAccessPoints{
				output: &efs.DescribeAccessPointsOutput{
					AccessPoints: []efstypes.AccessPointDescription{},
				},
			},
			createAP: &mockCreateAccessPoint{
				err: errors.New("create failed"),
			},
			wantErr: "create access point",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newHappyMocks()
			m.describeAPs = tt.descAPs
			if tt.createAP != nil {
				m.createAP = tt.createAP
			}
			init := m.build()

			_, err := init.ensureAccessPointResult(context.Background(), "fs-12345", "testowner", "arn:aws:iam::123456789012:user/testowner", "default")

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
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
// Tests: Instance profile validation
// ---------------------------------------------------------------------------

func TestValidateInstanceProfile(t *testing.T) {
	tests := []struct {
		name    string
		mock    *mockGetInstanceProfile
		wantErr string
	}{
		{
			name: "instance profile exists",
			mock: &mockGetInstanceProfile{
				output: &iam.GetInstanceProfileOutput{
					InstanceProfile: &iamtypes.InstanceProfile{
						InstanceProfileName: aws.String("mint-instance-profile"),
					},
				},
			},
		},
		{
			name: "instance profile missing (NoSuchEntity)",
			mock: &mockGetInstanceProfile{
				err: &iamtypes.NoSuchEntityException{
					Message: aws.String("Instance Profile mint-instance-profile cannot be found"),
				},
			},
			wantErr: "instance profile \"mint-instance-profile\" not found",
		},
		{
			name: "IAM API error propagated",
			mock: &mockGetInstanceProfile{
				err: errors.New("access denied"),
			},
			wantErr: "get instance profile",
		},
		{
			name: "AccessDenied (403) warns and continues (no error)",
			mock: &mockGetInstanceProfile{
				err: &smithy.GenericAPIError{
					Code:    "AccessDenied",
					Message: "User: arn:aws:iam::123456789012:user/dev is not authorized to perform: iam:GetInstanceProfile",
				},
			},
			// AccessDenied is non-fatal: warning is printed, nil returned.
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newHappyMocks()
			m.instanceProfile = tt.mock
			init := m.build()

			err := init.validateInstanceProfile(context.Background())

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// TestValidateInstanceProfileAccessDeniedReturnsNil verifies that a 403
// AccessDenied from iam:GetInstanceProfile is non-fatal: validateInstanceProfile
// must return nil so that init continues. The warning is emitted via log.Printf
// (verified indirectly — log output goes to stderr in tests, not captured here).
func TestValidateInstanceProfileAccessDeniedReturnsNil(t *testing.T) {
	accessDeniedErr := &smithy.GenericAPIError{
		Code:    "AccessDenied",
		Message: "User is not authorized to perform: iam:GetInstanceProfile",
	}
	m := newHappyMocks()
	m.instanceProfile = &mockGetInstanceProfile{err: accessDeniedErr}
	init := m.build()

	err := init.validateInstanceProfile(context.Background())

	if err != nil {
		t.Errorf("AccessDenied should be non-fatal (warn and continue), got error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Tests: Instance profile — AccessDenied warns and continues
// ---------------------------------------------------------------------------

// TestValidateInstanceProfileAccessDeniedWarnsAndContinues verifies that an
// AccessDenied response from iam:GetInstanceProfile does NOT fail init — it
// emits a warning and returns nil so the user can continue.
func TestValidateInstanceProfileAccessDeniedWarnsAndContinues(t *testing.T) {
	accessDeniedErr := &smithy.GenericAPIError{
		Code:    "AccessDenied",
		Message: "User is not authorized to perform: iam:GetInstanceProfile",
	}
	m := newHappyMocks()
	m.instanceProfile = &mockGetInstanceProfile{err: accessDeniedErr}
	init := m.build()

	err := init.validateInstanceProfile(context.Background())

	if err != nil {
		t.Errorf("expected nil error for AccessDenied (should warn and continue), got: %v", err)
	}
}

// TestRunFullFlowAccessDeniedContinues verifies that AccessDenied on
// iam:GetInstanceProfile does not abort the full init flow.
func TestRunFullFlowAccessDeniedContinues(t *testing.T) {
	m := newHappyMocks()
	m.instanceProfile = &mockGetInstanceProfile{
		err: &smithy.GenericAPIError{
			Code:    "AccessDenied",
			Message: "User is not authorized to perform: iam:GetInstanceProfile",
		},
	}
	init := m.build()

	result, err := init.Run(context.Background(), "testowner", "arn:aws:iam::123456789012:user/testowner", "default")

	if err != nil {
		t.Errorf("expected nil error when AccessDenied on GetInstanceProfile, got: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result when AccessDenied on GetInstanceProfile")
	}
}

// ---------------------------------------------------------------------------
// Tests: Full init flow
// ---------------------------------------------------------------------------

func TestRunFullFlow(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(*initMocks)
		wantErr string
	}{
		{
			name:  "happy path - all resources created",
			setup: func(m *initMocks) {},
		},
		{
			name: "happy path - idempotent (SG and AP already exist)",
			setup: func(m *initMocks) {
				m.describeSGs.output = &ec2.DescribeSecurityGroupsOutput{
					SecurityGroups: []ec2types.SecurityGroup{
						{GroupId: aws.String("sg-existing")},
					},
				}
				m.describeAPs.output = &efs.DescribeAccessPointsOutput{
					AccessPoints: []efstypes.AccessPointDescription{
						{
							AccessPointId: aws.String("fsap-existing"),
							Tags: []efstypes.Tag{
								{Key: aws.String("mint"), Value: aws.String("true")},
								{Key: aws.String("mint:owner"), Value: aws.String("testowner")},
								{Key: aws.String("mint:component"), Value: aws.String("efs-access-point")},
							},
						},
					},
				}
			},
		},
		{
			name: "fails on VPC validation",
			setup: func(m *initMocks) {
				m.vpcs.output = &ec2.DescribeVpcsOutput{Vpcs: []ec2types.Vpc{}}
			},
			wantErr: "no default VPC found",
		},
		{
			name: "fails on EFS discovery",
			setup: func(m *initMocks) {
				m.fileSystems.output = &efs.DescribeFileSystemsOutput{
					FileSystems: []efstypes.FileSystemDescription{},
				}
			},
			wantErr: "no admin EFS filesystem found",
		},
		{
			name: "fails on instance profile validation",
			setup: func(m *initMocks) {
				m.instanceProfile.err = &iamtypes.NoSuchEntityException{
					Message: aws.String("not found"),
				}
				m.instanceProfile.output = nil
			},
			wantErr: "instance profile",
		},
		{
			name: "fails on SG creation",
			setup: func(m *initMocks) {
				m.createSG.err = errors.New("sg boom")
			},
			wantErr: "security group",
		},
		{
			name: "fails on access point creation",
			setup: func(m *initMocks) {
				m.createAP.err = errors.New("ap boom")
			},
			wantErr: "access point",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newHappyMocks()
			tt.setup(m)
			init := m.build()

			result, err := init.Run(context.Background(), "testowner", "arn:aws:iam::123456789012:user/testowner", "default")

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result == nil {
				t.Fatal("expected non-nil result on success")
			}
			if result.VPCID == "" {
				t.Error("result.VPCID should not be empty")
			}
			if result.EFSID == "" {
				t.Error("result.EFSID should not be empty")
			}
		})
	}
}


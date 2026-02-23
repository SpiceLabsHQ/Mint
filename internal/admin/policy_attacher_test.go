package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssoadmin"
	ssoadmintypes "github.com/aws/aws-sdk-go-v2/service/ssoadmin/types"
)

// ---------------------------------------------------------------------------
// Mock implementations
// ---------------------------------------------------------------------------

type mockListInstances struct {
	out *ssoadmin.ListInstancesOutput
	err error
}

func (m *mockListInstances) ListInstances(_ context.Context, _ *ssoadmin.ListInstancesInput, _ ...func(*ssoadmin.Options)) (*ssoadmin.ListInstancesOutput, error) {
	return m.out, m.err
}

// mockListPermSets supports multi-page responses. Each call to ListPermissionSets
// advances pages; the mock records calls for assertion.
type mockListPermSets struct {
	// pages is a list of per-page ARN slices. Index 0 is the first page.
	pages     [][]string
	callCount int
	err       error
}

func (m *mockListPermSets) ListPermissionSets(_ context.Context, in *ssoadmin.ListPermissionSetsInput, _ ...func(*ssoadmin.Options)) (*ssoadmin.ListPermissionSetsOutput, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.callCount >= len(m.pages) {
		return &ssoadmin.ListPermissionSetsOutput{}, nil
	}
	page := m.pages[m.callCount]
	m.callCount++

	var nextToken *string
	if m.callCount < len(m.pages) {
		nextToken = aws.String(fmt.Sprintf("token-%d", m.callCount))
	}
	return &ssoadmin.ListPermissionSetsOutput{
		PermissionSets: page,
		NextToken:      nextToken,
	}, nil
}

type mockDescribePermSet struct {
	// byARN maps permission-set ARN → PermissionSet name to return.
	byARN map[string]string
	err   error
}

func (m *mockDescribePermSet) DescribePermissionSet(_ context.Context, in *ssoadmin.DescribePermissionSetInput, _ ...func(*ssoadmin.Options)) (*ssoadmin.DescribePermissionSetOutput, error) {
	if m.err != nil {
		return nil, m.err
	}
	arn := aws.ToString(in.PermissionSetArn)
	name, ok := m.byARN[arn]
	if !ok {
		return &ssoadmin.DescribePermissionSetOutput{
			PermissionSet: &ssoadmintypes.PermissionSet{
				PermissionSetArn: aws.String(arn),
				Name:             aws.String("unknown"),
			},
		}, nil
	}
	return &ssoadmin.DescribePermissionSetOutput{
		PermissionSet: &ssoadmintypes.PermissionSet{
			PermissionSetArn: aws.String(arn),
			Name:             aws.String(name),
		},
	}, nil
}

type mockAttachPolicy struct {
	// called records whether the method was invoked.
	called bool
	err    error
}

func (m *mockAttachPolicy) AttachCustomerManagedPolicyReferenceToPermissionSet(_ context.Context, in *ssoadmin.AttachCustomerManagedPolicyReferenceToPermissionSetInput, _ ...func(*ssoadmin.Options)) (*ssoadmin.AttachCustomerManagedPolicyReferenceToPermissionSetOutput, error) {
	m.called = true
	return &ssoadmin.AttachCustomerManagedPolicyReferenceToPermissionSetOutput{}, m.err
}

type mockProvision struct {
	// called records whether the method was invoked.
	called bool
	status ssoadmintypes.StatusValues
	err    error
}

func (m *mockProvision) ProvisionPermissionSet(_ context.Context, _ *ssoadmin.ProvisionPermissionSetInput, _ ...func(*ssoadmin.Options)) (*ssoadmin.ProvisionPermissionSetOutput, error) {
	m.called = true
	return &ssoadmin.ProvisionPermissionSetOutput{
		PermissionSetProvisioningStatus: &ssoadmintypes.PermissionSetProvisioningStatus{
			Status: m.status,
		},
	}, m.err
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const (
	testInstanceARN   = "arn:aws:sso:::instance/ssoins-test123"
	testPermSetARN    = "arn:aws:sso:::permissionSet/ssoins-test123/ps-abc123"
	testPermSetName   = "PowerUserAccess"
	testPolicyName    = "mint-pass-instance-role"
)

// newTestAttacher constructs a PolicyAttacher wired to the provided mocks.
func newTestAttacher(
	instances *mockListInstances,
	permSets *mockListPermSets,
	describe *mockDescribePermSet,
	attach *mockAttachPolicy,
	provision *mockProvision,
) *PolicyAttacher {
	return NewPolicyAttacher(instances, permSets, describe, attach, provision)
}

// standardInstances returns a mockListInstances with one SSO instance.
func standardInstances() *mockListInstances {
	return &mockListInstances{
		out: &ssoadmin.ListInstancesOutput{
			Instances: []ssoadmintypes.InstanceMetadata{
				{InstanceArn: aws.String(testInstanceARN)},
			},
		},
	}
}

// standardPermSets returns a single-page mockListPermSets containing testPermSetARN.
func standardPermSets() *mockListPermSets {
	return &mockListPermSets{
		pages: [][]string{{testPermSetARN}},
	}
}

// standardDescribe returns a mockDescribePermSet that maps testPermSetARN → testPermSetName.
func standardDescribe() *mockDescribePermSet {
	return &mockDescribePermSet{
		byARN: map[string]string{testPermSetARN: testPermSetName},
	}
}

// ---------------------------------------------------------------------------
// Table-driven tests
// ---------------------------------------------------------------------------

func TestPolicyAttacher_Attach(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string

		// mock factories — each test configures its own mocks.
		instances *mockListInstances
		permSets  *mockListPermSets
		describe  *mockDescribePermSet
		attach    *mockAttachPolicy
		provision *mockProvision

		opts AttachOptions

		// expectations
		wantErr         error   // if non-nil, errors.Is is used
		wantErrContains string  // substring match for non-sentinel errors
		wantPermSetARN  string
		wantStatus      string
		wantAttachCalled   bool
		wantProvisionCalled bool
	}{
		{
			name:      "successful attach and reprovision",
			instances: standardInstances(),
			permSets:  standardPermSets(),
			describe:  standardDescribe(),
			attach:    &mockAttachPolicy{},
			provision: &mockProvision{status: ssoadmintypes.StatusValuesInProgress},
			opts: AttachOptions{
				PermissionSetName: testPermSetName,
				PolicyName:        testPolicyName,
			},
			wantPermSetARN:      testPermSetARN,
			wantStatus:          "IN_PROGRESS",
			wantAttachCalled:    true,
			wantProvisionCalled: true,
		},
		{
			name:      "already attached is idempotent — no error returned",
			instances: standardInstances(),
			permSets:  standardPermSets(),
			describe:  standardDescribe(),
			// AttachPolicy returns ConflictException (already attached).
			attach: &mockAttachPolicy{
				err: &ssoadmintypes.ConflictException{
					Message: aws.String("Policy is already attached"),
				},
			},
			provision: &mockProvision{status: ssoadmintypes.StatusValuesSucceeded},
			opts: AttachOptions{
				PermissionSetName: testPermSetName,
				PolicyName:        testPolicyName,
			},
			wantPermSetARN:      testPermSetARN,
			wantStatus:          "SUCCEEDED",
			wantAttachCalled:    true,
			wantProvisionCalled: true,
		},
		{
			name: "no SSO instance returns ErrNoSSOInstance",
			instances: &mockListInstances{
				out: &ssoadmin.ListInstancesOutput{Instances: []ssoadmintypes.InstanceMetadata{}},
			},
			permSets:  standardPermSets(),
			describe:  standardDescribe(),
			attach:    &mockAttachPolicy{},
			provision: &mockProvision{},
			opts: AttachOptions{
				PermissionSetName: testPermSetName,
				PolicyName:        testPolicyName,
			},
			wantErr:             ErrNoSSOInstance,
			wantAttachCalled:    false,
			wantProvisionCalled: false,
		},
		{
			name:      "permission set not found returns descriptive error",
			instances: standardInstances(),
			// One page with a single ARN that maps to a different name.
			permSets: &mockListPermSets{
				pages: [][]string{{"arn:aws:sso:::permissionSet/ssoins-test123/ps-other"}},
			},
			describe: &mockDescribePermSet{
				byARN: map[string]string{
					"arn:aws:sso:::permissionSet/ssoins-test123/ps-other": "OtherPermSet",
				},
			},
			attach:    &mockAttachPolicy{},
			provision: &mockProvision{},
			opts: AttachOptions{
				PermissionSetName: testPermSetName,
				PolicyName:        testPolicyName,
			},
			wantErrContains:     "permission set \"PowerUserAccess\" not found",
			wantAttachCalled:    false,
			wantProvisionCalled: false,
		},
		{
			name: "reprovision is always called on successful attach",
			// Same as the "successful" case but we explicitly verify ProvisionPermissionSet
			// is invoked even when the attach was a no-op (duplicate).
			instances: standardInstances(),
			permSets:  standardPermSets(),
			describe:  standardDescribe(),
			attach:    &mockAttachPolicy{},
			provision: &mockProvision{status: ssoadmintypes.StatusValuesSucceeded},
			opts: AttachOptions{
				PermissionSetName: testPermSetName,
				PolicyName:        testPolicyName,
			},
			wantPermSetARN:      testPermSetARN,
			wantStatus:          "SUCCEEDED",
			wantAttachCalled:    true,
			wantProvisionCalled: true,
		},
		{
			name:      "defaults applied when options are empty",
			instances: standardInstances(),
			permSets:  standardPermSets(),
			describe:  standardDescribe(),
			attach:    &mockAttachPolicy{},
			provision: &mockProvision{status: ssoadmintypes.StatusValuesInProgress},
			// opts is zero-value; defaults should apply.
			opts:                AttachOptions{},
			wantPermSetARN:      testPermSetARN,
			wantStatus:          "IN_PROGRESS",
			wantAttachCalled:    true,
			wantProvisionCalled: true,
		},
		{
			name: "ListInstances API error is propagated",
			instances: &mockListInstances{
				err: errors.New("network timeout"),
			},
			permSets:  standardPermSets(),
			describe:  standardDescribe(),
			attach:    &mockAttachPolicy{},
			provision: &mockProvision{},
			opts:      AttachOptions{PermissionSetName: testPermSetName},
			wantErrContains:     "list SSO instances",
			wantAttachCalled:    false,
			wantProvisionCalled: false,
		},
		{
			name:      "multi-page permission set pagination finds match on second page",
			instances: standardInstances(),
			permSets: &mockListPermSets{
				pages: [][]string{
					{"arn:aws:sso:::permissionSet/ssoins-test123/ps-page1a"},
					{testPermSetARN}, // match is on second page
				},
			},
			describe: &mockDescribePermSet{
				byARN: map[string]string{
					"arn:aws:sso:::permissionSet/ssoins-test123/ps-page1a": "SomethingElse",
					testPermSetARN: testPermSetName,
				},
			},
			attach:    &mockAttachPolicy{},
			provision: &mockProvision{status: ssoadmintypes.StatusValuesInProgress},
			opts: AttachOptions{
				PermissionSetName: testPermSetName,
				PolicyName:        testPolicyName,
			},
			wantPermSetARN:      testPermSetARN,
			wantStatus:          "IN_PROGRESS",
			wantAttachCalled:    true,
			wantProvisionCalled: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			attacher := newTestAttacher(tc.instances, tc.permSets, tc.describe, tc.attach, tc.provision)
			result, err := attacher.Attach(context.Background(), tc.opts)

			// Error assertions.
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("Attach() error = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if tc.wantErrContains != "" {
				if err == nil {
					t.Errorf("Attach() expected error containing %q, got nil", tc.wantErrContains)
					return
				}
				if !strings.Contains(err.Error(), tc.wantErrContains) {
					t.Errorf("Attach() error = %q, want it to contain %q", err.Error(), tc.wantErrContains)
				}
				// After a non-sentinel error, verify side-effect mocks.
				if tc.attach.called != tc.wantAttachCalled {
					t.Errorf("AttachPolicy called = %v, want %v", tc.attach.called, tc.wantAttachCalled)
				}
				if tc.provision.called != tc.wantProvisionCalled {
					t.Errorf("ProvisionPermissionSet called = %v, want %v", tc.provision.called, tc.wantProvisionCalled)
				}
				return
			}
			if err != nil {
				t.Fatalf("Attach() unexpected error: %v", err)
			}

			// Success assertions.
			if result == nil {
				t.Fatal("Attach() returned nil result on success")
			}
			if result.PermissionSetArn != tc.wantPermSetARN {
				t.Errorf("PermissionSetArn = %q, want %q", result.PermissionSetArn, tc.wantPermSetARN)
			}
			if result.ProvisioningStatus != tc.wantStatus {
				t.Errorf("ProvisioningStatus = %q, want %q", result.ProvisioningStatus, tc.wantStatus)
			}
			if tc.attach.called != tc.wantAttachCalled {
				t.Errorf("AttachPolicy called = %v, want %v", tc.attach.called, tc.wantAttachCalled)
			}
			if tc.provision.called != tc.wantProvisionCalled {
				t.Errorf("ProvisionPermissionSet called = %v, want %v", tc.provision.called, tc.wantProvisionCalled)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Additional unit tests for exported sentinel error
// ---------------------------------------------------------------------------

func TestErrNoSSOInstance_IsIdentifiable(t *testing.T) {
	t.Parallel()
	// Callers rely on errors.Is(err, ErrNoSSOInstance) for non-SSO fallback.
	wrapped := fmt.Errorf("outer: %w", ErrNoSSOInstance)
	if !errors.Is(wrapped, ErrNoSSOInstance) {
		t.Error("errors.Is should unwrap ErrNoSSOInstance through wrapping")
	}
}


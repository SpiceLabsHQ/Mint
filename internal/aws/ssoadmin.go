// Package aws provides thin wrappers around AWS SDK clients used by Mint.
// This file defines narrow interfaces for SSO Admin operations needed by the
// admin policy-attacher workflow. Each interface wraps exactly one AWS SDK
// method, enabling mock injection in tests.
package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/ssoadmin"
)

// ---------------------------------------------------------------------------
// SSO Admin interfaces
// ---------------------------------------------------------------------------

// ListSSOInstancesAPI defines the subset of the SSO Admin API used for
// discovering the account's SSO instance ARN and identity store ID. Used by
// the admin policy attacher to locate the SSO instance before operating on
// permission sets.
type ListSSOInstancesAPI interface {
	ListInstances(ctx context.Context, params *ssoadmin.ListInstancesInput, optFns ...func(*ssoadmin.Options)) (*ssoadmin.ListInstancesOutput, error)
}

// ListPermissionSetsAPI defines the subset of the SSO Admin API used for
// enumerating all permission sets provisioned in the SSO instance. Used by
// the admin policy attacher to find the target permission set by name.
type ListPermissionSetsAPI interface {
	ListPermissionSets(ctx context.Context, params *ssoadmin.ListPermissionSetsInput, optFns ...func(*ssoadmin.Options)) (*ssoadmin.ListPermissionSetsOutput, error)
}

// DescribePermissionSetAPI defines the subset of the SSO Admin API used for
// fetching the details (name, description, ARN) of a specific permission set.
// Used by the admin policy attacher to confirm the correct set is targeted.
type DescribePermissionSetAPI interface {
	DescribePermissionSet(ctx context.Context, params *ssoadmin.DescribePermissionSetInput, optFns ...func(*ssoadmin.Options)) (*ssoadmin.DescribePermissionSetOutput, error)
}

// AttachCustomerManagedPolicyReferenceAPI defines the subset of the SSO Admin
// API used for attaching a customer-managed IAM policy reference to a permission
// set. Used by the admin policy attacher to bind the Mint IAM policy to the
// target SSO permission set.
type AttachCustomerManagedPolicyReferenceAPI interface {
	AttachCustomerManagedPolicyReferenceToPermissionSet(ctx context.Context, params *ssoadmin.AttachCustomerManagedPolicyReferenceToPermissionSetInput, optFns ...func(*ssoadmin.Options)) (*ssoadmin.AttachCustomerManagedPolicyReferenceToPermissionSetOutput, error)
}

// ProvisionPermissionSetAPI defines the subset of the SSO Admin API used for
// propagating permission set changes to all assigned accounts and users. Used
// by the admin policy attacher after attaching a policy reference to ensure the
// change takes effect.
type ProvisionPermissionSetAPI interface {
	ProvisionPermissionSet(ctx context.Context, params *ssoadmin.ProvisionPermissionSetInput, optFns ...func(*ssoadmin.Options)) (*ssoadmin.ProvisionPermissionSetOutput, error)
}

// ---------------------------------------------------------------------------
// Compile-time interface satisfaction checks
// ---------------------------------------------------------------------------

var (
	_ ListSSOInstancesAPI                     = (*ssoadmin.Client)(nil)
	_ ListPermissionSetsAPI                   = (*ssoadmin.Client)(nil)
	_ DescribePermissionSetAPI                = (*ssoadmin.Client)(nil)
	_ AttachCustomerManagedPolicyReferenceAPI = (*ssoadmin.Client)(nil)
	_ ProvisionPermissionSetAPI               = (*ssoadmin.Client)(nil)
)

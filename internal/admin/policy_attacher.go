// Package admin provides administrative workflow implementations for Mint.
// These workflows are used by privileged operators to configure shared
// infrastructure (IAM Identity Center permission sets, instance profiles, etc.)
// before individual developers run `mint init`.
package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssoadmin"
	ssoadmintypes "github.com/aws/aws-sdk-go-v2/service/ssoadmin/types"

	mintaws "github.com/SpiceLabsHQ/Mint/internal/aws"
)

// ErrNoSSOInstance is returned by Attach when the AWS account has no IAM
// Identity Center instance. This is a graceful non-SSO fallback signal: callers
// should surface a clear message explaining that SSO is not configured and skip
// policy attachment rather than treating it as a fatal error.
var ErrNoSSOInstance = errors.New("no IAM Identity Center instance found")

// AttachOptions configures a single policy-attachment operation.
// Both fields carry opinionated defaults aligned with the Mint admin setup.
type AttachOptions struct {
	// PermissionSetName is the IAM Identity Center permission set to which the
	// customer-managed policy will be attached. Defaults to "PowerUserAccess".
	PermissionSetName string

	// PolicyName is the name of the customer-managed IAM policy that will be
	// attached to the permission set. Defaults to "mint-pass-instance-role".
	PolicyName string
}

// AttachResult holds the outcome of a successful Attach call.
type AttachResult struct {
	// PermissionSetArn is the full ARN of the permission set that was updated.
	PermissionSetArn string

	// ProvisioningStatus is the status value returned by ProvisionPermissionSet,
	// e.g. "IN_PROGRESS", "SUCCEEDED", or "FAILED".
	ProvisioningStatus string
}

// PolicyAttacher discovers the account's IAM Identity Center instance, locates
// a named permission set, attaches a customer-managed IAM policy reference to
// it, and triggers reprovisioning across all assigned accounts. All AWS
// dependencies are injected for testability.
type PolicyAttacher struct {
	listInstances  mintaws.ListSSOInstancesAPI
	listPermSets   mintaws.ListPermissionSetsAPI
	describePermSet mintaws.DescribePermissionSetAPI
	attachPolicy   mintaws.AttachCustomerManagedPolicyReferenceAPI
	provision      mintaws.ProvisionPermissionSetAPI
}

// NewPolicyAttacher constructs a PolicyAttacher with all required AWS
// interface dependencies. Pass nil only in production where the real
// ssoadmin.Client satisfies every interface.
func NewPolicyAttacher(
	listInstances mintaws.ListSSOInstancesAPI,
	listPermSets mintaws.ListPermissionSetsAPI,
	describePermSet mintaws.DescribePermissionSetAPI,
	attachPolicy mintaws.AttachCustomerManagedPolicyReferenceAPI,
	provision mintaws.ProvisionPermissionSetAPI,
) *PolicyAttacher {
	return &PolicyAttacher{
		listInstances:   listInstances,
		listPermSets:    listPermSets,
		describePermSet: describePermSet,
		attachPolicy:    attachPolicy,
		provision:       provision,
	}
}

// Attach performs the full policy-attachment workflow:
//
//  1. Discovers the IAM Identity Center instance ARN.
//  2. Paginates ListPermissionSets + DescribePermissionSet to locate the named
//     permission set.
//  3. Attaches the customer-managed policy reference (idempotent on conflict).
//  4. Triggers ProvisionPermissionSet for ALL_PROVISIONED_ACCOUNTS.
//
// If no SSO instance exists, Attach returns (nil, ErrNoSSOInstance) so callers
// can implement graceful non-SSO fallback without string-matching errors.
func (p *PolicyAttacher) Attach(ctx context.Context, opts AttachOptions) (*AttachResult, error) {
	// Apply defaults.
	if opts.PermissionSetName == "" {
		opts.PermissionSetName = "PowerUserAccess"
	}
	if opts.PolicyName == "" {
		opts.PolicyName = "mint-pass-instance-role"
	}

	// Step 1: discover SSO instance.
	instanceARN, err := p.discoverInstance(ctx)
	if err != nil {
		return nil, err
	}

	// Step 2: find the target permission set by name.
	permSetARN, err := p.findPermissionSet(ctx, instanceARN, opts.PermissionSetName)
	if err != nil {
		return nil, err
	}

	// Step 3: attach the customer-managed policy reference (idempotent).
	if err := p.attachPolicyReference(ctx, instanceARN, permSetARN, opts.PolicyName); err != nil {
		return nil, err
	}

	// Step 4: reprovision the permission set across all assigned accounts.
	status, err := p.reprovision(ctx, instanceARN, permSetARN)
	if err != nil {
		return nil, err
	}

	return &AttachResult{
		PermissionSetArn:   permSetARN,
		ProvisioningStatus: status,
	}, nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// discoverInstance calls ListInstances and returns the first instance ARN. It
// returns ErrNoSSOInstance when the account has no IAM Identity Center instance,
// enabling graceful non-SSO fallback in callers.
func (p *PolicyAttacher) discoverInstance(ctx context.Context) (string, error) {
	out, err := p.listInstances.ListInstances(ctx, &ssoadmin.ListInstancesInput{})
	if err != nil {
		return "", fmt.Errorf("list SSO instances: %w", err)
	}
	if len(out.Instances) == 0 {
		return "", ErrNoSSOInstance
	}
	return aws.ToString(out.Instances[0].InstanceArn), nil
}

// findPermissionSet paginates ListPermissionSets and calls DescribePermissionSet
// for each ARN until it finds one whose Name matches targetName. Returns a
// descriptive error when the permission set is not found after full pagination.
func (p *PolicyAttacher) findPermissionSet(ctx context.Context, instanceARN, targetName string) (string, error) {
	var nextToken *string
	for {
		listOut, err := p.listPermSets.ListPermissionSets(ctx, &ssoadmin.ListPermissionSetsInput{
			InstanceArn: aws.String(instanceARN),
			NextToken:   nextToken,
		})
		if err != nil {
			return "", fmt.Errorf("list permission sets: %w", err)
		}

		for _, arn := range listOut.PermissionSets {
			descOut, err := p.describePermSet.DescribePermissionSet(ctx, &ssoadmin.DescribePermissionSetInput{
				InstanceArn:      aws.String(instanceARN),
				PermissionSetArn: aws.String(arn),
			})
			if err != nil {
				return "", fmt.Errorf("describe permission set %s: %w", arn, err)
			}
			if descOut.PermissionSet != nil && aws.ToString(descOut.PermissionSet.Name) == targetName {
				return arn, nil
			}
		}

		if listOut.NextToken == nil || aws.ToString(listOut.NextToken) == "" {
			break
		}
		nextToken = listOut.NextToken
	}

	return "", fmt.Errorf("permission set %q not found in SSO instance %s; "+
		"ensure the permission set exists before running admin setup", targetName, instanceARN)
}

// attachPolicyReference calls AttachCustomerManagedPolicyReferenceToPermissionSet.
// A ConflictException is treated as success (idempotent): if the policy
// reference is already attached, no action is needed.
func (p *PolicyAttacher) attachPolicyReference(ctx context.Context, instanceARN, permSetARN, policyName string) error {
	_, err := p.attachPolicy.AttachCustomerManagedPolicyReferenceToPermissionSet(ctx,
		&ssoadmin.AttachCustomerManagedPolicyReferenceToPermissionSetInput{
			InstanceArn:      aws.String(instanceARN),
			PermissionSetArn: aws.String(permSetARN),
			CustomerManagedPolicyReference: &ssoadmintypes.CustomerManagedPolicyReference{
				Name: aws.String(policyName),
				Path: aws.String("/"),
			},
		},
	)
	if err != nil {
		// ConflictException means the policy reference is already attached —
		// treat as success for idempotent behaviour.
		var conflict *ssoadmintypes.ConflictException
		if errors.As(err, &conflict) {
			return nil
		}
		// Some AWS implementations surface duplicate-attachment as a validation
		// error with message containing "already attached". Accept those too.
		if strings.Contains(err.Error(), "already attached") {
			return nil
		}
		return fmt.Errorf("attach customer managed policy %q to permission set %s: %w",
			policyName, permSetARN, err)
	}
	return nil
}

// reprovision calls ProvisionPermissionSet targeting ALL_PROVISIONED_ACCOUNTS
// and returns the provisioning status string from the response.
func (p *PolicyAttacher) reprovision(ctx context.Context, instanceARN, permSetARN string) (string, error) {
	out, err := p.provision.ProvisionPermissionSet(ctx, &ssoadmin.ProvisionPermissionSetInput{
		InstanceArn:      aws.String(instanceARN),
		PermissionSetArn: aws.String(permSetARN),
		TargetType:       ssoadmintypes.ProvisionTargetTypeAllProvisionedAccounts,
	})
	if err != nil {
		return "", fmt.Errorf("provision permission set %s: %w", permSetARN, err)
	}

	var status string
	if out.PermissionSetProvisioningStatus != nil {
		status = string(out.PermissionSetProvisioningStatus.Status)
	}
	return status, nil
}

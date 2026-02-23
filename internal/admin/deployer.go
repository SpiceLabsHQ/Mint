package admin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	mintaws "github.com/nicholasgasior/mint/internal/aws"
)

// ErrVPCNotFound is returned when no default VPC exists in the region.
// The admin stack requires a default VPC for the EFS security group and
// mount targets (ADR-0010).
var ErrVPCNotFound = errors.New("no default VPC found in this region; " +
	"create one with: aws ec2 create-default-vpc")

// defaultStackName is used when DeployOptions.StackName is empty.
const defaultStackName = "mint-admin"

// pollInterval controls how frequently the deployer polls for stack events
// during create/update operations. Tests override this via the Deployer field.
const pollInterval = 5 * time.Second

// noUpdatesMsg is the substring CloudFormation returns in a ValidationError
// when a stack update produces no changes. Treated as a successful no-op.
const noUpdatesMsg = "No updates are to be performed"

// ---------------------------------------------------------------------------
// Public types
// ---------------------------------------------------------------------------

// DeployOptions configures a Deploy call.
type DeployOptions struct {
	// StackName is the CloudFormation stack name. Defaults to "mint-admin".
	StackName string

	// EventWriter receives formatted stack event lines during polling.
	// May be nil to suppress event output.
	EventWriter io.Writer
}

// DeployResult holds the stack outputs produced by a successful Deploy.
type DeployResult struct {
	StackName           string
	EfsFileSystemId     string
	EfsSecurityGroupId  string
	InstanceProfileArn  string
	PassRolePolicyArn   string
}

// ---------------------------------------------------------------------------
// Deployer
// ---------------------------------------------------------------------------

// Deployer manages the Mint admin CloudFormation stack. All AWS dependencies
// are injected via narrow interfaces for testability.
type Deployer struct {
	cfnCreate   mintaws.CreateStackAPI
	cfnUpdate   mintaws.UpdateStackAPI
	cfnDescribe mintaws.DescribeStacksAPI
	cfnEvents   mintaws.DescribeStackEventsAPI
	ec2Vpcs     mintaws.DescribeVpcsAPI
	ec2Subnets  mintaws.DescribeSubnetsAPI

	// pollInterval controls the delay between event-polling iterations.
	// Overridable in tests to avoid slow loops.
	pollInterval time.Duration

	// clock returns the current time. Injectable for tests.
	clock func() time.Time
}

// NewDeployer constructs a Deployer with all required AWS interfaces.
func NewDeployer(
	cfnCreate mintaws.CreateStackAPI,
	cfnUpdate mintaws.UpdateStackAPI,
	cfnDescribe mintaws.DescribeStacksAPI,
	cfnEvents mintaws.DescribeStackEventsAPI,
	ec2Vpcs mintaws.DescribeVpcsAPI,
	ec2Subnets mintaws.DescribeSubnetsAPI,
) *Deployer {
	return &Deployer{
		cfnCreate:    cfnCreate,
		cfnUpdate:    cfnUpdate,
		cfnDescribe:  cfnDescribe,
		cfnEvents:    cfnEvents,
		ec2Vpcs:      ec2Vpcs,
		ec2Subnets:   ec2Subnets,
		pollInterval: pollInterval,
		clock:        time.Now,
	}
}

// Deploy runs a full create-or-update lifecycle for the Mint admin stack:
//  1. Auto-discover the default VPC and list its subnets.
//  2. Check whether the stack already exists.
//  3. Route to CreateStack or UpdateStack accordingly.
//  4. Poll DescribeStackEvents, writing progress to opts.EventWriter.
//  5. Return stack outputs on success.
func (d *Deployer) Deploy(ctx context.Context, opts DeployOptions) (*DeployResult, error) {
	stackName := opts.StackName
	if stackName == "" {
		stackName = defaultStackName
	}

	// Step 1: Auto-discover default VPC.
	vpcID, subnetIDs, err := d.discoverVPCAndSubnets(ctx)
	if err != nil {
		return nil, err
	}

	// Step 2: Build CloudFormation parameters from discovered networking.
	params := buildParameters(vpcID, subnetIDs)

	// Step 3: Check stack existence and route to create or update.
	exists, err := d.stackExists(ctx, stackName)
	if err != nil {
		return nil, fmt.Errorf("check stack existence: %w", err)
	}

	// Record the deployment start time so we only stream events that occurred
	// after we initiated the operation.
	startTime := d.clock()

	if exists {
		err = d.updateStack(ctx, stackName, params)
	} else {
		err = d.createStack(ctx, stackName, params)
	}
	if err != nil {
		return nil, err
	}

	// Step 4: Poll events and wait for a terminal stack status.
	if err := d.waitAndStreamEvents(ctx, stackName, startTime, opts.EventWriter); err != nil {
		return nil, err
	}

	// Step 5: Retrieve and return stack outputs.
	return d.collectOutputs(ctx, stackName)
}

// ---------------------------------------------------------------------------
// VPC / subnet discovery
// ---------------------------------------------------------------------------

func (d *Deployer) discoverVPCAndSubnets(ctx context.Context) (string, []string, error) {
	out, err := d.ec2Vpcs.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("isDefault"), Values: []string{"true"}},
		},
	})
	if err != nil {
		return "", nil, fmt.Errorf("describe VPCs: %w", err)
	}
	if len(out.Vpcs) == 0 {
		return "", nil, ErrVPCNotFound
	}

	vpcID := aws.ToString(out.Vpcs[0].VpcId)

	subOut, err := d.ec2Subnets.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("vpc-id"), Values: []string{vpcID}},
		},
	})
	if err != nil {
		return "", nil, fmt.Errorf("describe subnets for VPC %s: %w", vpcID, err)
	}

	subnetIDs := make([]string, 0, len(subOut.Subnets))
	for _, s := range subOut.Subnets {
		subnetIDs = append(subnetIDs, aws.ToString(s.SubnetId))
	}

	return vpcID, subnetIDs, nil
}

// buildParameters maps the discovered VPC and subnets to CloudFormation
// parameters. The template supports up to 6 subnets (Subnet1–Subnet6).
func buildParameters(vpcID string, subnetIDs []string) []cftypes.Parameter {
	params := []cftypes.Parameter{
		{ParameterKey: aws.String("VpcId"), ParameterValue: aws.String(vpcID)},
	}

	for i := 1; i <= 6; i++ {
		key := fmt.Sprintf("Subnet%d", i)
		if i <= len(subnetIDs) {
			params = append(params, cftypes.Parameter{
				ParameterKey:   aws.String(key),
				ParameterValue: aws.String(subnetIDs[i-1]),
			})
		} else {
			// Pass empty string for optional subnets not present.
			params = append(params, cftypes.Parameter{
				ParameterKey:   aws.String(key),
				ParameterValue: aws.String(""),
			})
		}
	}

	return params
}

// ---------------------------------------------------------------------------
// Stack existence check
// ---------------------------------------------------------------------------

func (d *Deployer) stackExists(ctx context.Context, stackName string) (bool, error) {
	out, err := d.cfnDescribe.DescribeStacks(ctx, &cloudformation.DescribeStacksInput{
		StackName: aws.String(stackName),
	})
	if err != nil {
		// CloudFormation returns a ValidationError (400) when the stack does not
		// exist — treat that as "not found", not a hard error.
		if isStackDoesNotExistError(err) {
			return false, nil
		}
		return false, fmt.Errorf("describe stack %q: %w", stackName, err)
	}
	// A stack in DELETE_COMPLETE state is effectively gone.
	if len(out.Stacks) > 0 && out.Stacks[0].StackStatus == cftypes.StackStatusDeleteComplete {
		return false, nil
	}
	return len(out.Stacks) > 0, nil
}

// isStackDoesNotExistError returns true when CloudFormation signals that the
// named stack does not exist. The service returns a generic error message
// containing the stack name rather than a distinct error code.
func isStackDoesNotExistError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "does not exist") ||
		strings.Contains(msg, "Stack with id") ||
		strings.Contains(msg, "ValidationError")
}

// ---------------------------------------------------------------------------
// Create / update
// ---------------------------------------------------------------------------

func (d *Deployer) createStack(ctx context.Context, stackName string, params []cftypes.Parameter) error {
	_, err := d.cfnCreate.CreateStack(ctx, &cloudformation.CreateStackInput{
		StackName:    aws.String(stackName),
		TemplateBody: aws.String(adminTemplate),
		Parameters:   params,
		Capabilities: []cftypes.Capability{cftypes.CapabilityCapabilityNamedIam},
	})
	if err != nil {
		return fmt.Errorf("create stack %q: %w", stackName, err)
	}
	return nil
}

func (d *Deployer) updateStack(ctx context.Context, stackName string, params []cftypes.Parameter) error {
	_, err := d.cfnUpdate.UpdateStack(ctx, &cloudformation.UpdateStackInput{
		StackName:    aws.String(stackName),
		TemplateBody: aws.String(adminTemplate),
		Parameters:   params,
		Capabilities: []cftypes.Capability{cftypes.CapabilityCapabilityNamedIam},
	})
	if err != nil {
		// "No updates are to be performed" means the stack is already up to date.
		// Treat this as a successful idempotent no-op.
		if strings.Contains(err.Error(), noUpdatesMsg) {
			return nil
		}
		return fmt.Errorf("update stack %q: %w", stackName, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Event streaming + wait loop
// ---------------------------------------------------------------------------

// waitAndStreamEvents polls DescribeStackEvents until the stack reaches a
// terminal status, writing each new event to w. startTime is used to filter
// out events from previous operations.
func (d *Deployer) waitAndStreamEvents(ctx context.Context, stackName string, startTime time.Time, w io.Writer) error {
	seen := make(map[string]struct{})

	for {
		// Check for context cancellation before each poll.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Fetch current stack status.
		descOut, err := d.cfnDescribe.DescribeStacks(ctx, &cloudformation.DescribeStacksInput{
			StackName: aws.String(stackName),
		})
		if err != nil {
			return fmt.Errorf("poll stack status: %w", err)
		}
		if len(descOut.Stacks) == 0 {
			return fmt.Errorf("stack %q disappeared during polling", stackName)
		}

		status := descOut.Stacks[0].StackStatus

		// Stream any new events that occurred on or after startTime.
		if w != nil {
			if streamErr := d.streamNewEvents(ctx, stackName, startTime, seen, w); streamErr != nil {
				// Non-fatal: log but continue polling.
				fmt.Fprintf(w, "warning: could not fetch stack events: %v\n", streamErr)
			}
		}

		if isTerminalStatus(status) {
			if isFailedStatus(status) {
				return fmt.Errorf("stack %q reached failed status: %s", stackName, status)
			}
			return nil
		}

		// Wait before the next poll.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d.pollInterval):
		}
	}
}

// streamNewEvents fetches DescribeStackEvents and writes any unseen events
// that occurred at or after startTime to w.
func (d *Deployer) streamNewEvents(ctx context.Context, stackName string, startTime time.Time, seen map[string]struct{}, w io.Writer) error {
	evOut, err := d.cfnEvents.DescribeStackEvents(ctx, &cloudformation.DescribeStackEventsInput{
		StackName: aws.String(stackName),
	})
	if err != nil {
		return err
	}

	// Events are returned newest-first; collect the ones we haven't seen yet.
	var fresh []cftypes.StackEvent
	for _, ev := range evOut.StackEvents {
		id := aws.ToString(ev.EventId)
		if _, already := seen[id]; already {
			continue
		}
		if ev.Timestamp != nil && ev.Timestamp.Before(startTime) {
			continue
		}
		seen[id] = struct{}{}
		fresh = append(fresh, ev)
	}

	// Print in chronological order (oldest first).
	for i := len(fresh) - 1; i >= 0; i-- {
		ev := fresh[i]
		fmt.Fprintf(w, "%s  %-40s  %-30s  %s\n",
			aws.ToString(ev.EventId),
			aws.ToString(ev.LogicalResourceId),
			string(ev.ResourceStatus),
			aws.ToString(ev.ResourceStatusReason),
		)
	}

	return nil
}

// ---------------------------------------------------------------------------
// Terminal status helpers
// ---------------------------------------------------------------------------

func isTerminalStatus(s cftypes.StackStatus) bool {
	switch s {
	case cftypes.StackStatusCreateComplete,
		cftypes.StackStatusCreateFailed,
		cftypes.StackStatusUpdateComplete,
		cftypes.StackStatusUpdateFailed,
		cftypes.StackStatusUpdateRollbackComplete,
		cftypes.StackStatusUpdateRollbackFailed,
		cftypes.StackStatusRollbackComplete,
		cftypes.StackStatusRollbackFailed,
		cftypes.StackStatusDeleteComplete,
		cftypes.StackStatusDeleteFailed:
		return true
	}
	return false
}

func isFailedStatus(s cftypes.StackStatus) bool {
	switch s {
	case cftypes.StackStatusCreateFailed,
		cftypes.StackStatusUpdateFailed,
		cftypes.StackStatusUpdateRollbackFailed,
		cftypes.StackStatusRollbackFailed,
		cftypes.StackStatusDeleteFailed,
		cftypes.StackStatusRollbackComplete,
		cftypes.StackStatusUpdateRollbackComplete:
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Output collection
// ---------------------------------------------------------------------------

func (d *Deployer) collectOutputs(ctx context.Context, stackName string) (*DeployResult, error) {
	out, err := d.cfnDescribe.DescribeStacks(ctx, &cloudformation.DescribeStacksInput{
		StackName: aws.String(stackName),
	})
	if err != nil {
		return nil, fmt.Errorf("describe stack outputs: %w", err)
	}
	if len(out.Stacks) == 0 {
		return nil, fmt.Errorf("stack %q not found when collecting outputs", stackName)
	}

	result := &DeployResult{StackName: stackName}
	for _, o := range out.Stacks[0].Outputs {
		val := aws.ToString(o.OutputValue)
		switch aws.ToString(o.OutputKey) {
		case "EfsFileSystemId":
			result.EfsFileSystemId = val
		case "EfsSecurityGroupId":
			result.EfsSecurityGroupId = val
		case "InstanceProfileArn":
			result.InstanceProfileArn = val
		case "PassRolePolicyArn":
			result.PassRolePolicyArn = val
		}
	}

	return result, nil
}

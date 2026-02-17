// Package vm provides tag-based VM resource discovery for Mint.
//
// All discovery is performed via AWS EC2 tag filters (ADR-0001, ADR-0014).
// No local state files are consulted — AWS is the source of truth.
package vm

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	mintaws "github.com/nicholasgasior/mint/internal/aws"
	"github.com/nicholasgasior/mint/internal/tags"
)

// VM represents a Mint-managed EC2 instance.
type VM struct {
	ID              string
	Name            string
	State           string
	PublicIP        string
	InstanceType    string
	LaunchTime      time.Time
	BootstrapStatus string
	Tags            map[string]string
}

// FindVM discovers a single VM by owner and VM name. It returns nil (without
// error) when no matching instance is found, and an error when multiple
// non-terminated instances match.
func FindVM(ctx context.Context, client mintaws.DescribeInstancesAPI, owner, vmName string) (*VM, error) {
	vms, err := describeAndParse(ctx, client, tags.FilterByOwnerAndVM(owner, vmName))
	if err != nil {
		return nil, err
	}

	switch len(vms) {
	case 0:
		return nil, nil
	case 1:
		return vms[0], nil
	default:
		return nil, fmt.Errorf("multiple VMs found for owner %q, vm %q (%d instances)", owner, vmName, len(vms))
	}
}

// ListVMs discovers all VMs belonging to the given owner. Terminated and
// shutting-down instances are excluded.
func ListVMs(ctx context.Context, client mintaws.DescribeInstancesAPI, owner string) ([]*VM, error) {
	return describeAndParse(ctx, client, tags.FilterByOwner(owner))
}

// describeAndParse calls DescribeInstances with the given filters and converts
// the response into VM structs, filtering out terminated/shutting-down instances.
func describeAndParse(ctx context.Context, client mintaws.DescribeInstancesAPI, filters []ec2types.Filter) ([]*VM, error) {
	out, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: filters,
	})
	if err != nil {
		return nil, fmt.Errorf("describe instances: %w", err)
	}

	var vms []*VM
	for _, res := range out.Reservations {
		for _, inst := range res.Instances {
			if isExcludedState(inst.State) {
				continue
			}
			vms = append(vms, parseInstance(inst))
		}
	}

	return vms, nil
}

// isExcludedState returns true for instance states that should be filtered out.
func isExcludedState(state *ec2types.InstanceState) bool {
	if state == nil {
		return false
	}
	return state.Name == ec2types.InstanceStateNameTerminated ||
		state.Name == ec2types.InstanceStateNameShuttingDown
}

// parseInstance converts an EC2 instance into a VM struct.
func parseInstance(inst ec2types.Instance) *VM {
	tagMap := make(map[string]string, len(inst.Tags))
	for _, tag := range inst.Tags {
		tagMap[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}

	vm := &VM{
		ID:           aws.ToString(inst.InstanceId),
		State:        string(inst.State.Name),
		InstanceType: string(inst.InstanceType),
		Tags:         tagMap,
	}

	if inst.PublicIpAddress != nil {
		vm.PublicIP = aws.ToString(inst.PublicIpAddress)
	}
	if inst.LaunchTime != nil {
		vm.LaunchTime = *inst.LaunchTime
	}

	vm.Name = tagMap[tags.TagVM]
	vm.BootstrapStatus = tagMap[tags.TagBootstrap]

	return vm
}

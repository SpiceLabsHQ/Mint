package provision

import (
	"context"
	"fmt"
	"log"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	mintaws "github.com/nicholasgasior/mint/internal/aws"
	"github.com/nicholasgasior/mint/internal/tags"
	"github.com/nicholasgasior/mint/internal/vm"
)

// DestroyResult holds the outcome of a successful destroy run.
type DestroyResult struct {
	InstanceID     string
	VolumesDeleted int
	EIPReleased    bool
	Warnings       []string
}

// Destroyer terminates a VM and cleans up all associated resources.
// All AWS dependencies are injected via narrow interfaces for testability.
type Destroyer struct {
	describe        mintaws.DescribeInstancesAPI
	terminate       mintaws.TerminateInstancesAPI
	describeVolumes mintaws.DescribeVolumesAPI
	detachVolume    mintaws.DetachVolumeAPI
	deleteVolume    mintaws.DeleteVolumeAPI
	describeAddrs   mintaws.DescribeAddressesAPI
	releaseAddr     mintaws.ReleaseAddressAPI
}

// NewDestroyer creates a Destroyer with all required AWS interfaces.
func NewDestroyer(
	describe mintaws.DescribeInstancesAPI,
	terminate mintaws.TerminateInstancesAPI,
	describeVolumes mintaws.DescribeVolumesAPI,
	detachVolume mintaws.DetachVolumeAPI,
	deleteVolume mintaws.DeleteVolumeAPI,
	describeAddrs mintaws.DescribeAddressesAPI,
	releaseAddr mintaws.ReleaseAddressAPI,
) *Destroyer {
	return &Destroyer{
		describe:        describe,
		terminate:       terminate,
		describeVolumes: describeVolumes,
		detachVolume:    detachVolume,
		deleteVolume:    deleteVolume,
		describeAddrs:   describeAddrs,
		releaseAddr:     releaseAddr,
	}
}

// Run executes the full destroy flow. It requires confirmed=true to proceed.
func (d *Destroyer) Run(ctx context.Context, owner, vmName string, confirmed bool) error {
	_, err := d.RunWithResult(ctx, owner, vmName, confirmed)
	return err
}

// RunWithResult executes the full destroy flow and returns a result struct.
func (d *Destroyer) RunWithResult(ctx context.Context, owner, vmName string, confirmed bool) (*DestroyResult, error) {
	if !confirmed {
		return nil, fmt.Errorf("destroy not confirmed")
	}

	// Step 1: Discover VM by tags.
	found, err := vm.FindVM(ctx, d.describe, owner, vmName)
	if err != nil {
		return nil, fmt.Errorf("discovering VM: %w", err)
	}
	if found == nil {
		return nil, fmt.Errorf("no VM %q found for owner %q", vmName, owner)
	}

	result := &DestroyResult{
		InstanceID: found.ID,
	}

	// Step 2: Terminate instance (root EBS auto-destroys per ADR-0017).
	_, err = d.terminate.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{found.ID},
	})
	if err != nil {
		return nil, fmt.Errorf("terminating instance %s: %w", found.ID, err)
	}

	// Step 3: Discover and delete project EBS volumes.
	d.cleanupProjectVolumes(ctx, owner, vmName, result)

	// Step 4: Discover and release Elastic IP.
	d.cleanupElasticIP(ctx, owner, vmName, result)

	return result, nil
}

// cleanupProjectVolumes discovers project volumes by tags and deletes them.
// Errors are non-fatal: logged as warnings and added to result.
func (d *Destroyer) cleanupProjectVolumes(ctx context.Context, owner, vmName string, result *DestroyResult) {
	filters := append(
		tags.FilterByOwnerAndVM(owner, vmName),
		ec2types.Filter{
			Name:   aws.String("tag:" + tags.TagComponent),
			Values: []string{tags.ComponentProjectVolume},
		},
	)

	out, err := d.describeVolumes.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{
		Filters: filters,
	})
	if err != nil {
		warn := fmt.Sprintf("failed to discover project volumes: %v", err)
		result.Warnings = append(result.Warnings, warn)
		log.Println(warn)
		return
	}

	for _, vol := range out.Volumes {
		volID := aws.ToString(vol.VolumeId)

		// Detach if in-use.
		if vol.State == ec2types.VolumeStateInUse {
			_, err := d.detachVolume.DetachVolume(ctx, &ec2.DetachVolumeInput{
				VolumeId: aws.String(volID),
				Force:    aws.Bool(true),
			})
			if err != nil {
				warn := fmt.Sprintf("failed to detach volume %s: %v", volID, err)
				result.Warnings = append(result.Warnings, warn)
				log.Println(warn)
				// Continue to attempt delete anyway.
			}
		}

		// Delete the volume.
		_, err := d.deleteVolume.DeleteVolume(ctx, &ec2.DeleteVolumeInput{
			VolumeId: aws.String(volID),
		})
		if err != nil {
			warn := fmt.Sprintf("failed to delete volume %s: %v", volID, err)
			result.Warnings = append(result.Warnings, warn)
			log.Println(warn)
			continue
		}

		result.VolumesDeleted++
	}
}

// cleanupElasticIP discovers the Elastic IP by tags and releases it.
// Errors are non-fatal: logged as warnings and added to result.
func (d *Destroyer) cleanupElasticIP(ctx context.Context, owner, vmName string, result *DestroyResult) {
	filters := append(
		tags.FilterByOwnerAndVM(owner, vmName),
		ec2types.Filter{
			Name:   aws.String("tag:" + tags.TagComponent),
			Values: []string{tags.ComponentElasticIP},
		},
	)

	out, err := d.describeAddrs.DescribeAddresses(ctx, &ec2.DescribeAddressesInput{
		Filters: filters,
	})
	if err != nil {
		warn := fmt.Sprintf("failed to discover Elastic IP: %v", err)
		result.Warnings = append(result.Warnings, warn)
		log.Println(warn)
		return
	}

	for _, addr := range out.Addresses {
		allocID := aws.ToString(addr.AllocationId)
		_, err := d.releaseAddr.ReleaseAddress(ctx, &ec2.ReleaseAddressInput{
			AllocationId: aws.String(allocID),
		})
		if err != nil {
			warn := fmt.Sprintf("failed to release Elastic IP %s: %v", allocID, err)
			result.Warnings = append(result.Warnings, warn)
			log.Println(warn)
			continue
		}
		result.EIPReleased = true
	}
}

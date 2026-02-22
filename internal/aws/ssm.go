// Package aws provides thin wrappers around AWS SDK clients used by Mint.
// This file implements AMI resolution via EC2 DescribeImages using Canonical's
// published owner ID, with no dependency on SSM.
package aws

import (
	"context"
	"fmt"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// canonicalOwnerID is Canonical Ltd's AWS account ID, used to filter for
// official Ubuntu AMIs without relying on SSM public parameters.
const canonicalOwnerID = "099720109477"

// ubuntuAMINameFilter matches Ubuntu 24.04 LTS (Noble Numbat) GP3 AMIs
// published by Canonical.
const ubuntuAMINameFilter = "ubuntu/images/hvm-ssd-gp3/ubuntu-noble-24.04-amd64-server-*"

// Compile-time interface satisfaction check.
var _ DescribeImagesAPI = (*ec2.Client)(nil)

// ResolveAMI finds the most recent Ubuntu 24.04 LTS AMI by querying EC2
// DescribeImages with Canonical's owner ID. No SSM access required.
func ResolveAMI(ctx context.Context, client DescribeImagesAPI) (string, error) {
	out, err := client.DescribeImages(ctx, &ec2.DescribeImagesInput{
		Owners: []string{canonicalOwnerID},
		Filters: []ec2types.Filter{
			{Name: aws.String("name"), Values: []string{ubuntuAMINameFilter}},
			{Name: aws.String("state"), Values: []string{"available"}},
		},
	})
	if err != nil {
		return "", fmt.Errorf("describe images: %w", err)
	}

	if len(out.Images) == 0 {
		return "", fmt.Errorf("no Ubuntu 24.04 LTS AMIs found in this region (owner %s)", canonicalOwnerID)
	}

	// Sort descending by CreationDate to pick the most recent AMI.
	sort.Slice(out.Images, func(i, j int) bool {
		return aws.ToString(out.Images[i].CreationDate) > aws.ToString(out.Images[j].CreationDate)
	})

	return aws.ToString(out.Images[0].ImageId), nil
}

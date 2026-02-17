# Admin Setup

One-time setup per AWS account and region. Creates shared infrastructure that all Mint users depend on.

## Prerequisites

- AWS account with administrator access (IAM permissions to create roles, instance profiles, EFS filesystems, and security groups)
- AWS CLI v2 installed and configured (`aws configure` or environment variables)
- Default VPC present in the target region (AWS creates one by default; if deleted, recreate it with `aws ec2 create-default-vpc`)

## Deploy

First, gather your VPC and subnet IDs:

```bash
# Get your default VPC ID
VPC_ID=$(aws ec2 describe-vpcs \
  --filters Name=isDefault,Values=true \
  --query 'Vpcs[0].VpcId' \
  --output text)
echo "VPC: $VPC_ID"

# List subnets in the default VPC
aws ec2 describe-subnets \
  --filters "Name=vpc-id,Values=$VPC_ID" \
  --query 'Subnets[*].[SubnetId,AvailabilityZone]' \
  --output table
```

Then deploy the stack, passing each subnet as a parameter. A typical 3-AZ region looks like this:

```bash
aws cloudformation deploy \
  --template-file deploy/cloudformation/admin-setup.yaml \
  --stack-name mint-admin \
  --capabilities CAPABILITY_NAMED_IAM \
  --parameter-overrides \
    VpcId="$VPC_ID" \
    Subnet1="subnet-aaaa1111" \
    Subnet2="subnet-bbbb2222" \
    Subnet3="subnet-cccc3333"
```

Replace the subnet IDs with the values from the previous command. Only `Subnet1` is required; provide as many as your region has (up to 6). EFS mount targets are created in each subnet so VMs in any AZ can access the filesystem.

## Verify

Check that the stack deployed successfully and note the outputs:

```bash
# Stack status should be CREATE_COMPLETE or UPDATE_COMPLETE
aws cloudformation describe-stacks \
  --stack-name mint-admin \
  --query 'Stacks[0].StackStatus' \
  --output text

# View the outputs (EFS ID, security group ID, instance profile ARN)
aws cloudformation describe-stacks \
  --stack-name mint-admin \
  --query 'Stacks[0].Outputs' \
  --output table
```

You can also verify individual resources:

```bash
# IAM instance profile exists
aws iam get-instance-profile \
  --instance-profile-name mint-instance-profile \
  --query 'InstanceProfile.Arn' \
  --output text

# EFS filesystem exists and is tagged
aws efs describe-file-systems \
  --query 'FileSystems[?Name==`mint-efs`].[FileSystemId,LifeCycleState]' \
  --output table

# Security group exists with self-referencing NFS rule
aws ec2 describe-security-groups \
  --filters Name=group-name,Values=mint-efs \
  --query 'SecurityGroups[0].[GroupId,Description]' \
  --output text
```

## What Gets Created

| Resource | Name | Purpose |
|----------|------|---------|
| IAM Role | `mint-instance-role` | Attached to every Mint VM. Allows self-stop when idle (ADR-0018), self-tagging for bootstrap verification (ADR-0009), EFS mount access, and CloudWatch Logs. Scoped to resources tagged `mint=true`. |
| Instance Profile | `mint-instance-profile` | Wraps the IAM role. Passed to EC2 instances at launch by `mint up`. |
| EFS Filesystem | `mint-efs` | Encrypted, elastic throughput. Persistent storage for user configuration (dotfiles, SSH keys, Claude Code auth state). Mounted at `/mint/user` on every VM. Files transition to Infrequent Access after 30 days. |
| Security Group | `mint-efs` | Self-referencing NFS rule (TCP 2049). Only instances in this group can reach the EFS mount targets. Attached to every Mint VM at launch. |
| EFS Mount Targets | (one per subnet) | Place EFS endpoints in each AZ so VMs can mount the filesystem regardless of which AZ they launch in. |

All resources are tagged with `mint=true` and `mint:component=admin` for identification and cost tracking.

## Tear Down

Deleting the stack removes all admin-created resources. User data stored on EFS will be lost.

```bash
aws cloudformation delete-stack --stack-name mint-admin

# Wait for deletion to complete
aws cloudformation wait stack-delete-complete --stack-name mint-admin
```

**Before deleting**, ensure:
- All Mint VMs are destroyed (`mint destroy` for each VM)
- No EFS access points remain (created by `mint init` per user) -- delete them first with `aws efs delete-access-point`
- The EFS filesystem has no remaining mount targets outside this stack

If the delete fails due to a non-empty EFS filesystem or active mount targets, CloudFormation will report the specific resource blocking deletion.

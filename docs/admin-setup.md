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

# EFS filesystem exists and is available
# LifeCycleState is the resource availability state (available/creating), not the lifecycle policy
aws efs describe-file-systems \
  --query 'FileSystems[?Name==`mint-efs`].[FileSystemId,LifeCycleState]' \
  --output table

# EFS lifecycle policy — use describe-lifecycle-configuration (separate API from describe-file-systems)
# Expected output: {"LifecyclePolicies":[{"TransitionToIA":"AFTER_30_DAYS"}]}
EFS_ID=$(aws efs describe-file-systems \
  --query 'FileSystems[?Name==`mint-efs`].FileSystemId' \
  --output text)
aws efs describe-lifecycle-configuration --file-system-id "$EFS_ID"

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

## IAM Policy Reference

The CloudFormation template creates an IAM role (`mint-instance-role`) attached to every Mint VM via an instance profile. This role grants the minimum permissions a VM needs at runtime -- it cannot launch instances, create resources, or access other AWS services.

The standalone policy JSON is available at [`docs/iam-policy.json`](iam-policy.json) for teams that prefer to manage IAM policies outside CloudFormation. Replace the placeholder values (`REGION`, `ACCOUNT_ID`, `EFS_FILESYSTEM_ID`) with your actual values before attaching.

### Permission groups

**SelfStop -- EC2 idle auto-stop (ADR-0018)**

```json
{
  "Sid": "SelfStop",
  "Effect": "Allow",
  "Action": ["ec2:StopInstances"],
  "Resource": "arn:aws:ec2:REGION:ACCOUNT_ID:instance/*",
  "Condition": {
    "StringEquals": { "aws:ResourceTag/mint": "true" }
  }
}
```

Allows a VM to stop itself when the idle detection timer fires. The `aws:ResourceTag/mint` condition restricts this to instances tagged `mint=true`, preventing the VM from stopping arbitrary EC2 instances. The idle detector is a systemd timer that checks for active SSH/mosh sessions, tmux clients, and running `claude` processes before stopping the instance.

**DescribeResources -- read-only EC2 queries**

```json
{
  "Sid": "DescribeResources",
  "Effect": "Allow",
  "Action": ["ec2:DescribeInstances", "ec2:DescribeVolumes", "ec2:DescribeTags"],
  "Resource": "*"
}
```

Read-only access for the VM to discover its own metadata, attached volumes, and tags. EC2 Describe actions require `Resource: "*"` -- AWS does not support resource-level restrictions on Describe calls. These are strictly read-only and cannot modify any resources.

**CreateTags -- bootstrap and health tagging (ADR-0009, ADR-0018)**

```json
{
  "Sid": "CreateTags",
  "Effect": "Allow",
  "Action": ["ec2:CreateTags"],
  "Resource": "arn:aws:ec2:REGION:ACCOUNT_ID:instance/*",
  "Condition": {
    "StringEquals": { "aws:ResourceTag/mint": "true" }
  }
}
```

Allows the VM to tag itself during bootstrap (writing `mint:bootstrap=done` after successful initialization) and for health reporting. Restricted to instances already tagged `mint=true` via the same condition as SelfStop.

**EfsAccess -- persistent user storage (ADR-0004)**

```json
{
  "Sid": "EfsAccess",
  "Effect": "Allow",
  "Action": [
    "elasticfilesystem:ClientMount",
    "elasticfilesystem:ClientWrite",
    "elasticfilesystem:ClientRootAccess"
  ],
  "Resource": "arn:aws:elasticfilesystem:REGION:ACCOUNT_ID:file-system/EFS_FILESYSTEM_ID"
}
```

Grants mount, write, and root access to the specific Mint EFS filesystem. The resource ARN is scoped to the exact filesystem created by this stack -- VMs cannot access other EFS filesystems in the account. `ClientRootAccess` is required because the bootstrap script creates per-user directories on the filesystem before EFS access points are configured.

**CloudWatchLogs -- operational visibility**

```json
{
  "Sid": "CloudWatchLogs",
  "Effect": "Allow",
  "Action": ["logs:CreateLogGroup", "logs:CreateLogStream", "logs:PutLogEvents"],
  "Resource": "arn:aws:logs:REGION:ACCOUNT_ID:log-group:/mint/*"
}
```

Allows VMs to write structured logs to CloudWatch under the `/mint/` log group prefix. Scoped to Mint's log groups only. This provides centralized operational visibility across all VMs without requiring SSH access to read logs.

### Security model and scoping

This policy follows the **trusted-team security model** (ADR-0005). Key design decisions:

- **Tag-based conditions** on mutating actions (StopInstances, CreateTags) restrict operations to resources tagged `mint=true`. A VM cannot stop or tag non-Mint instances.
- **Describe actions use `Resource: "*"`** because AWS does not support resource-level restrictions on EC2 Describe API calls. This is an AWS limitation, not an oversight. These calls are read-only.
- **EFS access is scoped to a single filesystem** by ARN. The VM cannot mount or write to any other EFS filesystem in the account.
- **CloudWatch Logs are scoped to `/mint/*`** log groups. The VM cannot read or write logs outside this prefix.
- **No launch permissions.** The VM role cannot start, terminate, or create EC2 instances. It can only stop instances (and only Mint-tagged ones).
- **No IAM permissions.** The VM role cannot create, modify, or assume other IAM roles.
- **No S3, DynamoDB, or other service access.** The role is limited to EC2 (describe + conditional stop/tag), EFS (single filesystem), and CloudWatch Logs (single prefix).

### v2 hardening plan

For teams requiring stronger isolation, v2 will add IAM permission boundaries that enforce `aws:ResourceTag/mint:owner` conditions. This will prevent a VM from stopping or tagging another user's VM, even via direct AWS API calls. The current architecture supports this without code changes -- it requires only an IAM policy update. See ADR-0005 for details.

## Homebrew Distribution

Mint is distributed via a Homebrew tap hosted at **SpiceLabsHQ/homebrew-mint**. GoReleaser automatically commits an updated formula to that repository on every tagged release.

### Prerequisites

The tap repository must exist before the first release:

1. Create `SpiceLabsHQ/homebrew-mint` as a public GitHub repository (no initial files needed — GoReleaser writes `Formula/mint.rb` on first release).
2. Add a GitHub Actions secret named `HOMEBREW_TAP_GITHUB_TOKEN` to the **Mint release repository** (`SpiceLabsHQ/Mint`). The token must belong to an account with write access to `homebrew-mint` and needs the `repo` scope (or a fine-grained token scoped to `homebrew-mint` with Contents: read/write).

### Installing Mint via Homebrew

End users install Mint with:

```bash
brew install SpiceLabsHQ/mint/mint
```

The first run taps the repository automatically. To tap explicitly before installing:

```bash
brew tap SpiceLabsHQ/mint
brew install mint
```

### How It Works

GoReleaser's `brews:` stanza (in `.goreleaser.yaml`) generates `Formula/mint.rb` from the release artifacts and pushes it to `SpiceLabsHQ/homebrew-mint` using `HOMEBREW_TAP_GITHUB_TOKEN`. No manual formula edits are required — every release updates the formula automatically with the new version, checksums, and download URLs.

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

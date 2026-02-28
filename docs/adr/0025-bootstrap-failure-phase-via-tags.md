# ADR-0025: Bootstrap Failure Phase Propagation via Instance Tags

## Status
Accepted

## Context
When `bootstrap.sh` fails, the EXIT trap marks the instance with `mint:bootstrap=failed`. The CLI polls for this tag value and surfaces a generic failure message — but it has no structured way to know *which section* of `bootstrap.sh` failed.

Bootstrap is a long sequential script with several distinct phases: package installation, EFS mount, Docker setup, systemd unit configuration, drift check, and the optional user bootstrap hook. When a VM lands in `mint:bootstrap=failed`, the developer's only recourse is to SSH in and read cloud-init logs — a poor experience that conflicts with Mint's transparency principle.

Three options were evaluated:

1. **Poll logs via SSM**: Query the instance's cloud-init log or journal via SSM Run Command to extract the last section heading.
2. **Encode phase in the existing tag value**: Use a compound value like `failed:user-script` instead of the bare `failed` string.
3. **Write a separate tag**: Before the EXIT trap fires, bootstrap writes `mint:bootstrap-failure-phase` with the name of the section that was active when the script exited non-zero.

The correct option must satisfy two constraints:

- **No new AWS dependencies**: Mint already avoids SSM, DynamoDB, and other managed services (ADR-0010, ADR-0014). Adding an SSM dependency for failure diagnostics introduces new IAM requirements, VPC endpoint considerations, and a new failure mode.
- **Backward compatible**: The existing `mint:bootstrap=failed` tag is a defined constant (`tags.BootstrapFailed`) compared by string equality throughout the CLI and in tests. Any approach that changes the tag value breaks existing consumers without a migration path.

## Decision

Add `mint:bootstrap-failure-phase` as a separate EC2 instance tag, written by `bootstrap.sh` immediately before the EXIT trap sets `mint:bootstrap=failed`.

### Mechanism

`bootstrap.sh` maintains a shell variable tracking the current phase name. As execution enters each phase, the variable is updated. When the EXIT trap fires on a non-zero exit, the trap writes the current phase value as a tag:

```bash
aws ec2 create-tags \
  --resources "$_TRAP_INSTANCE_ID" \
  --tags "Key=mint:bootstrap-failure-phase,Value=$_BOOTSTRAP_PHASE" \
  --region "$_TRAP_REGION"
```

The tag is written only when the exit status is non-zero. Successful bootstraps do not write this tag.

### Phase values

| Value | When written |
|-------|-------------|
| `packages` | apt package installation or snap install |
| `efs-mount` | EFS discovery or NFS mount |
| `docker` | Docker installation or daemon start |
| `systemd-units` | systemd unit creation or `systemctl enable` |
| `drift-check` | Post-bootstrap health check |
| `user-script` | User bootstrap hook (`MINT_USER_BOOTSTRAP`) |

### CLI consumption

The CLI reads `mint:bootstrap-failure-phase` when it detects `mint:bootstrap=failed`. The phase value is appended to the failure message shown to the developer:

```
Bootstrap failed (phase: efs-mount). Run `mint destroy` to clean up, or SSH in to debug.
```

When the tag is absent (e.g., the EXIT trap itself failed before writing it, or the VM is from before this ADR), the CLI omits the phase detail and shows the existing generic message. The phase tag is advisory — its absence is not an error.

### Tag key convention

`mint:bootstrap-failure-phase` follows the existing `mint:` namespace convention for all Mint-managed tags (ADR-0001). It is a diagnostic tag, not a state tag — it does not drive any branching logic beyond display. Its presence does not replace `mint:bootstrap=failed`; both tags coexist when bootstrap fails.

## Alternatives Rejected

### SSM Parameter Store or SSM Run Command

Poll the instance's cloud-init log or journal via SSM Run Command (`aws ssm send-command`) to retrieve the last phase heading written by `bootstrap.sh`.

Rejected because: (a) SSM requires the SSM agent to be running and healthy on the instance, which is not guaranteed when bootstrap fails mid-way; (b) SSM Run Command requires `ssm:SendCommand` and `ssm:GetCommandInvocation` IAM permissions not currently in Mint's permission set; (c) SSM requires either an SSM VPC endpoint or outbound HTTPS from the instance, adding a network dependency; (d) Mint explicitly avoids managed services beyond EC2 and EFS (ADR-0010, ADR-0014) — introducing SSM for diagnostics contradicts that principle.

### Encoding phase in the existing tag value (e.g., `failed:user-script`)

Change the `mint:bootstrap` tag from `failed` to a compound value like `failed:user-script` that encodes both the status and the phase.

Rejected because: (a) `tags.BootstrapFailed` is compared by string equality throughout the CLI (`== tags.BootstrapFailed`) and in tests; changing the tag value breaks all existing consumers without a migration path; (b) the tag value becomes an implicit protocol with two fields separated by a delimiter — future readers must parse it, and the delimiter choice becomes a maintenance decision; (c) backward compatibility cannot be guaranteed — a CLI running against an older VM (or an older CLI against a newer VM) would see an unexpected tag value and fail to detect the failure state.

### Log polling via SSH or cloud-init

After detecting `mint:bootstrap=failed`, SSH into the instance and read `/var/log/cloud-init-output.log` to extract the last section.

Rejected because: (a) SSH may not be available when bootstrap fails, particularly if the failure occurs before the SSH daemon is configured or before the security group rules are applied; (b) polling via SSH requires a working EC2 Instance Connect session, which itself depends on the instance being in a running state with network access — conditions that may not hold during a mid-bootstrap failure; (c) parsing unstructured log output is brittle and locale-sensitive; (d) the CLI's bootstrap polling loop is designed to work without an SSH session — adding an SSH dependency for failure diagnosis complicates the failure path significantly.

## Consequences

- **Targeted failure messages.** Developers see which phase failed (`efs-mount`, `user-script`, etc.) without reading cloud-init logs. This directly reduces time-to-debug for the most common bootstrap failure scenarios.
- **Convention-establishing.** This ADR establishes that structured failure context flows from the instance to the CLI via EC2 tags. Future bootstrap instrumentation — new phases, extended diagnostics, retry hints — will follow this pattern rather than inventing ad-hoc mechanisms.
- **No new AWS dependencies.** The feature uses only the existing `ec2:CreateTags` permission already required for bootstrap completion tagging. No new IAM permissions, no new AWS services.
- **Backward compatible.** The `mint:bootstrap=failed` tag value is unchanged. Older CLI versions continue to work — they simply don't display the phase. Newer CLI versions degrade gracefully when the phase tag is absent.
- **Tag written before EXIT trap completes.** If the `create-tags` call itself fails (e.g., network loss), the phase tag is not written. The EXIT trap continues and writes `mint:bootstrap=failed` regardless. The CLI handles the absent phase tag gracefully.
- **Implementing PR:** #190

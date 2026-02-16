# Mint — Specification

## Overview

Mint is a CLI tool that provisions and manages EC2-based development environments for running Claude Code in devcontainers. A single VM hosts multiple projects. Advanced users can run multiple VMs.

### Usage Patterns

**Primary — VS Code Remote SSH from MacBook:** The developer connects to their Mint VM via VS Code's Remote-SSH extension and works inside devcontainers as if they were local. Claude Code runs in the integrated terminal. This is the everyday workflow at the desk.

**Secondary — Mobile monitoring via Termius on iPad:** When away from the desk (car, couch, travel), the developer connects via mosh from Termius to check on Claude Code sessions, approve permissions, and give quick steering. tmux on the VM host keeps sessions alive between reconnections.

## Architecture

```
Primary:   MacBook → VS Code Remote-SSH → EC2 host → devcontainer
Secondary: iPad (Termius) → mosh → EC2 host → tmux → docker exec → devcontainer → Claude Code
```

tmux lives on the EC2 host, not inside containers. When the iPad disconnects (iOS suspends Termius, network drops), tmux keeps the session alive. Reconnecting is a single `tmux attach`.

## Security Model

Mint is a **trusted-team tool**. Multiple users share an AWS account and operate with PowerUser permissions. The `mint:owner` tag provides **ownership tracking for cost attribution and resource discovery, not access control**. Any PowerUser in the account can modify another user's resources via AWS APIs.

If the threat model changes (untrusted users, compliance requirements), the escalation path is IAM permission boundaries with `aws:ResourceTag` conditions or separate AWS accounts per user.

## Resource Tagging

Every AWS resource Mint creates is tagged for discovery and billing.

| Tag Key | Value | Purpose |
|---|---|---|
| `mint` | `true` | Primary filter — identifies all Mint-managed resources |
| `mint:component` | `instance`, `volume`, `security-group`, `elastic-ip` | Resource type |
| `mint:vm` | VM name (e.g. `default`, `gpu-box`) | Which VM this resource belongs to |
| `mint:owner` | Friendly name derived from AWS identity ARN (e.g. `ryan`) | Resource discovery and filtering |
| `mint:owner-arn` | Full caller ARN from `sts get-caller-identity` | Auditability, disambiguation if friendly names collide |
| `mint:bootstrap` | `complete` | Set by health-check script after successful first-boot provisioning |
| `Name` | `mint/<owner>/<vm-name>` | Standard AWS console display |

Mint discovers its own resources exclusively via tags. There is no local state file tracking resource IDs. Multiple users in the same AWS account coexist by filtering on `mint:owner`.

### Owner Identity

The owner is derived at runtime from `aws sts get-caller-identity` — it is not stored in config. The ARN's trailing identifier is normalized to a friendly name: strip `@domain` for SSO emails, lowercase, replace non-alphanumeric characters with `-`.

| Auth Type | ARN | Derived Owner |
|---|---|---|
| IAM user | `arn:aws:iam::123456789012:user/ryan` | `ryan` |
| SSO | `arn:aws:sts::123456789012:assumed-role/AWSReservedSSO_.../ryan@example.com` | `ryan` |
| Assumed role | `arn:aws:sts::123456789012:assumed-role/RoleName/session-name` | `session-name` |

If a user authenticates with a different AWS identity, they will not see resources created under the previous identity. This is correct behavior — different identity, different owner.

Billing review: filter Cost Explorer on `mint=true`, group by `mint:vm` or `mint:owner`.

## Admin Setup (one-time, requires IAM permissions)

An AWS administrator performs this once per account. It creates shared infrastructure that all Mint users depend on. Mint users themselves operate with PowerUser permissions and cannot perform these steps.

### What the admin creates

**IAM Role: `mint-instance-role`** — Allows Mint VMs to stop themselves when idle and tag themselves for bootstrap verification. Scoped so an instance can only stop instances tagged `mint=true` with its own instance ID. Permissions: `ec2:StopInstances` (conditioned on own instance ID + `mint=true` tag), `ec2:DescribeInstances`, `ec2:DescribeTags`, `ec2:CreateTags` (on own instance, for `mint:bootstrap` tag).

**IAM Instance Profile: `mint-instance-profile`** — Wraps the role above. Mint passes this profile to instances at launch.

Mint ships with documentation containing the exact IAM policy JSON and a CloudFormation template the admin can deploy directly.

### Validation

`mint init` checks for the instance profile. If missing, it exits with an error message directing the admin to the setup documentation. `mint init` also validates that the default VPC exists with a public subnet in the configured region. These are the only blockers — everything else Mint creates on its own.

## Per-User Init

Each Mint user runs `mint init` once from their machine. This creates user-scoped resources within the shared AWS account using PowerUser permissions:

- Creates a security group allowing SSH (TCP 22) and mosh (UDP 60000-61000) from the user's current public IP, tagged. The source IP is not 0.0.0.0/0.
- Sets the AWS region explicitly (not inherited from AWS CLI default config)
- Validates that the admin-created instance profile exists
- Validates that the default VPC exists with a public subnet

No SSH keys are generated or stored. SSH access is handled by EC2 Instance Connect (see Authentication).

## CLI Commands

### VM Lifecycle

Most users have one VM. The `--vm` flag defaults to `default` and can be omitted. Advanced users name their VMs to run multiple.

All commands support `--verbose` (progress steps) and `--debug` (AWS SDK call details) flags globally.

**`mint up [--vm <name>]`** — Creates and starts a VM. If a stopped VM with that name exists (found by tag), starts it instead. The VM gets an Elastic IP for a stable address (Mint checks EIP quota before allocation). On first boot, the VM bootstraps itself and `mint up` polls for the `mint:bootstrap=complete` tag before reporting success, showing progress with phase labels ("Launching instance...", "Allocating Elastic IP...", "Waiting for bootstrap..."). On subsequent starts, a boot-time reconciliation script verifies installed software versions. Base image is Ubuntu 24.04 LTS, resolved dynamically. Instance type defaults to m6i.xlarge (4 vCPU, 16GB). All resources are tagged.

**`mint down [--vm <name>]`** — Stops the VM. EBS volume persists. Compute billing stops. Elastic IP remains allocated so the address doesn't change on next start.

**`mint destroy [--vm <name>]`** — Terminates the VM and releases all associated resources (EBS, Elastic IP) found by tag. Requires interactive confirmation by default. Use `--yes` to skip confirmation in scripts.

**`mint list [--json]`** — Shows all VMs owned by the current user with their state (running, stopped), IP, uptime, and idle timer status.

**`mint status [--vm <name>] [--json]`** — Detailed status for a VM: state, IP, instance type, volume size, disk usage, running devcontainers, tmux sessions, idle timer remaining.

### Connecting

**`mint ssh [--vm <name>]`** — Opens an SSH session to the VM via EC2 Instance Connect.

**`mint mosh [--vm <name>]`** — Opens a mosh session to the VM. Uses EC2 Instance Connect for the initial SSH handshake, then switches to UDP.

**`mint connect [session] [--vm <name>]`** — Opens a mosh session and automatically attaches to the named tmux session. If no session name is given, presents a session picker.

**`mint sessions [--vm <name>]`** — Lists active tmux sessions on the VM.

**`mint ssh-config [--vm <name>]`** — Generates or updates `~/.ssh/config` with a `Host mint-<vm>` entry using a `ProxyCommand` that routes through EC2 Instance Connect. This enables VS Code Remote-SSH and other standard SSH clients to connect without managing keys.

**`mint key add <public-key> [--vm <name>]`** — Adds a public key to the VM's `~/.ssh/authorized_keys` via EC2 Instance Connect. Use this for clients that cannot use Instance Connect directly (e.g. Termius on iPad, CI runners, third-party tools). Accepts a file path or `-` for stdin.

### Projects

Projects live on VMs. A single VM typically hosts multiple projects, each in its own devcontainer.

**`mint project add <git-url> [--branch <branch>] [--name <name>] [--vm <name>]`** — On the specified VM: clones the repo, builds the devcontainer (using BuildKit cache for layer reuse), and creates a named tmux session with a `docker exec` shell into the running container. The project name defaults to the repo name. The repo URL and branch are not stored in Mint's config — this is an imperative action on the VM.

**`mint project list [--vm <name>] [--json]`** — Lists projects on the VM by inspecting running devcontainers and project directories.

**`mint project rebuild <project> [--vm <name>]`** — Tears down and rebuilds the devcontainer for a project.

### Idle Management

**`mint extend [minutes] [--vm <name>]`** — Resets the idle auto-stop timer. Defaults to the configured timeout.

### Configuration

**`mint config [--json]`** — Shows current configuration.

**`mint config set <key> <value>`** — Sets a configuration value (e.g. `mint config set idle.timeout_minutes 90`).

Configuration is stored at `~/.config/mint/config.toml` (following XDG conventions). Configuration covers: AWS region, default instance type, default volume size, and idle timeout. Owner identity is derived at runtime from AWS credentials, not stored in config. It does not store project or repo information — that lives on the VMs themselves.

## Auto-Stop

Each VM runs an idle detection system (systemd timer checking every 5 minutes). The idle detection service writes structured JSON logs to journald with fields: `check_timestamp`, `active_criteria_met`, `idle_elapsed_minutes`, `action_taken`, `stop_result`.

A VM is considered active if any of these are true:

- An SSH or mosh session is connected
- A tmux session has an attached client
- A `claude` process is running inside any container
- The idle timer was manually extended

When none are true for the configured timeout (default 60 minutes), the VM stops itself using the IAM permissions from the instance role.

## Bootstrap Verification

On first boot, the VM runs user-data to install all required software. A health-check script runs at the end of the bootstrap process to validate that all components are operational (Docker daemon running, devcontainer CLI in PATH, mosh-server available, tmux installed, etc.). On success, it tags the instance with `mint:bootstrap=complete`.

`mint up` polls for this tag before reporting success. If the tag does not appear within a timeout, `mint up` reports a bootstrap failure and directs the user to check cloud-init logs.

On subsequent starts (stop/start cycles), a boot-time reconciliation script (systemd unit) compares installed component versions against expected versions and logs discrepancies. This catches drift from manual modifications or extended periods between restarts.

## Multiple VMs (advanced)

Power users can run multiple VMs for workload isolation or different instance types. Every VM command accepts `--vm <name>` to target a specific VM. Without it, commands target the `default` VM.

Example:

```
mint up                          # starts "default" VM
mint up --vm gpu-box             # starts a second VM
mint project add <url> --vm gpu-box
mint list                        # shows both VMs
mint down --vm gpu-box           # stops only gpu-box
```

Each VM has its own Elastic IP, EBS volume, idle timer, and set of devcontainers. They share the user's security group.

## EC2 Instance Details

- **AMI**: Ubuntu 24.04 LTS, resolved via SSM parameter (not hardcoded)
- **Instance type**: m6i.xlarge (4 vCPU, 16GB RAM), configurable per VM
- **Storage**: 200GB gp3 EBS root volume, persists across stop/start
- **Elastic IP**: One per VM, stable across stop/start cycles (default quota: 5 per region — admin should request increase via Service Quotas for multi-user setups)
- **Security group**: Shared across user's VMs (SSH + mosh ports, scoped to user's IP)
- **Instance profile**: `mint-instance-profile` (admin-created, shared)
- **Networking**: Default VPC with public subnet. No custom VPC, no bastion, no SSM Session Manager.

### Software installed on first boot

Docker Engine, Docker Compose, devcontainer CLI, tmux (with mouse support and large scroll buffer), mosh-server, Git, GitHub CLI, Node.js LTS, AWS CLI v2, EC2 Instance Connect agent.

Docker BuildKit cache mounts are persisted on the EBS volume so devcontainer rebuilds reuse layers across projects.

### tmux configuration

Mouse support enabled, 50k line scroll buffer, 256-color terminal. This is host-level tmux — not inside containers.

## Authentication

**AWS**: Users authenticate via standard AWS CLI credentials (profiles, SSO, env vars). Mint uses whatever `aws` is configured to use. Users need `ec2-instance-connect:SendSSHPublicKey` permission (included in PowerUser).

**Claude Code**: Users authenticate interactively on first connect. Claude Code prompts for login. Mint does not manage Anthropic credentials.

**SSH/mosh**: EC2 Instance Connect is the primary mechanism. On each connection, Mint pushes an ephemeral public key to the instance (valid for 60 seconds) and opens an SSH session. No persistent keys are generated, stored, or managed by Mint.

For clients that cannot use EC2 Instance Connect (e.g. Termius on iPad, CI runners), `mint key add` appends a public key to the VM's `authorized_keys` via Instance Connect, enabling direct SSH access with that key.

## VS Code Integration

The primary workflow uses VS Code Remote-SSH. After `mint up`, the developer:

1. Runs `mint ssh-config` to generate/update their SSH config (one-time per VM, or after `mint destroy` + `mint up` allocates a new Elastic IP)
2. Connects via Remote-SSH in VS Code using the `mint-<vm>` host
3. Opens the project folder, which has a devcontainer configuration
4. VS Code detects and reopens in the devcontainer
5. Claude Code runs in the integrated terminal

`mint ssh-config` writes a `ProxyCommand` entry that routes through EC2 Instance Connect, so VS Code connects without any SSH key configuration. VS Code's existing Remote-SSH and Dev Containers extensions handle the rest natively.

## Future Considerations (out of scope for v1)

- Spot instance support for cost savings
- Automatic devcontainer rebuild on git push via webhook
- Instance type scaling (resize a running VM)
- EBS snapshot/restore for fast recreation
- Team shared instances with per-user tmux sessions
- Push notifications when Claude Code needs input
- Dead-man's switch Lambda for detecting auto-stop failures (watchdog that checks heartbeat tags and force-stops stale instances)
- Dedicated Docker EBS volume at `/var/lib/docker` (upgrade from shared root volume)
- IAM permission boundaries for untrusted multi-user environments

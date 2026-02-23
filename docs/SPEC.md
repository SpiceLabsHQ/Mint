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
| `mint:component` | `instance`, `volume`, `security-group`, `elastic-ip`, `project-volume`, `efs-access-point` | Resource type |
| `mint:vm` | VM name (e.g. `default`, `gpu-box`) | Which VM this resource belongs to |
| `mint:owner` | Friendly name derived from AWS identity ARN (e.g. `ryan`) | Resource discovery and filtering |
| `mint:owner-arn` | Full caller ARN from `sts get-caller-identity` | Auditability, disambiguation if friendly names collide |
| `mint:bootstrap` | `complete`, `failed` | Set by health-check script after first-boot provisioning; `failed` set before termination on bootstrap timeout |
| `mint:health` | `healthy`, `drift-detected` | Client-queryable VM health state, set by boot-time reconciliation unit |
| `mint:pending-attach` | (none) | Presence-only. Set on project EBS during `mint recreate` for failure recovery; tag existence signals pending reattachment |
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

**EFS Filesystem** — A shared Amazon EFS filesystem with Elastic throughput mode for user configuration volumes. The CloudFormation template creates the filesystem, a dedicated `mint-efs` security group with an NFS inbound rule referencing itself, and configures the filesystem for Elastic throughput. Every Mint VM is launched with the `mint-efs` security group attached, scoping NFS access to Mint VMs only.

Mint automates this with `mint admin setup` (or `mint admin deploy` + `mint admin attach-policy` individually). See [docs/admin-setup.md](admin-setup.md) for the operator guide.

### Validation

`mint init` checks for the instance profile and EFS filesystem. If either is missing, it exits with an error message directing the admin to the setup documentation. `mint init` also validates that the default VPC exists with a public subnet in the configured region. These are the only blockers — everything else Mint creates on its own.

## Per-User Init

Each Mint user runs `mint init` once from their machine. This creates user-scoped resources within the shared AWS account using PowerUser permissions:

- Creates a security group allowing SSH (TCP 41122) and mosh (UDP 60000-61000), tagged. Uses non-standard SSH port to avoid automated scanning; inbound is open to all IPs, with security provided by key-only authentication (see ADR-0016).
- Creates a per-user EFS access point on the shared EFS filesystem for persistent user configuration (dotfiles, authorized_keys, Claude Code auth state)
- Sets the AWS region explicitly (not inherited from AWS CLI default config)
- Validates that the admin-created instance profile exists
- Validates that the EFS filesystem exists
- Validates that the default VPC exists with a public subnet

No SSH keys are generated or stored. SSH access is handled by EC2 Instance Connect (see Authentication).

## CLI Commands

### VM Lifecycle

Most users have one VM. The `--vm` flag defaults to `default` and can be omitted. Advanced users name their VMs to run multiple.

All commands support `--verbose` (progress steps) and `--debug` (AWS SDK call details) flags globally.

**`mint up [--vm <name>]`** — Creates and starts a VM. If a stopped VM with that name exists (found by tag), starts it instead. The VM gets an Elastic IP for a stable address (Mint checks EIP quota before allocation). On first boot, the VM bootstraps itself and `mint up` polls for the `mint:bootstrap=complete` tag before reporting success, showing progress with phase labels ("Launching instance...", "Allocating Elastic IP...", "Waiting for bootstrap..."). On subsequent starts, a boot-time reconciliation script verifies installed software versions. Base image is Ubuntu 24.04 LTS, resolved dynamically. Instance type defaults to m6i.xlarge (4 vCPU, 16GB). All resources are tagged. After the VM is ready, `mint up` auto-generates the SSH config entry (prompting for permission on first run — see ADR-0015).

**`mint down [--vm <name>]`** — Stops the VM. Root EBS, project EBS, and EFS all persist. Compute billing stops. Elastic IP remains allocated so the address doesn't change on next start.

**`mint resize [--vm <name>] <instance-type>`** — Changes the EC2 instance type. Stops the instance, modifies the instance type attribute, starts the instance. All volumes preserved. This is a native EC2 operation taking ~60 seconds.

**`mint recreate [--vm <name>]`** — Terminates the instance and root volume, launches a new instance in the same AZ, reattaches the project EBS volume, EFS mounts via fstab, and bootstrap runs on the fresh root volume. Use when the host OS or Docker environment needs a clean slate (bootstrap updates, root corruption, Ubuntu LTS upgrade). Requires interactive confirmation. Refuses to proceed if active SSH, mosh, or tmux sessions are detected; use `--force` to override. Orchestration sequence: (1) check for active sessions, (2) query the project EBS volume's AZ via `DescribeVolumes` — this happens first so that if the query fails, no state has changed, (3) tag the project EBS with `mint:pending-attach` for failure recovery, (4) stop the instance, (5) detach the project EBS, (6) terminate the instance, (7) launch a new instance in the same AZ, (8) attach the project EBS and remove the `mint:pending-attach` tag. If a recreate fails mid-sequence, `mint up` detects the pending-attach tag on the project volume and resumes the reattachment.

**`mint destroy [--vm <name>]`** — Fully destructive. Terminates the instance, deletes root EBS, deletes project EBS, releases Elastic IP. User EFS unmounts naturally (user-scoped, not VM-scoped) and persists independently. Requires interactive confirmation by default. Use `--yes` to skip confirmation in scripts.

**`mint doctor [--vm <name>] [--fix]`** — Validates environment health. Checks AWS credentials, region configuration, service quota headroom (Elastic IPs, vCPUs), and SSH config sanity. If any VMs are running, also checks VM health, disk usage, component versions, and `mint:health` tag status. Use `--vm` to target a specific VM. `--fix` triggers explicit repair of detected drift (the only path to remediation — auto-fix is intentionally avoided).

**`mint version`** — Prints version information.

**`mint update`** — Self-updates the CLI binary. Downloads the latest release from GitHub Releases (built via GoReleaser for cross-platform matrix), verifies the checksum, and performs atomic binary replacement. Leaves the existing binary untouched if the checksum does not match.

**`mint list [--json]`** — Shows all VMs owned by the current user with their state (running, stopped), IP, uptime, and idle timer status. Running VMs that have exceeded their configured idle timeout are flagged with a warning — this is the primary v1 cost safety net for detecting auto-stop failures. Also prints a one-line notice when a newer Mint version is available (checked against GitHub Releases API, cached for 24 hours at `~/.config/mint/version-cache.json`; fails open — if the API call fails, the notice is silently skipped).

**`mint status [--vm <name>] [--json]`** — Detailed status for a VM: state, IP, instance type, volume size, disk usage, running devcontainers, tmux sessions, idle timer remaining. Also prints the stale-version notice (same as `mint list`).

### Connecting

**`mint ssh [--vm <name>]`** — Opens an SSH session to the VM via EC2 Instance Connect.

**`mint mosh [--vm <name>]`** — Opens a mosh session to the VM. Uses EC2 Instance Connect for the initial SSH handshake, then switches to UDP.

**`mint connect [session] [--vm <name>]`** — Opens a mosh session and automatically attaches to the named tmux session. If no session name is given, presents a session picker.

**`mint sessions [--vm <name>]`** — Lists active tmux sessions on the VM.

**`mint ssh-config [--vm <name>]`** — Generates or updates `~/.ssh/config` with a `Host mint-<vm>` entry using a `ProxyCommand` that routes through EC2 Instance Connect. This enables VS Code Remote-SSH and other standard SSH clients to connect without managing keys.

**`mint code [project] [--vm <name>]`** — Opens VS Code connected to the VM via Remote-SSH. If a project name is given, opens that project's directory. Runs `code --remote ssh-remote+mint-<vm> <path>`. Ensures `mint ssh-config` has been run first, and that the VM is running.

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

**`mint config set <key> <value>`** — Sets a configuration value (e.g. `mint config set idle_timeout_minutes 90`). Validates aggressively on write: `instance_type` is validated against the AWS API, `volume_size_gb` must be >= 50, `idle_timeout_minutes` must be >= 15, and unknown keys are rejected.

Configuration is stored at `~/.config/mint/config.toml` (following XDG conventions). Flat structure, no nesting, all keys are snake_case:

| Key | Type | Description |
|-----|------|-------------|
| `region` | string | AWS region (e.g. `us-east-1`) |
| `instance_type` | string | Default EC2 instance type (e.g. `t3.medium`) |
| `volume_size_gb` | integer | Project EBS volume size in GB (default 50; root EBS is fixed at 200GB) |
| `idle_timeout_minutes` | integer | Minutes of idle before auto-stop |
| `ssh_config_approved` | boolean | Whether user has approved Mint writing SSH config entries |

Owner identity is derived at runtime from AWS credentials, not stored in config. It does not store project or repo information — that lives on the VMs themselves.

## Auto-Stop

Each VM runs an idle detection system (systemd timer checking every 5 minutes). The idle detection service writes structured JSON logs to journald with fields: `check_timestamp`, `active_criteria_met`, `idle_elapsed_minutes`, `action_taken`, `stop_result`.

A VM is considered active if any of these are true:

- An SSH or mosh session is connected
- A tmux session has an attached client
- A `claude` process is running inside any container
- The idle timer was manually extended

When none are true for the configured timeout (default 60 minutes), the VM stops itself using the IAM permissions from the instance role.

## Bootstrap Verification

On first boot, the VM runs user-data to install all required software. The bootstrap script's SHA256 hash is embedded in the Go binary at compile time via `go:generate` and verified before sending to EC2 — if the hash does not match, `mint up` aborts immediately. This closes supply-chain attack vectors (compromised CDN, tampered repository) without requiring full signing infrastructure. CI must verify that the embedded hash matches the script content to prevent drift when contributors edit the bootstrap script.

A health-check script runs at the end of the bootstrap process to validate that all components are operational (Docker daemon running, devcontainer CLI in PATH, mosh-server available, tmux installed, etc.). On success, it tags the instance with `mint:bootstrap=complete`. The bootstrap script writes its version to `/var/lib/mint/bootstrap-version` for drift detection on subsequent boots.

`mint up` polls for this tag with a **7-minute timeout** before reporting success. If the tag does not appear within the timeout, `mint up` prompts the user to choose one of three options:

1. **Stop the instance** — Halts billing while preserving the instance for later debugging.
2. **Terminate the instance** — Tags the instance with `mint:bootstrap=failed` (visible in `mint list` output), then destroys the instance and cleans up resources.
3. **Leave running** — Takes no action, allowing the user to connect via SSH and debug directly.

On subsequent starts (stop/start cycles), a boot-time reconciliation script (systemd unit) compares installed component versions against expected versions, logs warnings to journald, and sets the `mint:health` tag to `healthy` or `drift-detected` accordingly. This tag is queryable from the client via `mint status` and `mint doctor` without requiring SSH. The reconciliation unit does **not** auto-fix — `mint doctor --fix` is the explicit repair path. This avoids the security anti-pattern of unattended package operations on boot.

## Multiple VMs (advanced)

Power users can run multiple VMs for workload isolation or different instance types. Every VM command accepts `--vm <name>` to target a specific VM. Without it, commands target the `default` VM. Mint warns when a user has 3 or more running VMs but does not enforce a hard limit — actual capacity is bounded by AWS service quotas (Elastic IPs, vCPUs).

Example:

```
mint up                          # starts "default" VM
mint up --vm gpu-box             # starts a second VM
mint project add <url> --vm gpu-box
mint list                        # shows both VMs
mint down --vm gpu-box           # stops only gpu-box
```

Each VM has its own Elastic IP, root EBS volume, project EBS volume, idle timer, and set of devcontainers. They share the user's security group and EFS access point (user config is shared across all VMs).

## EC2 Instance Details

- **AMI**: Ubuntu 24.04 LTS, resolved via SSM parameter (not hardcoded)
- **Instance type**: m6i.xlarge (4 vCPU, 16GB RAM), configurable per VM
- **Storage**: Three-tier model:
  - **Root EBS**: 200GB gp3, ephemeral OS and Docker layer. Created fresh on `mint up` and `mint recreate`. Destroyed on `mint destroy` and `mint recreate`.
  - **User EFS**: Per-user EFS access point mounted at `/mint/user`, with symlinks into `$HOME` for well-known paths (`~/.ssh`, `~/.config/claude`, dotfiles). Persistent configuration: dotfiles, authorized_keys, Claude Code auth state. Created during `mint init`. Shared across all of the user's VMs. Persists across all lifecycle operations including `mint destroy`.
  - **Project EBS**: Per-VM 50GB gp3 volume (configurable via `volume_size_gb`) mounted at `/mint/projects` for project source code. Created on `mint up`. Persists across `mint resize` and `mint recreate`. Destroyed on `mint destroy`.
- **Elastic IP**: One per VM, stable across stop/start cycles (default quota: 5 per region — admin should request increase via Service Quotas for multi-user setups)
- **Security group**: Shared across user's VMs (SSH on port 41122 + mosh ports, open to all IPs)
- **Instance profile**: `mint-instance-profile` (admin-created, shared)
- **Networking**: Default VPC with public subnet. No custom VPC, no bastion, no SSM Session Manager.

### Software installed on first boot

Docker Engine, Docker Compose, devcontainer CLI, tmux (with mouse support and large scroll buffer), mosh-server, Git, GitHub CLI, Node.js LTS, AWS CLI v2, EC2 Instance Connect agent.

Docker BuildKit cache mounts are persisted on the root EBS volume so devcontainer rebuilds reuse layers across projects.

### tmux configuration

Mouse support enabled, 50k line scroll buffer, 256-color terminal. This is host-level tmux — not inside containers.

## Authentication

**AWS**: Users authenticate via standard AWS CLI credentials (profiles, SSO, env vars). Mint uses whatever `aws` is configured to use. Users need `ec2-instance-connect:SendSSHPublicKey` permission (included in PowerUser).

**Claude Code**: Users authenticate interactively on first connect. Claude Code prompts for login. Mint does not manage Anthropic credentials.

**SSH/mosh**: EC2 Instance Connect is the primary mechanism. On each connection, Mint pushes an ephemeral public key to the instance (valid for 60 seconds) and opens an SSH session. No persistent keys are generated, stored, or managed by Mint.

For clients that cannot use EC2 Instance Connect (e.g. Termius on iPad, CI runners), `mint key add` appends a public key to the VM's `authorized_keys` via Instance Connect, enabling direct SSH access with that key.

**SSH host key trust**: Mint stores first-seen host keys in `~/.config/mint/known_hosts` and validates on reconnect. When a new host key appears for a VM name that already has a stored key (e.g. after `mint recreate`), Mint prompts the user before accepting the new key.

## Observability

**CLI structured logging**: Every AWS API call is logged with service, operation, duration, and result. Logs are written to `~/.config/mint/logs/` and suppressed by default. Visible via `--debug` for ad-hoc troubleshooting or post-mortem analysis.

**CLI audit logging**: All Mint commands are recorded to `~/.config/mint/audit.log` with timestamp, command, VM name, and caller ARN. Local-only, no centralized export in v1.

**VM disk usage alerting**: A journald threshold warning fires at 80% root volume usage. This warning is surfaced in `mint status` output so users are aware before disk pressure causes failures.

## VS Code Integration

The primary workflow uses VS Code Remote-SSH. After `mint up`, the developer:

1. Runs `mint code <project>` to open VS Code connected to the project on the VM
2. VS Code detects the devcontainer configuration and reopens in the devcontainer
3. Claude Code runs in the integrated terminal

`mint up` auto-generates the SSH config entry, so there's no separate setup step. `mint code` wraps the `code --remote` invocation so the user never needs to remember the syntax. VS Code's existing Remote-SSH and Dev Containers extensions handle the rest natively. `mint ssh-config` remains available for manual re-generation if needed.

## Future Considerations (out of scope for v1)

- Spot instance support for cost savings
- Automatic devcontainer rebuild on git push via webhook
- EBS snapshot/restore for fast recreation
- Team shared instances with per-user tmux sessions
- Push notifications when Claude Code needs input
- Dead-man's switch Lambda for detecting auto-stop failures (watchdog that checks heartbeat tags and force-stops stale instances)
- IAM permission boundaries for untrusted multi-user environments
- `mint up --project <git-url>` to provision a VM and clone a project in one command
- CLI binary signing (cosign/GPG) for release verification

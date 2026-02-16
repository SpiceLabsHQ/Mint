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

## Resource Tagging

Every AWS resource Mint creates is tagged for discovery and billing.

| Tag Key | Value | Purpose |
|---|---|---|
| `mint` | `true` | Primary filter — identifies all Mint-managed resources |
| `mint:component` | `instance`, `volume`, `security-group`, `key-pair`, `elastic-ip` | Resource type |
| `mint:vm` | VM name (e.g. `default`, `gpu-box`) | Which VM this resource belongs to |
| `mint:owner` | IAM username or configured owner identifier | Which user owns this resource |
| `Name` | `mint/<owner>/<vm-name>` | Standard AWS console display |

Mint discovers its own resources exclusively via tags. There is no local state file tracking resource IDs. Multiple users in the same AWS account coexist by filtering on `mint:owner`.

Billing review: filter Cost Explorer on `mint=true`, group by `mint:vm` or `mint:owner`.

## Admin Setup (one-time, requires IAM permissions)

An AWS administrator performs this once per account. It creates shared infrastructure that all Mint users depend on. Mint users themselves operate with PowerUser permissions and cannot perform these steps.

### What the admin creates

**IAM Role: `mint-instance-role`** — Allows Mint VMs to stop themselves when idle. Scoped so an instance can only stop instances tagged `mint=true` with its own instance ID. Also needs `ec2:DescribeInstances` and `ec2:DescribeTags` for self-identification.

**IAM Instance Profile: `mint-instance-profile`** — Wraps the role above. Mint passes this profile to instances at launch.

Mint ships with documentation containing the exact IAM policy JSON and a CloudFormation template the admin can deploy directly.

### Validation

`mint init` checks for the instance profile. If missing, it exits with an error message directing the admin to the setup documentation. This is the only blocker — everything else Mint creates on its own.

## Per-User Init

Each Mint user runs `mint init` once from their machine. This creates user-scoped resources within the shared AWS account using PowerUser permissions:

- Generates an SSH key pair (Ed25519) and registers it with AWS, tagged with `mint:owner`
- Creates a security group allowing SSH (TCP 22) and mosh (UDP 60000-61000), tagged
- Validates that the admin-created instance profile exists
- Stores the private key locally for SSH/mosh access and for import into Termius

## CLI Commands

### VM Lifecycle

Most users have one VM. The `--vm` flag defaults to `default` and can be omitted. Advanced users name their VMs to run multiple.

**`mint up [--vm <name>]`** — Creates and starts a VM. If a stopped VM with that name exists (found by tag), starts it instead. The VM gets an Elastic IP for a stable address. On first boot, the VM bootstraps itself with Docker, devcontainer CLI, tmux, mosh-server, Git, GitHub CLI, Node.js, and AWS CLI. Base image is Ubuntu 24.04 LTS, resolved dynamically. Instance type defaults to m6i.xlarge (4 vCPU, 16GB). All resources are tagged.

**`mint down [--vm <name>]`** — Stops the VM. EBS volume persists. Compute billing stops. Elastic IP remains allocated so the address doesn't change on next start.

**`mint destroy [--vm <name>]`** — Terminates the VM and releases all associated resources (EBS, Elastic IP) found by tag.

**`mint list`** — Shows all VMs owned by the current user with their state (running, stopped), IP, uptime, and idle timer status.

**`mint status [--vm <name>]`** — Detailed status for a VM: state, IP, instance type, volume size, running devcontainers, tmux sessions, idle timer remaining.

### Connecting

**`mint ssh [--vm <name>]`** — Opens an SSH session to the VM.

**`mint mosh [--vm <name>]`** — Opens a mosh session to the VM.

**`mint connect [session] [--vm <name>]`** — Opens a mosh session and automatically attaches to the named tmux session. If no session name is given, presents a session picker.

**`mint sessions [--vm <name>]`** — Lists active tmux sessions on the VM.

### Projects

Projects live on VMs. A single VM typically hosts multiple projects, each in its own devcontainer.

**`mint project add <git-url> [--branch <branch>] [--name <name>] [--vm <name>]`** — On the specified VM: clones the repo, builds the devcontainer, and creates a named tmux session with a `docker exec` shell into the running container. The project name defaults to the repo name. The repo URL and branch are not stored in Mint's config — this is an imperative action on the VM.

**`mint project list [--vm <name>]`** — Lists projects on the VM by inspecting running devcontainers and project directories.

**`mint project rebuild <project> [--vm <name>]`** — Tears down and rebuilds the devcontainer for a project.

### Idle Management

**`mint extend [minutes] [--vm <name>]`** — Resets the idle auto-stop timer. Defaults to the configured timeout.

### Configuration

**`mint config`** — Shows current configuration.

**`mint config set <key> <value>`** — Sets a configuration value (e.g. `mint config set idle.timeout_minutes 90`).

Configuration covers: AWS region, default instance type, default volume size, idle timeout, and owner identifier. It does not store project or repo information — that lives on the VMs themselves.

## Auto-Stop

Each VM runs an idle detection system (systemd timer checking every 5 minutes). A VM is considered active if any of these are true:

- An SSH or mosh session is connected
- A tmux session has an attached client
- A `claude` process is running inside any container
- The idle timer was manually extended

When none are true for the configured timeout (default 60 minutes), the VM stops itself using the IAM permissions from the instance role.

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

Each VM has its own Elastic IP, EBS volume, idle timer, and set of devcontainers. They share the user's security group and key pair.

## EC2 Instance Details

- **AMI**: Ubuntu 24.04 LTS, resolved via SSM parameter (not hardcoded)
- **Instance type**: m6i.xlarge (4 vCPU, 16GB RAM), configurable per VM
- **Storage**: 100GB gp3 EBS root volume, persists across stop/start
- **Elastic IP**: One per VM, stable across stop/start cycles
- **Security group**: Shared across user's VMs (SSH + mosh ports)
- **Instance profile**: `mint-instance-profile` (admin-created, shared)

### Software installed on first boot

Docker Engine, Docker Compose, devcontainer CLI, tmux (with mouse support and large scroll buffer), mosh-server, Git, GitHub CLI, Node.js LTS, AWS CLI v2.

### tmux configuration

Mouse support enabled, 50k line scroll buffer, 256-color terminal. This is host-level tmux — not inside containers.

## Authentication

**AWS**: Users authenticate via standard AWS CLI credentials (profiles, SSO, env vars). Mint uses whatever `aws` is configured to use.

**Claude Code**: Users authenticate interactively on first connect. Claude Code prompts for login. Mint does not manage Anthropic credentials.

**SSH/mosh**: Key pair generated by `mint init`, stored locally. User imports into Termius manually (documented).

## VS Code Integration

The primary workflow uses VS Code Remote-SSH. After `mint up`, the developer:

1. Adds the VM's Elastic IP to their SSH config (or uses the key directly)
2. Connects via Remote-SSH in VS Code
3. Opens the project folder, which has a devcontainer configuration
4. VS Code detects and reopens in the devcontainer
5. Claude Code runs in the integrated terminal

Mint documents this workflow but does not automate VS Code configuration — VS Code's existing Remote-SSH and Dev Containers extensions handle it natively.

## Future Considerations (out of scope for v1)

- Spot instance support for cost savings
- Automatic devcontainer rebuild on git push via webhook
- Instance type scaling (resize a running VM)
- EBS snapshot/restore for fast recreation
- Team shared instances with per-user tmux sessions
- Push notifications when Claude Code needs input
- `mint ssh-config` command to auto-generate SSH config entries


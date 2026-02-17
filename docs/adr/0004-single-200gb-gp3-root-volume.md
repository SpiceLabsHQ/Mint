# ADR-0004: Three-Tier Storage Model

## Status

Accepted (supersedes original single-volume decision)

## Context

The original ADR-0004 specified a single 200GB gp3 root volume for everything — OS, Docker, and project code. The squadron design review identified that different categories of data have fundamentally different lifecycle semantics, and conflating them on a single volume creates problems:

- **User configuration** (dotfiles, `~/.ssh/authorized_keys`, Claude Code auth state) must persist across ALL VMs and all lifecycle operations. A user recreating or destroying a VM should not lose their shell preferences or lose Claude Code authentication state.
- **Project source code** must persist across VM recreations (new instance type, fresh OS) but is naturally VM-scoped — it belongs to a particular project environment, not the user's identity.
- **The OS and Docker layer** are ephemeral by nature. They should be rebuildable without losing user data or project code. Treating them as precious increases operational risk and migration cost.

Colocating all three on a single root volume means any lifecycle operation that touches the OS (recreate, destroy) also destroys user config and project code. This is incorrect behavior for a developer environment tool.

## Decision

Use a three-tier storage model with distinct volumes for each tier, each with its own lifecycle semantics.

### Tier 1: Root EBS (ephemeral, OS layer)

- **Size**: 200GB gp3
- **Contents**: OS, Docker Engine, Docker image layers and container data, system packages
- **Lifecycle**: Created fresh on `mint up` and `mint recreate`. Destroyed on `mint destroy` and `mint recreate`.
- **Tag**: `mint:component=volume`

This volume is intentionally disposable. Its contents can always be reconstructed by re-running the bootstrap script and pulling Docker images.

### Tier 2: User EFS (persistent, user-scoped)

- **Type**: Amazon EFS access point
- **Contents**: Dotfiles, `~/.ssh/authorized_keys`, Claude Code auth state, user customizations
- **Lifecycle**: Created during `mint init` as a per-user EFS access point. The underlying EFS filesystem is created once per AWS account during admin setup (CloudFormation template). Persists across ALL lifecycle operations including `mint destroy`.
- **Tag**: `mint:component=efs-access-point`
- **Scope**: Mounted on every VM the user runs. A user with multiple VMs (via `--vm`) shares the same EFS access point across all of them.
- **Security group**: A single shared `mint-efs` security group is created once by the admin via CloudFormation. It has an NFS inbound rule referencing itself (self-referencing SG), so any instance launched with this SG can reach the EFS mount target. Every Mint VM is launched with this shared SG attached.

This tier represents the user's identity within the tool. It is never destroyed by normal lifecycle commands.

### Tier 3: Project EBS (persistent, VM-scoped)

- **Size**: 50GB gp3, configurable via `volume_size_gb` (minimum 50GB per ADR-0012)
- **Type**: gp3 EBS volume
- **Contents**: Project source code, local build artifacts, project-specific configuration
- **Mount point**: `/mint/projects`
- **Lifecycle**: Created on `mint up`. Persists across `mint resize` (same instance, no volume manipulation required) and `mint recreate` (detached from terminated instance, reattached to new instance). Destroyed on `mint destroy`.
- **Tags**: `mint:component=project-volume`, `mint:vm=<name>`
- **Scope**: Bound to a named VM, not to the underlying EC2 instance.

This volume survives instance replacement but is destroyed when the VM environment itself is destroyed.

## Key Constraints

- **AZ affinity**: EBS volumes are Availability Zone-scoped. `mint recreate` must launch the replacement instance in the same AZ as the existing project volume. The AZ is recorded implicitly via the volume's existing placement and must be honored when launching the new instance.
- **`mint resize` simplicity**: Changing instance type on the same instance requires no volume manipulation (stop instance → modify instance type → start instance). The project EBS remains attached throughout.
- **`mint recreate` complexity**: This is the most operationally complex lifecycle command. It must: (0) stop the instance, (1) detach the project EBS, (2) terminate the old instance (destroying the root EBS), (3) launch a new instance in the same AZ, (4) reattach the project EBS, (5) mount EFS.
- **EFS mount point**: `/mint/user`. Avoids collision with Ubuntu's default home directory skeleton. Symlinks from `$HOME` well-known paths (`~/.ssh`, `~/.config/claude`, dotfiles) into `/mint/user/` provide seamless user experience while keeping the mount point deterministic. If `/mint/user` is unmounted, the VM is detectably misconfigured.

## Consequences

- **Increased provisioning complexity**: `mint up` creates two EBS volumes and mounts EFS instead of creating a single volume. Bootstrap script must handle EFS mount and project volume attachment.
- **Admin setup required**: The EFS filesystem must be created before any user can run `mint init`. This requires a CloudFormation template or equivalent one-time setup step.
- **`mint init` creates EFS access point**: Per-user onboarding includes an AWS API call to create an EFS access point. The shared `mint-efs` security group is created once by the admin via CloudFormation and attached to every Mint VM at launch.
- **`mint recreate` is the most complex lifecycle operation**: Detach, terminate, launch (AZ-constrained), reattach is a multi-step operation with failure modes at each step. Error handling and rollback behavior must be carefully specified (see ADR-0017).
- **Clear separation of concerns**: The OS is disposable, user config is permanent, and project code is VM-scoped. Each tier can be reasoned about independently.
- **Volume behavior is precisely defined per lifecycle operation**: The lifecycle semantics for each tier are documented in ADR-0017.
- **`mint status` disk reporting**: Still relevant for the root EBS and project EBS. EFS usage reporting may differ due to its managed nature.

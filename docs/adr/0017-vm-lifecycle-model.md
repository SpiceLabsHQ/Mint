# ADR-0017: VM Lifecycle Model

## Status
Accepted

## Context
Mint manages EC2-based development environments with a three-tier storage architecture (ADR-0004). Previously, the VM lifecycle was implicit — `mint up` created, `mint down` stopped, `mint destroy` terminated. The introduction of three storage tiers with different persistence semantics requires a formal lifecycle model that precisely defines how each operation affects each resource type. Two new operations were identified: `mint resize` (change instance type without losing any data) and `mint recreate` (fresh OS/Docker while preserving project data and user config).

## Decision
Five lifecycle operations, each with precisely defined behavior for every resource type:

| Operation | Root EBS (OS/Docker) | User EFS (config) | Project EBS (source) | EIP | Instance |
|-----------|---------------------|-------------------|---------------------|-----|----------|
| `mint up` | Create | Mount | Create | Allocate | Launch |
| `mint down` | Keep | Unmounts naturally | Keep | Keep | Stop |
| `mint resize` | Keep | Stays mounted | Keep | Keep | Stop → modify type → start |
| `mint recreate` | Destroy + new | Mount | Detach + reattach | Keep | Terminate + launch |
| `mint destroy` | Destroy | Unmounts naturally | Destroy | Release | Terminate |

### Operation Details

**`mint up`** — Creates all resources fresh. New EC2 instance, new root EBS, new project EBS, allocates Elastic IP, mounts user EFS. This is the full provisioning path. If a stopped VM with the same name exists (found by tag), starts it instead (equivalent to reversing `mint down`).

**`mint down`** — Stops the VM. All volumes persist. EFS unmounts naturally when the instance stops. Compute billing stops. EIP remains allocated for a stable address on next start.

**`mint resize`** — Changes instance type without touching any storage. Stops the instance, modifies the instance type attribute, starts the instance. All volumes remain attached. This is a native EC2 operation taking ~60 seconds. No volume manipulation needed.

**`mint recreate`** — Replaces the instance and root volume while preserving project data and user config. Requires no active SSH, mosh, or tmux sessions (use `--force` to override). Orchestration sequence:
1. Check for active sessions; refuse if any detected (unless `--force`)
2. Query project EBS volume's AZ via `DescribeVolumes`
3. Tag project EBS with `mint:pending-attach=true` for failure recovery
4. Stop instance
5. Detach project EBS volume
6. Terminate instance (destroys root EBS)
7. Launch new instance in the **same AZ** as project EBS (select matching subnet)
8. Attach project EBS to new instance; clear `mint:pending-attach` tag
9. Mount project EBS at project directory
10. EFS mounts via fstab during boot
11. Bootstrap runs on new root volume
12. Health check validates all components; tags instance `mint:bootstrap=complete` on success
13. Report success to user

If a recreate fails mid-sequence (e.g. new instance launch fails, EBS reattachment fails), `mint up` detects the `mint:pending-attach` tag on the project EBS and resumes the reattachment sequence. If the project EBS volume is missing (manually deleted), `mint recreate` fails fast with guidance to use `mint destroy` instead.

Use cases: fresh OS after bootstrap updates, recovering from a corrupted root volume, upgrading to a new Ubuntu LTS.

**`mint destroy`** — Fully destructive. Terminates the instance, deletes root EBS, deletes project EBS, releases Elastic IP. User EFS unmounts naturally (it is user-scoped, not VM-scoped, and persists independently). Requires interactive confirmation (`--yes` to skip).

### Key Constraints

- **AZ pinning**: `mint recreate` must launch the new instance in the same AZ as the project EBS volume. EBS volumes cannot be attached across AZs.
- **EIP stability**: `mint resize` and `mint recreate` preserve the Elastic IP, so the VM's public address does not change.
- **EFS independence**: The user's EFS volume is not tied to any specific VM. It survives `mint destroy` and is available to any VM the user creates.

## Consequences
- **Precise control.** Five distinct lifecycle operations give users clear, predictable control over what persists and what is rebuilt. The volume behavior table is the canonical reference for implementers and documentation.
- **Implementation sequencing.** `mint resize` is the simplest lifecycle change — a good early implementation target and confidence builder. `mint recreate` is the most complex command in the CLI and should be implemented after simpler lifecycle commands are stable.
- **Dual upgrade paths.** Users can resize for capacity changes (cheap, fast, ~60 seconds) and recreate for OS-level changes (more involved, rebuilds containers). This distinction maps cleanly to different user needs.
- **AZ coupling.** `mint recreate` introduces a hard dependency on AZ consistency between the new instance and the existing project EBS volume. The implementation must record or query the volume's AZ before terminating the instance.
- **EFS durability.** Because EFS is user-scoped rather than VM-scoped, `mint destroy` is less destructive than it appears — user config survives and is immediately available on the next `mint up`.

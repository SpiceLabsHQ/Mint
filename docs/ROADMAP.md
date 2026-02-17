# Mint -- Implementation Roadmap

This roadmap breaks the Mint CLI into five phases, ordered by dependency and shippability. Each phase produces a working subset of Mint.

## Phase 0: Project Scaffold and Foundation ✅

**Goal**: Buildable Go binary with CLI framework, config management, and AWS identity resolution. No AWS resources created yet.

**Commands functional after this phase**:
- `mint version`
- `mint config`
- `mint config set <key> <value>`

**Work**:
- Initialize Go module, set up cobra CLI framework, wire viper for TOML config
- Implement `~/.config/mint/config.toml` read/write with the flat snake_case schema (region, instance_type, volume_size_gb, idle_timeout_minutes, ssh_config_approved)
- Implement `mint config set` with aggressive validation: instance_type validated against AWS API, volume_size_gb >= 50, idle_timeout_minutes >= 15, unknown keys rejected
- Implement owner identity derivation from `sts get-caller-identity` with ARN normalization (ADR-0013)
- Implement `--verbose`, `--debug`, `--json` global flag plumbing
- Implement structured logging to `~/.config/mint/logs/` and audit logging to `~/.config/mint/audit.log`
- Set up GoReleaser config for cross-platform builds with checksum generation
- Set up CI pipeline (build, test, lint)
- Write the bootstrap user-data script and implement SHA256 hash embedding via `go:generate` (ADR-0009)

**Risks**: None significant. This is foundational scaffolding with no cloud resource management.

**Dependencies**: None.

---

## Phase 1: Init and Single-VM Provisioning ✅

**Goal**: A developer can run `mint init`, `mint up`, connect via SSH, and `mint down` to stop the VM. This is the minimum viable product -- a working cloud dev environment with the primary connectivity workflow.

**Commands functional after this phase**:
- `mint init`
- `mint up` (create and start)
- `mint down`
- `mint ssh`
- `mint ssh-config`
- `mint code`
- `mint list`
- `mint status`
- `mint destroy`

**Work**:

*Admin setup (prerequisite)*:
- Write CloudFormation template for IAM role (`mint-instance-role`), instance profile (`mint-instance-profile`), EFS filesystem, and `mint-efs` security group
- Document admin setup procedure

*`mint init`*:
- Validate default VPC exists with public subnet in configured region
- Validate admin-created instance profile exists
- Validate EFS filesystem exists
- Create user security group (TCP 41122, UDP 60000-61000, open inbound) with Mint tags
- Create per-user EFS access point on the shared EFS filesystem
- Write initial config

*`mint up`*:
- Resolve Ubuntu 24.04 LTS AMI via SSM parameter
- Check EIP quota before allocation (ADR squadron HIGH risk -- fail fast with count, limit, console link, remediation guidance)
- Launch EC2 instance with instance profile, user security group, `mint-efs` security group, and user-data script
- Create and attach project EBS volume (gp3, configurable size)
- Allocate and associate Elastic IP
- Verify bootstrap script hash before sending to EC2 (ADR-0009)
- Poll for `mint:bootstrap=complete` tag with 7-minute timeout
- On timeout: prompt user to stop, terminate, or leave running (ADR-0009)
- Tag all resources with the full tag schema (ADR-0001)
- Auto-generate SSH config entry with permission prompt on first run (ADR-0015)
- Handle "start stopped VM" path (detect existing stopped VM by tags, start it)

*Bootstrap script*:
- Install Docker Engine, Docker Compose, devcontainer CLI, tmux, mosh-server, Git, GitHub CLI, Node.js LTS, AWS CLI v2, EC2 Instance Connect agent
- Configure SSH on port 41122 with password auth disabled
- Configure tmux (mouse support, 50k scroll buffer, 256-color)
- Mount EFS at `/mint/user` with symlinks into `$HOME`
- Format and mount project EBS at `/mint/projects`
- Write fstab entries for EFS and project EBS
- Write bootstrap version to `/var/lib/mint/bootstrap-version`
- Run health check, tag instance `mint:bootstrap=complete`
- Install reconciliation systemd unit (detect-only, sets `mint:health` tag)
- Install idle detection systemd timer and service (ADR-0018)

*Connectivity*:
- `mint ssh` via EC2 Instance Connect (push ephemeral key, connect on port 41122)
- `mint ssh-config` with ProxyCommand routing through Instance Connect, managed block with checksum detection (squadron MEDIUM risk -- warn on hand-edits)
- `mint code` wrapping `code --remote ssh-remote+mint-<vm>`

*Listing and status*:
- `mint list` with state, IP, uptime, idle timer warning for VMs exceeding timeout, `--json` output
- `mint status` with state, IP, instance type, volume sizes, `--json` output
- Version-check notice against GitHub Releases API (cached 24 hours, fails open)

*`mint down`*:
- Stop instance (all volumes persist, EIP remains allocated)

*`mint destroy`*:
- Interactive confirmation (type VM name to confirm, `--yes` to skip)
- Terminate instance, delete root EBS, delete project EBS, release EIP
- EFS unmounts naturally

**Risks**:
- **[HIGH] EFS admin setup**: CloudFormation template, access point creation, mount configuration, symlink strategy.
- **[HIGH] EIP quota exhaustion**: Must be handled before allocation, not after.
- **[MEDIUM] Bootstrap script reliability**: No CI validation of the script yet. Manual testing only. Bugs here are expensive (7-minute timeout per attempt).
- **[MEDIUM] SSH config clobbering**: Managed block with checksum detection.

**Dependencies**: Admin must deploy CloudFormation template before any developer can run `mint init`.

---

## Phase 2: Connectivity and Session Management ✅

**Goal**: Complete the secondary workflow (iPad/mosh/tmux) and project management. After this phase, both primary and secondary usage patterns are fully functional.

**Commands functional after this phase**:
- `mint mosh`
- `mint connect`
- `mint sessions`
- `mint key add`
- `mint project add`
- `mint project list`
- `mint project rebuild`
- `mint extend`

**Work**:

*mosh connectivity*:
- `mint mosh` using EC2 Instance Connect for initial SSH handshake, then UDP
- `mint connect [session]` opens mosh and attaches to named tmux session; session picker if no name given
- `mint sessions` lists active tmux sessions on the VM

*Key management escape hatch*:
- `mint key add <public-key>` appends to `~/.ssh/authorized_keys` via Instance Connect session
- Accept file path or stdin

*Project management*:
- `mint project add <git-url>` clones repo on VM, builds devcontainer via BuildKit, creates named tmux session with `docker exec` shell
- `mint project list` inspects running devcontainers and project directories, shows state (running/stopped), `--json` output
- `mint project rebuild <project>` tears down and rebuilds the devcontainer

*Idle management*:
- `mint extend [minutes]` SSHs into VM and writes timestamp to `/var/lib/mint/idle-extended-until`

**Risks**:
- **mosh on non-standard port**: The initial SSH handshake uses port 41122. Must verify mosh client passes the correct port.
- **devcontainer build failures**: BuildKit cache configuration and devcontainer CLI invocation on the remote VM. Errors must be surfaced clearly.

**Dependencies**: Phase 1 complete. VM must be running with mosh-server and tmux installed by bootstrap.

---

## Phase 3: Lifecycle Operations, Health, and Self-Update

**Goal**: Complete the VM lifecycle model (ADR-0017) with `mint resize` and `mint recreate`, add production hardening with `mint doctor` and `mint update`, and build the bootstrap CI pipeline. After this phase, all five lifecycle verbs work and the CLI can diagnose and repair its own environment.

**Commands functional after this phase**:
- `mint resize`
- `mint recreate`
- `mint doctor [--fix]`
- `mint update`

**Work**:

*`mint resize`*:
- Stop instance, modify instance type attribute, start instance
- All volumes remain attached (no volume manipulation)
- Validate instance type against AWS API before proceeding
- ~60 second operation; show progress phases

*`mint recreate`*:
- Check for active SSH/mosh/tmux sessions; refuse if detected (unless `--force`)
- Query project EBS volume AZ via `DescribeVolumes` (this happens first -- if it fails, no state has changed)
- Tag project EBS with `mint:pending-attach` for failure recovery
- Stop instance
- Detach project EBS
- Terminate instance (destroys root EBS)
- Launch new instance in same AZ (select matching subnet)
- Attach project EBS, remove `mint:pending-attach` tag
- Mount project EBS at `/mint/projects`
- EFS mounts via fstab during boot
- Bootstrap runs on new root volume
- Health check validates, tags `mint:bootstrap=complete`
- Interactive confirmation required

*Failure recovery*:
- `mint up` detects `mint:pending-attach` tag on project EBS and resumes reattachment
- If project EBS is missing (manually deleted), fail fast with guidance to use `mint destroy`

*Host key handling*:
- `mint recreate` produces a new host key; next connection triggers TOFU change detection (ADR-0019)
- Prominent warning with old/new fingerprints and likely cause
- User prompt to accept or reject

*`mint doctor`*:
- Check AWS credentials validity
- Check region configuration
- Check service quota headroom (Elastic IPs, vCPUs)
- Check SSH config sanity (managed block intact, no stale entries)
- If VMs running: check VM health, disk usage, component versions, `mint:health` tag status
- `--vm` to target a specific VM
- `--fix` triggers explicit repair of detected drift (re-run specific component installs, not full bootstrap)
- `--json` for machine-readable output

*`mint update`*:
- Download latest release from GitHub Releases
- Verify checksum
- Atomic binary replacement (download to temp, verify, `mv`)
- Leave existing binary untouched if checksum fails

*Disk usage alerting*:
- Journald threshold warning at 80% root volume usage
- Surface in `mint status` output

*Bootstrap CI pipeline*:
- CI job that launches a test EC2 instance, validates all bootstrap components, terminates
- Run on every commit that touches the bootstrap script
- Verify embedded hash matches script content

**Risks**:
- **[HIGH] Three-tier storage lifecycle complexity**: The detach/terminate/launch/reattach sequence in `mint recreate` is the most complex command in the CLI. Each step has failure modes.
- **[MEDIUM] AZ pinning**: New instance must launch in the same AZ as the project EBS volume. Must select the correct subnet.
- **Failure recovery edge cases**: What if the old instance is terminated but the new one fails to launch? What if EBS attachment fails on the new instance? Each failure mode needs explicit handling.
- **[MEDIUM] Bootstrap CI pipeline**: EC2-based CI testing must be reliable. Flaky tests in CI are worse than no tests.
- **[MEDIUM] Disk usage alerting**: Must work reliably in the systemd/journald environment.

**Dependencies**: Phase 1 complete. Phase 2 is not a prerequisite -- lifecycle operations, doctor, and update are independent of mosh/project commands.

---

## Phase 4: Polish and Distribution

**Goal**: Ship-ready CLI with Homebrew distribution, documentation, and the remaining UX polish items.

**Work**:
- Homebrew tap formula via GoReleaser
- Install script (`curl -fsSL ... | sh`) for non-macOS
- User-facing documentation: getting started guide, admin setup guide, command reference
- CloudFormation template documentation with exact IAM policy JSON
- Multiple VM support testing (warn at 3+ running VMs, no hard limit)
- Error message audit across all commands (actionable, specific, includes remediation)
- Progress spinner TTY detection with line-by-line fallback for non-interactive environments
- gp3 IOPS configuration (squadron LOW risk -- `--volume-iops` override, default 3000, max 16000)
- End-to-end workflow testing: MacBook primary flow and iPad secondary flow

**Risks**: Low. This phase is polish and packaging, not new architecture.

**Dependencies**: Phases 1-3 complete.

---

## Phase Summary

| Phase | What Ships |
|-------|------------|
| **0 -- Scaffold** | **Buildable CLI with config and identity** ✅ |
| **1 -- Init and Provisioning** | **Create, connect, stop, destroy a VM** ✅ |
| **2 -- Connectivity and Projects** | **mosh, tmux, projects, iPad workflow** ✅ |
| 3 -- Lifecycle, Health, and Self-Update | resize, recreate, doctor, update, bootstrap CI |
| 4 -- Polish and Distribution | Homebrew, docs, production-ready |

---

## v2 Horizon

These items are explicitly deferred from v1 per the spec and ADRs. They are listed here for planning visibility, not as commitments.

**Cost safety**:
- Dead-man's switch Lambda (ADR-0011): CloudWatch-scheduled Lambda checks heartbeat tags, force-stops stale instances. Catches all auto-stop failure modes.
- Spot instance support for cost savings on interruptible workloads.

**Security hardening**:
- Binary signing: minisign initially, then cosign with keyless signing via GitHub Actions OIDC (ADR-0020).
- IAM permission boundaries for untrusted multi-user environments.

**Developer experience**:
- `mint up --project <git-url>`: Provision and clone in one command.
- Automatic devcontainer rebuild on git push via webhook.
- Push notifications when Claude Code needs input.

**Data management**:
- EBS snapshot/restore for fast VM recreation.
- Team shared instances with per-user tmux sessions.

**Priority guidance for v2**: The dead-man's switch Lambda should be the first v2 item. It is the only external backstop for auto-stop failures, and the cost risk compounds over time as more users adopt Mint. Binary signing is second -- it becomes important when distribution extends beyond a single trusted team.

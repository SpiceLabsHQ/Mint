# Command Reference

Complete reference for every `mint` command, organized by function.

---

## Global Flags

These flags are available on all commands.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--verbose` | bool | `false` | Show progress steps during command execution |
| `--debug` | bool | `false` | Show AWS SDK details for troubleshooting |
| `--json` | bool | `false` | Machine-readable JSON output (supported on list, status, sessions, config, project list, doctor, init, up) |
| `--yes` | bool | `false` | Skip confirmation prompts on destructive operations |
| `--vm <name>` | string | `"default"` | Target VM name. Can be omitted for single-VM users |

The `--json` flag follows [ADR-0012](adr/0012-cli-ux-conventions.md). The `--vm` flag enables multi-VM workflows per [ADR-0002](adr/0002-single-vm-hosts-multiple-projects.md).

---

## VM Lifecycle

Commands that create, stop, destroy, resize, or recreate VMs.

### `mint up`

Provision a new VM or start a stopped one.

```
mint up [flags]
```

Creates an EC2 instance, project EBS volume, and Elastic IP. If a VM already exists and is stopped, it starts the existing instance instead. After provisioning, the bootstrap process installs required software (Docker, tmux, mosh-server, devcontainer CLI). If SSH config write approval has been granted, the SSH config entry is auto-generated.

**Flags:** Global flags only.

**Requires:** `mint init` must have been run first to create the admin EFS filesystem and per-user resources.

**Examples:**

```bash
# Provision or start the default VM
mint up

# Provision with progress output
mint up --verbose

# Provision a named VM
mint up --vm staging

# Machine-readable output
mint up --json
```

**JSON output fields:** `instance_id`, `public_ip`, `volume_id`, `allocation_id`, `restarted`, `bootstrap_error` (if applicable).

---

### `mint down`

Stop the VM instance.

```
mint down [flags]
```

Stops the EC2 instance. All volumes and the Elastic IP persist for the next `mint up`. If the VM is already stopped, the command exits gracefully with a message.

**Flags:** Global flags only.

**Examples:**

```bash
# Stop the default VM
mint down

# Stop with progress output
mint down --verbose

# Stop a named VM
mint down --vm staging
```

---

### `mint destroy`

Terminate the VM and clean up all associated resources.

```
mint destroy [flags]
```

Permanently destroys the VM. The following resources are cleaned up:

- EC2 instance is terminated (root EBS is auto-destroyed by EC2)
- Project EBS volumes are deleted
- Elastic IP is released
- User EFS access point is **preserved** (persistent across VMs)

Requires interactive confirmation: you must type the VM name to proceed. Use `--yes` to skip.

**Flags:** Global flags only. Use `--yes` to bypass the confirmation prompt.

**Examples:**

```bash
# Destroy with interactive confirmation
mint destroy

# Destroy without confirmation
mint destroy --yes

# Destroy a named VM
mint destroy --vm staging --yes
```

---

### `mint resize`

Change the VM instance type.

```
mint resize <instance-type> [flags]
```

Stops the VM (if running), changes its instance type, and restarts it. If the VM is already stopped, only the instance type is changed and the VM remains stopped. The new instance type is validated against the AWS API before any changes are made.

**Arguments:**

| Argument | Required | Description |
|----------|----------|-------------|
| `instance-type` | Yes | The EC2 instance type to switch to (e.g., `m7i.xlarge`) |

**Flags:** Global flags only.

**Examples:**

```bash
# Resize to a larger instance
mint resize m7i.2xlarge

# Resize a named VM
mint resize c7i.xlarge --vm dev
```

---

### `mint recreate`

Destroy and re-provision the VM with the same configuration.

```
mint recreate [flags]
```

Destroys the current VM and creates a fresh one, preserving the project EBS volume. The 9-step lifecycle:

1. Query project EBS volume
2. Tag volume with pending-attach (crash recovery safety net)
3. Stop instance
4. Detach project EBS
5. Terminate instance
6. Launch new instance in same AZ
7. Attach project EBS and remove pending-attach tag
8. Reassociate Elastic IP
9. Poll for bootstrap complete

Active sessions are detected before proceeding. If SSH or mosh sessions are active, the command is blocked unless `--force` is used. Requires interactive confirmation (type the VM name) unless `--yes` is set. The cached TOFU host key is cleared after recreate so the next connection records the new key ([ADR-0019](adr/0019-ssh-host-key-tofu.md)).

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--force` | bool | `false` | Bypass active session guard |

**Examples:**

```bash
# Recreate with confirmation
mint recreate

# Recreate and skip session guard
mint recreate --force --yes

# Recreate a named VM
mint recreate --vm dev --yes
```

---

## Connectivity

Commands for connecting to VMs via SSH, mosh, VS Code, and managing sessions.

All connectivity commands use **EC2 Instance Connect** for ephemeral SSH key management ([ADR-0007](adr/0007-ec2-instance-connect-ssh.md)). No SSH keys are stored locally. SSH runs on **port 41122** (non-standard port per [ADR-0016](adr/0016-non-standard-ports-replace-ip-scoping.md)). Host key verification uses trust-on-first-use (TOFU) per [ADR-0019](adr/0019-ssh-host-key-tofu.md).

### `mint ssh`

SSH into the VM using ephemeral keys.

```
mint ssh [-- extra-ssh-args] [flags]
```

Connects to the VM via SSH using EC2 Instance Connect. Extra SSH arguments can be passed after `--` for port forwarding, X11 forwarding, or other SSH options.

**Flags:** Global flags only.

**Examples:**

```bash
# SSH into the default VM
mint ssh

# SSH with port forwarding
mint ssh -- -L 8080:localhost:8080

# SSH into a named VM
mint ssh --vm staging

# SSH with multiple forwarded ports
mint ssh -- -L 3000:localhost:3000 -L 5432:localhost:5432
```

---

### `mint mosh`

Open a mosh session to the VM using ephemeral keys.

```
mint mosh [flags]
```

Connects via mosh for roaming, intermittent-connectivity sessions. Ideal for iPads and unreliable networks. Requires `mosh` to be installed locally (`brew install mosh` on macOS, `apt install mosh` on Linux). Mosh uses UDP ports 60000-61000 per [ADR-0016](adr/0016-non-standard-ports-replace-ip-scoping.md).

**Flags:** Global flags only.

**Examples:**

```bash
# Connect via mosh
mint mosh

# Connect to a named VM via mosh
mint mosh --vm dev
```

---

### `mint connect`

Connect to a tmux session on the VM via mosh.

```
mint connect [session] [flags]
```

Combines mosh with tmux for persistent, roaming sessions. If a session name is provided, creates or attaches to that session. If no session name is given, lists available sessions and presents an interactive picker. When only one session exists, it is auto-selected.

Requires `mosh` to be installed locally. Tmux runs on the host (not inside containers) per [ADR-0003](adr/0003-tmux-on-host-not-in-containers.md).

**Arguments:**

| Argument | Required | Description |
|----------|----------|-------------|
| `session` | No | Name of the tmux session to connect to |

**Flags:** Global flags only.

**Examples:**

```bash
# Pick from available sessions interactively
mint connect

# Connect to a specific session
mint connect my-project

# Connect to a session on a named VM
mint connect my-project --vm dev
```

---

### `mint sessions`

List tmux sessions on the VM.

```
mint sessions [flags]
```

Shows active tmux sessions with name, window count, attached status, and creation time.

**Flags:** Global flags only. Supports `--json` for machine-readable output.

**Examples:**

```bash
# List all sessions
mint sessions

# JSON output for scripting
mint sessions --json
```

**JSON output fields (per session):** `name`, `windows`, `attached`, `created_epoch`, `created_at`.

---

### `mint ssh-config`

Manage SSH config entries for mint VMs.

```
mint ssh-config [flags]
```

Generates and manages SSH config Host blocks in `~/.ssh/config`. Managed blocks are marked with `# mint:begin` / `# mint:end` markers and include a SHA256 checksum for hand-edit detection. Requires SSH config write approval per [ADR-0015](adr/0015-permission-before-modifying-user-files.md).

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--hostname` | string | | Public IP or hostname of the VM (required for generation) |
| `--instance-id` | string | | EC2 instance ID for ProxyCommand (required) |
| `--az` | string | | Availability zone for EC2 Instance Connect (required) |
| `--ssh-config-path` | string | `~/.ssh/config` | Path to SSH config file |
| `--remove` | bool | `false` | Remove the managed block for the VM |

**Examples:**

```bash
# Generate an SSH config entry
mint ssh-config --hostname 54.123.45.67 --instance-id i-0abc123def --az us-east-1a --yes

# Remove the SSH config entry for the default VM
mint ssh-config --remove

# Remove the entry for a named VM
mint ssh-config --remove --vm staging
```

Note: `mint up` and `mint code` auto-generate SSH config entries when `ssh_config_approved` is set to `true` in the mint config.

---

### `mint code`

Open VS Code connected to the VM.

```
mint code [flags]
```

Opens VS Code with Remote-SSH connected to the VM. Ensures the SSH config entry exists before launching. Requires `ssh_config_approved` to be `true` in the mint config. This is the primary workflow entry point: MacBook to VS Code Remote-SSH to EC2 host.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--path` | string | `/home/ubuntu` | Remote directory to open in VS Code |

**Examples:**

```bash
# Open VS Code connected to the default VM
mint code

# Open a specific directory
mint code --path /mint/projects/my-app

# Open VS Code to a named VM
mint code --vm dev --path /mint/projects/api
```

---

### `mint key add`

Add an SSH public key to the VM.

```
mint key add <public-key> [flags]
```

Adds an SSH public key to the VM's `~/.ssh/authorized_keys`. This is the escape hatch for clients that cannot use EC2 Instance Connect (e.g., some mobile apps). See [ADR-0007](adr/0007-ec2-instance-connect-ssh.md).

The argument can be:
- A file path (e.g., `~/.ssh/id_ed25519.pub`)
- A key string (e.g., `ssh-ed25519 AAAA...`)
- `-` to read from stdin

The key is validated for format safety before being added. Duplicate keys are detected and skipped.

**Arguments:**

| Argument | Required | Description |
|----------|----------|-------------|
| `public-key` | Yes | SSH public key (file path, key string, or `-` for stdin) |

**Flags:** Global flags only.

**Examples:**

```bash
# Add a key from a file
mint key add ~/.ssh/id_ed25519.pub

# Add a key from stdin
cat ~/.ssh/id_ed25519.pub | mint key add -

# Add a key to a named VM
mint key add ~/.ssh/id_ed25519.pub --vm staging
```

---

## Project Management

Commands for cloning repos, building devcontainers, and managing projects on the VM.

### `mint project add`

Clone a repo, build its devcontainer, and create a tmux session.

```
mint project add <git-url> [flags]
```

Clones a git repository to `/mint/projects/<name>` on the VM, runs `devcontainer up` to build the development container, and creates a tmux session for the project. The command is resumable: if a previous run was interrupted, it detects existing state (clone, container, session) and resumes from the appropriate step.

**Arguments:**

| Argument | Required | Description |
|----------|----------|-------------|
| `git-url` | Yes | Git repository URL (HTTPS or SSH format) |

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--name` | string | (derived from URL) | Override the project name |
| `--branch` | string | (default branch) | Branch to clone |

**Examples:**

```bash
# Add a project from GitHub
mint project add https://github.com/org/my-app.git

# Add with a custom name and branch
mint project add https://github.com/org/api.git --name backend --branch develop

# Add a project via SSH URL
mint project add git@github.com:org/my-app.git
```

---

### `mint project list`

List projects on the VM.

```
mint project list [flags]
```

Lists project directories under `/mint/projects/` and their devcontainer status (running, exited, none).

**Flags:** Global flags only. Supports `--json` for machine-readable output.

**Examples:**

```bash
# List all projects
mint project list

# JSON output
mint project list --json
```

**JSON output fields (per project):** `name`, `container_status`, `image`.

---

### `mint project rebuild`

Tear down and rebuild a project's devcontainer.

```
mint project rebuild <project-name> [flags]
```

Stops and removes the existing devcontainer for a project, then rebuilds it with `devcontainer up`. The project source code is preserved; only the container is rebuilt. Requires confirmation (type the project name) unless `--yes` is set.

**Arguments:**

| Argument | Required | Description |
|----------|----------|-------------|
| `project-name` | Yes | Name of the project to rebuild |

**Flags:** Global flags only. Use `--yes` to bypass the confirmation prompt.

**Examples:**

```bash
# Rebuild with confirmation
mint project rebuild my-app

# Rebuild without confirmation
mint project rebuild my-app --yes
```

---

## Maintenance

Commands for health checks, updates, and extending the idle timer.

### `mint doctor`

Check environment and VM health.

```
mint doctor [flags]
```

Runs environment health checks and reports results. Checks include:

- **AWS credentials** -- verifies identity resolution via STS
- **Config validation** -- region format, volume_size_gb >= 50, idle_timeout_minutes >= 15
- **SSH config** -- verifies mint managed block exists
- **EIP quota** -- warns when nearing the default limit of 5 Elastic IPs
- **VM health** (per running VM):
  - Health tag status
  - Root volume disk usage (warns at 80%, fails at 90%)
  - Component versions: Docker, devcontainer CLI, tmux, mosh-server
  - `--fix` mode: reinstalls failed components

When `--vm` is specified, only that VM is checked. Otherwise, all running VMs owned by the current user are checked.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--fix` | bool | `false` | Re-install components that failed version checks |

**Flags:** Supports `--json` for machine-readable output.

**Examples:**

```bash
# Run all health checks
mint doctor

# Run with fix mode
mint doctor --fix

# Check a specific VM
mint doctor --vm staging

# JSON output for CI
mint doctor --json
```

**JSON output fields (per check):** `name`, `status` (PASS/FAIL/WARN), `detail`.

---

### `mint update`

Update mint to the latest version.

```
mint update [flags]
```

Downloads the latest release from GitHub, verifies its SHA256 checksum, and replaces the current binary. Checksum verification follows [ADR-0020](adr/0020-binary-signing-deferred-to-v2.md).

**Flags:** Global flags only.

**Examples:**

```bash
# Update to latest version
mint update
```

---

### `mint extend`

Extend the VM idle auto-stop timer.

```
mint extend [minutes] [flags]
```

Resets the idle auto-stop timer on the VM. The idle detection system ([ADR-0018](adr/0018-auto-stop-idle-detection.md)) checks for SSH/mosh sessions, tmux clients, `claude` processes in containers, and manual extend timestamps. This command writes a future timestamp to `/var/lib/mint/idle-extended-until` on the VM.

**Arguments:**

| Argument | Required | Description |
|----------|----------|-------------|
| `minutes` | No | Number of minutes to extend (default: `idle_timeout_minutes` from config, minimum: 15) |

**Flags:** Global flags only.

**Examples:**

```bash
# Extend by the configured default (e.g., 60 minutes)
mint extend

# Extend by a specific duration
mint extend 120

# Extend a named VM
mint extend 90 --vm dev
```

---

## Configuration

Commands for viewing and modifying mint preferences.

Configuration is stored in TOML format at `~/.config/mint/config.toml`. Mint stores only user preferences locally; all resource state is discovered from AWS tags ([ADR-0014](adr/0014-aws-is-source-of-truth.md)).

### `mint config`

Display current configuration.

```
mint config [flags]
```

Shows all configuration values.

**Flags:** Supports `--json` for machine-readable output.

**Configuration keys:**

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `region` | string | | AWS region (e.g., `us-east-1`) |
| `instance_type` | string | | EC2 instance type (e.g., `m7i.xlarge`) |
| `volume_size_gb` | int | `50` | Project EBS volume size in GB (minimum 50) |
| `idle_timeout_minutes` | int | `60` | Idle auto-stop timeout in minutes (minimum 15) |
| `ssh_config_approved` | bool | `false` | Whether mint may write to `~/.ssh/config` |

**Examples:**

```bash
# Show all config
mint config

# JSON output
mint config --json
```

---

### `mint config set`

Set a configuration value.

```
mint config set <key> <value> [flags]
```

Validates and sets a single configuration key. Instance types are validated against the AWS API when a region is configured.

**Arguments:**

| Argument | Required | Description |
|----------|----------|-------------|
| `key` | Yes | Configuration key name |
| `value` | Yes | Value to set |

**Flags:** Global flags only.

**Examples:**

```bash
# Set your AWS region
mint config set region us-west-2

# Set instance type
mint config set instance_type m7i.xlarge

# Set volume size
mint config set volume_size_gb 100

# Set idle timeout
mint config set idle_timeout_minutes 90

# Approve SSH config writes
mint config set ssh_config_approved true
```

---

### `mint config get`

Get a single configuration value.

```
mint config get <key> [flags]
```

Prints the value of a single configuration key. Useful for scripting.

**Arguments:**

| Argument | Required | Description |
|----------|----------|-------------|
| `key` | Yes | Configuration key name (see `mint config` for valid keys) |

**Flags:** Supports `--json` for machine-readable output.

**Examples:**

```bash
# Get the current region
mint config get region

# Get volume size as JSON
mint config get volume_size_gb --json
```

---

### `mint init`

Initialize mint for the current user.

```
mint init [flags]
```

Validates prerequisites and creates per-user resources. This command must be run once before `mint up`. It is safe to run multiple times -- existing resources are detected and skipped.

**What it does:**

1. Validates the default VPC exists ([ADR-0010](adr/0010-default-vpc-no-custom-networking.md))
2. Discovers the admin EFS filesystem
3. Verifies the `mint-instance-profile` IAM instance profile exists
4. Creates a per-user security group (if not present)
5. Creates a per-user EFS access point (if not present)

**Flags:** Supports `--json` for machine-readable output.

**IAM permissions note:** `mint init` calls `iam:GetInstanceProfile` to verify the admin-created instance profile exists. PowerUserAccess does not include this permission — if your credentials lack it, `mint init` returns a friendly error directing you to your administrator rather than a raw SDK chain. Ask your admin to run `mint admin setup` to create the instance profile, or verify the profile exists manually via the AWS Console.

**Examples:**

```bash
# Initialize mint
mint init

# Initialize with verbose output
mint init --verbose

# JSON output
mint init --json
```

**JSON output fields:** `vpc_id`, `efs_id`, `security_group`, `sg_created`, `access_point_id`, `ap_created`.

---

## Admin Setup

One-time account setup commands for AWS administrators. These commands require PowerUser or AdministratorAccess permissions and are run once per AWS account before any user can run `mint init`.

### `mint admin`

```
mint admin <subcommand> [flags]
```

Parent command for account-level administration. All subcommands accept `--json` for machine-readable output. See [admin-setup.md](admin-setup.md) for the full operator guide.

---

### `mint admin setup`

Run the full admin setup in one shot.

```
mint admin setup [flags]
```

Composite command that runs `mint admin deploy` then `mint admin attach-policy` in sequence. The attach-policy step is skipped gracefully if IAM Identity Center is not configured in the account.

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--stack-name` | `mint-admin-setup` | CloudFormation stack name |
| `--permission-set` | `PowerUserAccess` | IAM Identity Center permission set name |
| `--policy` | `mint-pass-instance-role` | Customer-managed policy name to attach |
| `--json` | `false` | Output result as JSON |

**Examples:**

```bash
# Full one-shot setup
mint admin setup

# With a specific AWS profile
mint admin setup --profile AdministratorAccess-123456789012

# JSON output (useful for CI)
mint admin setup --json
```

**JSON output fields:** `deploy` (see `mint admin deploy`) and `attach_policy` (see `mint admin attach-policy`, omitted if SSO is not configured).

---

### `mint admin deploy`

Deploy the Mint CloudFormation stack.

```
mint admin deploy [flags]
```

Creates or updates the `mint-admin-setup` CloudFormation stack. Auto-discovers the default VPC and subnets. Streams CloudFormation events to stderr during deployment. Idempotent — safe to re-run. If a previous creation attempt left the stack in `ROLLBACK_COMPLETE`, the stuck stack is deleted and re-created automatically.

**What the stack creates:**

- `mint-efs` — EFS filesystem (Elastic throughput) for persistent user storage
- `mint-efs` security group — NFS inbound scoped to Mint VMs
- `mint-instance-role` — IAM role allowing VMs to self-stop and update their bootstrap tag
- `mint-instance-profile` — Instance profile wrapping the role
- `mint-pass-instance-role` — IAM policy granting PowerUsers permission to pass the instance role at launch

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--stack-name` | `mint-admin-setup` | CloudFormation stack name |
| `--json` | `false` | Output result as JSON |

**Examples:**

```bash
mint admin deploy
mint admin deploy --json
```

**JSON output fields:** `StackName`, `EfsFileSystemId`, `EfsSecurityGroupId`, `InstanceProfileArn`, `PassRolePolicyArn`.

---

### `mint admin attach-policy`

Attach the PassRole policy to an IAM Identity Center permission set.

```
mint admin attach-policy [flags]
```

Finds the `mint-pass-instance-role` customer-managed policy and attaches it to the specified IAM Identity Center permission set. Triggers reprovisioning and polls until it completes. If IAM Identity Center is not configured in the account, the command prints a notice and exits successfully.

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--permission-set` | `PowerUserAccess` | IAM Identity Center permission set name |
| `--policy` | `mint-pass-instance-role` | Customer-managed policy name to attach |
| `--json` | `false` | Output result as JSON |

**Examples:**

```bash
mint admin attach-policy
mint admin attach-policy --permission-set MyCustomPermissionSet
mint admin attach-policy --json
```

**JSON output fields:** `PermissionSetArn`, `ProvisioningStatus`.

---

## Informational

Commands for viewing VM state and build info.

### `mint list`

List all VMs.

```
mint list [flags]
```

Lists all VMs belonging to the current owner with state, IP, instance type, uptime, and bootstrap status. Running VMs that have exceeded the configured idle timeout are marked with `(idle)` per [ADR-0018](adr/0018-auto-stop-idle-detection.md). A version check notice is appended if a newer version is available.

**Flags:** Supports `--json` for machine-readable output.

**Examples:**

```bash
# List all VMs
mint list

# JSON output for scripting
mint list --json
```

**Human output columns:** NAME, STATE, IP, TYPE, UPTIME, BOOTSTRAP.

**JSON output fields (per VM):** `id`, `name`, `state`, `public_ip`, `instance_type`, `launch_time`, `uptime`, `bootstrap_status`, `tags`.

**Note:** When `--json` is used, informational warnings (such as the multi-VM cost warning) are omitted; machine-readable output contains structured fields only.

---

### `mint status`

Show detailed VM status.

```
mint status [flags]
```

Shows detailed status of a single VM including state, IP, instance type, volume sizes, disk usage percentage, launch time, bootstrap status, and all tags. Disk usage is fetched live via SSH when the VM is running.

**Flags:** Supports `--json` for machine-readable output.

**Examples:**

```bash
# Show default VM status
mint status

# Show status of a named VM
mint status --vm staging

# JSON output
mint status --json
```

**JSON output fields:** `id`, `name`, `state`, `public_ip`, `instance_type`, `root_volume_gb`, `project_volume_gb`, `disk_usage_pct`, `launch_time`, `bootstrap_status`, `tags`, `mint_version`.

---

### `mint version`

Print the version of mint.

```
mint version [flags]
```

Prints the version, commit hash, and build date of the current mint binary. This command does not require AWS credentials.

**Flags:** Global flags only.

**Examples:**

```bash
mint version
```

**Output format:**

```
mint version: 1.0.0
commit: abc1234
date: 2025-01-15
```

---

## Quick Reference

| Command | Purpose |
|---------|---------|
| `mint admin setup` | One-time account setup (admin) |
| `mint admin deploy` | Deploy admin CloudFormation stack |
| `mint admin attach-policy` | Attach PassRole policy to SSO |
| `mint init` | One-time setup for new users |
| `mint up` | Create or start a VM |
| `mint down` | Stop a VM (preserves resources) |
| `mint destroy` | Permanently delete a VM |
| `mint resize` | Change instance type |
| `mint recreate` | Fresh VM, same config |
| `mint ssh` | SSH with ephemeral keys |
| `mint mosh` | Roaming SSH for iPads |
| `mint connect` | Mosh + tmux session picker |
| `mint sessions` | List tmux sessions |
| `mint code` | Open VS Code Remote-SSH |
| `mint ssh-config` | Manage SSH config entries |
| `mint key add` | Permanent SSH key escape hatch |
| `mint project add` | Clone + devcontainer + tmux |
| `mint project list` | Show projects and containers |
| `mint project rebuild` | Rebuild a devcontainer |
| `mint doctor` | Health checks and diagnostics |
| `mint update` | Self-update to latest version |
| `mint extend` | Extend idle auto-stop timer |
| `mint config` | Show configuration |
| `mint config set` | Set a config value |
| `mint config get` | Get a config value |
| `mint list` | List all VMs |
| `mint status` | Detailed single-VM status |
| `mint version` | Print build info |

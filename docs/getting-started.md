# Getting Started with Mint

Mint provisions and manages EC2-based development environments for running Claude Code. You work on a remote VM through VS Code or a terminal -- Mint handles the infrastructure so you can focus on code.

This guide walks you through installation, first-time setup, launching your first VM, connecting, and daily usage.

## Prerequisites

Before you begin, make sure you have:

- **AWS account with PowerUser permissions** -- Mint creates EC2 instances, EBS volumes, Elastic IPs, security groups, and EFS access points. You need PowerUser-level access (including `ec2-instance-connect:SendSSHPublicKey`).
- **AWS CLI v2 installed and configured** -- Mint uses your existing AWS credentials (profiles, SSO, or environment variables). Run `aws sts get-caller-identity` to verify your credentials work.
- **Admin setup complete** -- An AWS administrator must deploy the Mint CloudFormation stack once per account. This creates the IAM instance profile, EFS filesystem, and supporting security group that all Mint users share. See [admin-setup.md](admin-setup.md) for the full procedure. If you are unsure whether this has been done, ask your team lead.
- **VS Code with Remote-SSH extension** (for the primary workflow) -- Install the [Remote - SSH](https://marketplace.visualstudio.com/items?itemName=ms-vscode-remote.ms-vscode-remote-extensionpack) and [Dev Containers](https://marketplace.visualstudio.com/items?itemName=ms-vscode-remote.remote-containers) extensions.

## Install Mint

### macOS (Homebrew)

```bash
brew install SpiceLabsHQ/mint/mint
```

### Linux (install script)

```bash
curl -fsSL https://raw.githubusercontent.com/SpiceLabsHQ/mint/main/install.sh | sh
```

The install script downloads the latest release, verifies the checksum, and places the binary in your PATH.

### Verify installation

```bash
mint version
```

You should see version and build information printed to the terminal.

## Initialize Your Environment

Run `mint init` once from your machine. This validates that the admin setup is in place and creates your per-user resources.

```bash
mint init
```

Mint performs these checks and actions:

1. Validates your AWS credentials and derives your owner identity from `aws sts get-caller-identity`
2. Confirms the default VPC exists with a public subnet in your configured region
3. Confirms the admin-created EFS filesystem and instance profile exist
4. Creates a security group for your VMs (SSH on port 41122, mosh on UDP 60000-61000)
5. Creates a per-user EFS access point for persistent configuration (dotfiles, SSH keys, Claude Code auth state)

Expected output:

```
VPC           vpc-0abc1234def56789
EFS           fs-0abc1234def56789
Security group sg-0abc1234def56789 (created)
Access point  fsap-0abc1234def56789 (created)

Initialization complete.
```

If you see an error about a missing instance profile or EFS filesystem, the admin setup has not been completed. Direct your AWS administrator to [admin-setup.md](admin-setup.md).

`mint init` is safe to run multiple times -- it detects existing resources and skips creation.

## Configure Mint

Mint stores preferences in `~/.config/mint/config.toml`. Set your preferred region and allow Mint to manage your SSH config:

```bash
mint config set region us-east-1
mint config set ssh_config_approved true
```

The `ssh_config_approved` setting lets Mint write an SSH config entry for your VM, which is required for VS Code Remote-SSH to connect.

Optional configuration:

```bash
mint config set instance_type m6i.xlarge      # Default: m6i.xlarge (4 vCPU, 16GB)
mint config set volume_size_gb 100            # Default: 50GB project volume
mint config set idle_timeout_minutes 90       # Default: 60 minutes
```

View your current configuration:

```bash
mint config
```

## Launch Your First VM

Start a VM with `mint up`:

```bash
mint up
```

Mint provisions a full development environment:

1. **Launches an EC2 instance** -- Ubuntu 24.04 LTS with your configured instance type
2. **Creates a project EBS volume** -- Mounted at `/mint/projects` for your source code
3. **Allocates an Elastic IP** -- Stable address that persists across stop/start cycles
4. **Runs the bootstrap script** -- Installs Docker, Docker Compose, devcontainer CLI, tmux, mosh, Git, GitHub CLI, Node.js LTS, AWS CLI v2, and EC2 Instance Connect
5. **Polls for completion** -- Waits up to 7 minutes for the bootstrap to finish

Expected output:

```
Instance      i-0abc1234def56789
IP            203.0.113.42
Volume        vol-0abc1234def56789
EIP           eipalloc-0abc1234def56789

Bootstrap complete. VM is ready.
```

Use `--verbose` to see detailed progress steps during provisioning:

```bash
mint up --verbose
```

If the bootstrap times out, Mint prompts you to choose: stop the instance (halt billing), terminate it (clean up), or leave it running for debugging.

## Connect with VS Code (Primary Workflow)

This is the everyday workflow for working at your desk.

### Open VS Code

```bash
mint code
```

This opens VS Code connected to your VM via Remote-SSH. Behind the scenes, Mint ensures your `~/.ssh/config` has the correct entry and runs `code --remote ssh-remote+mint-default /home/ubuntu`.

### Open a devcontainer

Once VS Code is connected to the VM:

1. Open a project folder that contains a `.devcontainer/` directory
2. VS Code detects the devcontainer configuration and shows a notification: "Reopen in Container"
3. Click it, or use the Command Palette: **Dev Containers: Reopen in Container**
4. Claude Code runs in the integrated terminal inside the container

### Add a project

Clone a repository and set up its devcontainer on the VM:

```bash
mint project add https://github.com/your-org/your-repo.git
```

This clones the repo to `/mint/projects/your-repo`, builds the devcontainer, and creates a named tmux session. You can then open it with `mint code --path /mint/projects/your-repo`.

List projects on the VM:

```bash
mint project list
```

## Connect from iPad (Secondary Workflow)

For checking on Claude Code sessions from a mobile device, Mint supports mosh connections with tmux for session persistence.

### Setup

1. Install [Termius](https://termius.com/) on your iPad (or any SSH/mosh client)
2. Add your SSH public key to the VM:

```bash
mint key add ~/.ssh/id_ed25519.pub
```

This is needed because Termius cannot use EC2 Instance Connect directly. The key is added to the VM's `authorized_keys` file.

3. In Termius, configure a new host:
   - **Hostname**: Your VM's Elastic IP (find it with `mint status`)
   - **Port**: 41122
   - **Username**: ubuntu
   - **Key**: Select the matching private key
   - **Enable mosh**: Yes, with port range 60000-61000

### Connect and use tmux

From Termius, connect via mosh. Once on the VM:

```bash
tmux attach
```

Or from your Mac, use Mint's connect command to mosh in and attach to a tmux session in one step:

```bash
mint connect
```

If multiple tmux sessions exist, Mint presents a picker. To connect directly to a named session:

```bash
mint connect my-project
```

tmux runs on the VM host, not inside containers. When your iPad disconnects (iOS suspends the app, network drops), tmux keeps the session alive. Reconnecting is a single `tmux attach`.

To manually access a container from a tmux session:

```bash
docker exec -it <container-name> /bin/bash
```

## Daily Commands

| Command | What it does |
|---------|-------------|
| `mint up` | Start your VM (or create it if it does not exist) |
| `mint down` | Stop the VM -- billing stops, all data persists |
| `mint code` | Open VS Code connected to the VM |
| `mint ssh` | Open an SSH session to the VM |
| `mint mosh` | Open a mosh session to the VM |
| `mint connect [session]` | Mosh in and attach to a tmux session |
| `mint status` | Detailed VM status: state, IP, instance type, disk usage |
| `mint list` | Show all your VMs with state and uptime |
| `mint project add <url>` | Clone a repo and build its devcontainer |
| `mint project list` | List projects on the VM |
| `mint extend [minutes]` | Reset the idle auto-stop timer |
| `mint doctor` | Check environment and VM health |
| `mint destroy` | Permanently delete the VM and its volumes |

### Global flags

Every command supports these flags:

| Flag | Purpose |
|------|---------|
| `--vm <name>` | Target a specific VM (default: `default`) |
| `--verbose` | Show progress steps |
| `--debug` | Show AWS SDK call details |
| `--json` | Machine-readable JSON output (on list/status commands) |
| `--yes` | Skip confirmation on destructive operations |

## Understanding Storage

Mint uses three tiers of storage:

| Tier | Mount point | Persists across | Destroyed by |
|------|-------------|-----------------|--------------|
| **Root EBS** (200GB) | `/` | Nothing -- ephemeral | `mint destroy`, `mint recreate` |
| **Project EBS** (50GB default) | `/mint/projects` | `mint down`, `mint resize`, `mint recreate` | `mint destroy` |
| **User EFS** | `/mint/user` | Everything, including `mint destroy` | Admin stack deletion only |

- **Root EBS** holds the OS and Docker layers. It is recreated fresh on `mint recreate`.
- **Project EBS** holds your source code and project data. It survives VM restarts and instance recreation, but is destroyed when you run `mint destroy`.
- **User EFS** holds persistent configuration: dotfiles, `~/.ssh/authorized_keys`, Claude Code authentication state. It is mounted from a shared EFS filesystem and survives all lifecycle operations. It is shared across all your VMs.

## Auto-Stop and Idle Detection

Mint automatically stops your VM after a period of inactivity to prevent unnecessary billing. The default idle timeout is 60 minutes.

A VM is considered active if any of the following are true:

- An SSH or mosh session is connected
- A tmux session has an attached client
- A `claude` process is running inside any container
- The idle timer was manually extended

When none of these are true for the configured timeout, the VM stops itself.

To extend the timer when you know a long-running task is in progress:

```bash
mint extend           # Reset to default timeout
mint extend 120       # Extend by 120 minutes
```

`mint list` warns you when a VM has exceeded its idle timeout, which can indicate an auto-stop failure.

## Troubleshooting

### Run mint doctor

The first step for any issue:

```bash
mint doctor
```

This checks:

- AWS credentials are valid
- Region and configuration values are correct
- SSH config has the managed block for your VM
- Elastic IP quota has headroom
- If a VM is running: health tag status, disk usage, and installed component versions (Docker, devcontainer CLI, tmux, mosh)

To attempt automated repair of failed components:

```bash
mint doctor --fix
```

### Common issues

**"no admin EFS found"** -- The admin CloudFormation stack has not been deployed. Ask your AWS administrator to follow [admin-setup.md](admin-setup.md).

**"VM is not running"** -- Run `mint up` to start your VM. If it was previously created, `mint up` restarts it without reprovisioning.

**"mint needs to update ~/.ssh/config"** -- Run `mint config set ssh_config_approved true` to allow Mint to manage your SSH config entries.

**Bootstrap timeout** -- If `mint up` reports that bootstrap did not complete within 7 minutes, you can SSH in to debug (`mint ssh`) or terminate and retry (`mint destroy && mint up`).

**HOST KEY CHANGED** -- This warning appears after `mint recreate` or if a VM was destroyed and recreated. If expected, follow the instructions in the error message. If unexpected, investigate before proceeding.

**Disk space warnings** -- `mint status` and `mint doctor` report disk usage. At 80% usage you get a warning; at 90% it reports a failure. Consider cleaning up Docker images (`docker system prune` on the VM) or increasing your volume size with `mint config set volume_size_gb <size>` followed by `mint recreate`.

**"mosh is not installed"** -- Install mosh locally before using `mint mosh` or `mint connect`:
  - macOS: `brew install mosh`
  - Linux: `apt install mosh` or `dnf install mosh`

### Logs and audit trail

Mint writes structured logs to `~/.config/mint/logs/` and an audit log to `~/.config/mint/audit.log`. Use `--debug` on any command to see AWS SDK call details in real time.

### Getting help

- Run `mint <command> --help` for usage information on any command
- Check `docs/SPEC.md` for the full specification
- File issues at the project repository

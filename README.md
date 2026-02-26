# Mint

Mint provisions and manages EC2-based development environments for running Claude Code. It handles the host VM — provisioning, connectivity, and lifecycle — so you can focus on the work inside.

```
MacBook → VS Code Remote-SSH → EC2 host → devcontainer
iPad    → mosh → EC2 host → tmux → devcontainer
```

One VM hosts multiple projects. Connect via VS Code Remote-SSH for your primary workflow, or drop in from an iPad over mosh to check on a Claude Code session from anywhere.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/SpiceLabsHQ/Mint/main/install.sh | sh
```

Installs the `mint` binary to `/usr/local/bin`. After the initial install, upgrade with:

```bash
mint update
```

## Requirements

- AWS account with PowerUser permissions
- An admin must run `mint admin setup` once per account to create shared infrastructure (EFS, IAM role)
- Run `mint init` once per user to create your user-scoped AWS resources

## Quick start

```bash
# First-time setup (once per user)
mint init

# Provision a VM and connect
mint up
mint ssh

# Open in VS Code
mint code

# Check what's running
mint list
mint sessions

# Shut down when done
mint destroy
```

## Key commands

| Command | Description |
|---------|-------------|
| `mint up` | Provision a new VM or start a stopped one |
| `mint ssh` | SSH into the VM |
| `mint code` | Open the VM in VS Code Remote-SSH |
| `mint list` | List your VMs and their status |
| `mint sessions` | Show active tmux/SSH/mosh sessions |
| `mint extend` | Extend idle timeout to keep the VM alive |
| `mint recreate` | Rebuild the VM, preserving project volumes |
| `mint destroy` | Terminate the VM and release resources |
| `mint projects` | Manage project EBS volumes |
| `mint update` | Upgrade mint to the latest release |
| `mint doctor` | Diagnose configuration and connectivity issues |

Run `mint --help` or `mint <command> --help` for full flag documentation.

## Configuration

Config lives at `~/.config/mint/config.toml`. Common settings:

```toml
region        = "us-east-1"
instance_type = "m7g.xlarge"
volume_size   = 50          # GB, per-project EBS
idle_timeout  = 120         # minutes before auto-stop warning
```

## Multiple VMs

Most users need only one VM. For advanced cases, `--vm <name>` targets a named VM:

```bash
mint up --vm gpu-box
mint ssh --vm gpu-box
```

# ADR-0002: Single VM Hosts Multiple Projects

## Status
Accepted

## Context
Developers using Mint typically work on multiple projects in a single day. Two models were considered:

1. **VM-per-project**: Each project gets its own EC2 instance. Clean isolation, but expensive (each m6i.xlarge is ~$140/month running 24/7) and operationally complex (multiple IPs, multiple SSH configs, multiple idle timers).
2. **Single VM, multiple containers**: One EC2 instance runs multiple devcontainers, one per project. Projects share compute but are isolated at the container level.

## Decision
Each Mint VM hosts multiple projects. Each project gets its own devcontainer on the shared EC2 instance. tmux sessions on the host provide per-project terminal access via `docker exec` into the appropriate container.

The default VM is named `default` and most users never need another. Power users who need workload isolation (e.g., a GPU instance for ML work) can create additional named VMs with `mint up --vm <name>`.

## Consequences
- **Cost efficient.** One m6i.xlarge instance handles multiple projects. Developers do not pay for idle VMs per project.
- **Simpler management.** One IP, one SSH config, one idle timer covers all projects.
- **Container isolation, not VM isolation.** A misbehaving container (disk fill, memory leak) can affect other projects on the same host. Acceptable for dev environments.
- **Shared resources.** CPU, memory, and disk are shared across projects. Heavy builds in one container can slow others.
- **Multiple VMs remain available.** The `--vm` flag on every command provides an escape hatch for workload isolation when needed.

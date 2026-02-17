# ADR-0002: Single VM Hosts Multiple Projects

## Status
Accepted

## Context
Developers using Mint typically work on multiple projects in a single day. Two models were considered:

1. **VM-per-project**: Each project gets its own EC2 instance. Clean isolation, but expensive (each m6i.xlarge is ~$140/month running 24/7) and operationally complex (multiple IPs, multiple SSH configs, multiple idle timers).
2. **Single VM, multiple projects**: One EC2 instance hosts multiple projects on a shared Project EBS volume. Users may run devcontainers per project, but container management is outside Mint's scope.

## Decision
Each Mint VM hosts multiple projects on a shared Project EBS volume (mounted at `/mint/projects`). Mint provisions the host VM and connects users via VS Code Remote-SSH, mosh, or SSH. From there, VS Code's Dev Containers extension and the user's own tooling manage any containers. tmux sessions on the host provide persistent terminal access that survives container rebuilds and disconnections (see ADR-0003).

The default VM is named `default` and most users never need another. Power users who need workload isolation (e.g., a GPU instance for ML work) can create additional named VMs with `mint up --vm <name>`.

## Consequences
- **Cost efficient.** One m6i.xlarge instance handles multiple projects. Developers do not pay for idle VMs per project.
- **Simpler management.** One IP, one SSH config, one idle timer covers all projects.
- **No host-level project isolation.** Projects share a single Project EBS volume and compute resources. A runaway process in one project can affect others. Acceptable for dev environments.
- **Shared resources.** CPU, memory, and disk are shared across projects. Heavy builds in one project can slow others.
- **Multiple VMs remain available.** The `--vm` flag on every command provides an escape hatch for workload isolation when needed.

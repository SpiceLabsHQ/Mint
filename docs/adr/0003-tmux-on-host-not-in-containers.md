# ADR-0003: tmux on Host, Not in Containers

## Status
Accepted

## Context
Mint supports two connection workflows:

1. **Primary**: MacBook with VS Code Remote-SSH directly into devcontainers.
2. **Secondary**: iPad with Termius connecting via mosh, then attaching to tmux sessions to interact with Claude Code running in containers.

The iPad/Termius workflow has a specific constraint: iOS aggressively suspends background apps, dropping the network connection. When the developer reopens Termius, the session must still be there. Mosh handles the transport-level reconnection, but something must keep the terminal session alive between reconnections.

If tmux runs inside a devcontainer, rebuilding the container (a routine devcontainer operation) kills the tmux session and everything running in it, including Claude Code. The developer loses work.

## Decision
tmux runs on the EC2 host, not inside devcontainers. Each project gets a tmux session on the host that runs `docker exec` to attach to the project's devcontainer. The session lifecycle is:

```
iPad (Termius) -> mosh -> EC2 host -> tmux session -> docker exec -> devcontainer
```

tmux is configured with mouse support, 50k line scroll buffer, and 256-color terminal.

## Consequences
- **Sessions survive container rebuilds.** Rebuilding a devcontainer only disrupts the `docker exec` inside the tmux session, not the tmux session itself. The developer can re-enter the rebuilt container without losing their tmux window layout.
- **Sessions survive iOS app suspension.** mosh + host-level tmux means reconnecting is a single `tmux attach`, regardless of how long Termius was suspended.
- **Host-level dependency.** tmux must be installed and configured on the host during bootstrap, not managed by devcontainer configuration. This is part of Mint's user-data script.
- **Container isolation is thinner.** Host-level tmux sessions run as the host user, not inside the container's filesystem namespace. The `docker exec` is the boundary.

# ADR-0018: Auto-Stop Idle Detection

## Status
Accepted

## Context
Mint VMs auto-stop when idle to prevent runaway compute costs. The idle detection criteria and mechanism were specified in SPEC.md but lacked formal design scrutiny. Cross-container process detection is non-trivial, and auto-stop false negatives (VM never stops) are the highest-cost failure mode in v1 — there is no external backstop until the dead-man's switch Lambda ships in v2 (ADR-0011).

The squadron review identified that idle detection deserves its own ADR because:
- The detection method for `claude` processes across containers requires explicit design
- False-positive/negative trade-offs affect cost and developer experience
- The structured log schema enables `mint status` to explain *why* a VM stopped or remains running
- `mint list` must surface auto-stop failures as the primary v1 cost safety net

## Decision

### Detection Method

A systemd timer fires every 5 minutes and runs the idle detection service. The service checks four activity criteria:

1. **SSH/mosh sessions**: Check for active `sshd` child processes and `mosh-server` processes with connected clients.
2. **tmux attached clients**: Run `tmux list-clients` to detect attached sessions. Detached tmux sessions (no client) are not considered active — a developer who disconnected is idle.
3. **Claude process in containers**: Run `docker top` against all running containers and scan for processes matching `claude`. This catches Claude Code running inside any devcontainer without requiring per-container configuration.
4. **Manual extend**: Check the extend timestamp file (`/var/lib/mint/idle-extended-until`). If the current time is before the extended timestamp, the VM is considered active.

If none of these criteria are met for the configured timeout (default 60 minutes), the VM stops itself using the IAM permissions from the instance role (ADR-0009).

### False-Positive/Negative Trade-Offs

**False positive (VM stops too early)**: A developer has an active Claude Code session but no SSH/mosh connection and no tmux client attached. This should not happen in normal usage — Claude Code requires a terminal — but could occur if the terminal emulator crashes while Claude continues working. Mitigation: the `claude` process check catches this case independently.

**False negative (VM never stops)**: A `claude` process is running but idle (waiting for user input indefinitely). The VM will never auto-stop because the process exists. This is the accepted trade-off — a running Claude process is considered "active" regardless of whether it's actually doing work. `mint list` auto-warns on VMs exceeding their idle timeout as the v1 cost safety net.

### Structured Log Schema

The idle detection service writes JSON to journald on every check:

```json
{
  "check_timestamp": "2024-01-15T10:30:00Z",
  "active_criteria_met": ["claude_process"],
  "idle_elapsed_minutes": 0,
  "action_taken": "none",
  "stop_result": null
}
```

When a stop is triggered:

```json
{
  "check_timestamp": "2024-01-15T11:30:00Z",
  "active_criteria_met": [],
  "idle_elapsed_minutes": 65,
  "action_taken": "stop",
  "stop_result": "success"
}
```

### `mint list` Auto-Warning

`mint list` compares each running VM's uptime against its configured idle timeout. VMs running longer than their timeout without recent activity (queryable via the last idle-check log or instance uptime heuristic) are flagged with a warning. This is the primary v1 mechanism for detecting auto-stop failures.

## Consequences
- **Explicit detection design.** Each activity criterion is specified with its detection method, enabling direct implementation without ambiguity.
- **Cross-container visibility.** `docker top` scanning works across all containers without per-container configuration, at the cost of being process-name-dependent (if Claude Code changes its process name, detection breaks).
- **Accepted false-negative.** An idle `claude` process keeps the VM running. This is correct behavior for the common case (Claude is working) and a minor cost leak in the edge case (Claude is waiting). `mint list` warnings surface the leak.
- **Cost safety net.** `mint list` auto-warnings provide the v1 alternative to the v2 dead-man's switch Lambda.
- **Structured observability.** JSON logs to journald enable `mint status` to report idle state and `mint doctor` to diagnose auto-stop failures.

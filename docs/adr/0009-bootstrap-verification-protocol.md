# ADR-0009: Bootstrap Verification Protocol

## Status
Accepted

## Context
Mint VMs bootstrap themselves via EC2 user-data on first boot. The user-data script installs Docker, devcontainer CLI, mosh, tmux, Git, GitHub CLI, Node.js, and AWS CLI. This script runs once at instance creation and has two problems:

1. **No observable outcome.** The user-data script runs asynchronously after instance launch. `mint up` had no way to know whether bootstrap succeeded, partially completed, or failed. The instance shows as "running" regardless.
2. **Not idempotent.** If the instance is stopped and started, the user-data does not re-run. If a package was removed or corrupted, there is no reconciliation.

Developers running `mint up` need confidence that the VM is fully ready before they connect.

## Decision
Implement a bootstrap verification protocol with two components:

**Post-boot health check**: A verification script runs at the end of user-data. It validates that all expected components are installed and functional (Docker daemon running, devcontainer CLI in PATH, mosh-server available, tmux installed, etc.). On success, it tags the instance with `mint:bootstrap=complete`.

**`mint up` polling**: After starting the instance, `mint up` polls for the `mint:bootstrap=complete` tag before reporting success to the user. If the tag does not appear within a timeout, `mint up` reports a bootstrap failure and directs the user to check cloud-init logs.

**Restart reconciliation**: A boot-time script (systemd unit) runs on every start (not just first boot). It compares installed component versions against expected versions and logs discrepancies. This catches drift from manual modifications or package updates.

## Consequences
- **Reliable "ready" signal.** `mint up` only reports success when the VM is verified functional. Developers do not connect to half-bootstrapped instances.
- **Diagnosable failures.** Bootstrap failures are surfaced to the user with actionable guidance instead of manifesting as mysterious connection or tooling errors.
- **Drift detection.** The restart reconciliation script catches configuration drift, though it logs warnings rather than auto-remediating (to avoid surprises).
- **Added boot time.** The health check adds seconds to the first-boot sequence. Acceptable given it runs once.
- **Tag dependency.** The verification protocol depends on the instance being able to tag itself, requiring the IAM instance role to include `ec2:CreateTags` permission on its own resource.

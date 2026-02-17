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

### Bootstrap timeout

The bootstrap polling timeout is **7 minutes**.

5 minutes is too tight — Docker installation variance in congested availability zones can push past that threshold under normal conditions. 10 minutes is too generous and leaves users waiting with no feedback on genuine failures. 7 minutes is the practical midpoint that accommodates real-world installation variance while keeping feedback loops short.

### Bootstrap failure behavior

When `mint up` reaches the 7-minute timeout without observing `mint:bootstrap=complete`, Mint does not silently terminate the instance. Instead, it prompts the user to choose one of three options:

1. **Stop the instance** — Halts billing for compute while preserving the instance for later debugging. The user can inspect cloud-init logs via `mint ssh` after manually starting the instance.
2. **Terminate the instance** — Destroys the instance and cleans up resources. Before terminating, Mint tags the instance with `mint:bootstrap=failed` so that the failure is visible in the AWS console and in `mint ls` output until termination completes.
3. **Leave running** — Takes no action, allowing the user to connect immediately via SSH and debug the in-progress or failed bootstrap directly.

This behavior aligns with the Transparency value: surface the problem, show the state, let the developer decide. Silent termination of a paying user's instance is not acceptable.

### Bootstrap script hash pinning

The Go binary embeds the expected SHA256 hash of the user-data script at compile time. Before sending user-data to EC2 on instance launch, Mint verifies the embedded script matches its expected hash.

This closes a supply-chain attack surface: a compromised CDN, a tampered repository, or a build artifact substitution could otherwise deliver a malicious bootstrap script to new instances. Hash pinning provides strong integrity guarantees without requiring full signing infrastructure or a key management system.

If the hash does not match, `mint up` aborts immediately with an error directing the user to update their Mint binary. The script is never sent to EC2.

### Reconciliation strategy

The restart reconciliation systemd unit detects drift (component version mismatches, missing packages, corrupted state) and logs warnings to journald. It does **not** auto-remediate.

The explicit repair path is `mint doctor --fix`, which the user runs intentionally. Unattended `apt-get` on every boot is a security anti-pattern: the xz-utils backdoor (CVE-2024-3094) demonstrated that automated package operations during startup are a high-value attack vector. Detection-only with user-initiated repair preserves auditability — the user controls when the system changes, and the change is visible in shell history.

### Bootstrap script versioning

The user-data script writes its own version string to `/var/lib/mint/bootstrap-version` upon successful completion. The restart reconciliation unit reads this file on each boot and compares it against the version embedded in the running Mint binary. Version mismatches are logged to journald as warnings and surfaced by `mint doctor`.

This enables drift detection across Mint upgrades without requiring the reconciliation unit to independently re-validate every installed package.

## Consequences
- **Reliable "ready" signal.** `mint up` only reports success when the VM is verified functional. Developers do not connect to half-bootstrapped instances.
- **Diagnosable failures.** Bootstrap failures are surfaced to the user with actionable guidance instead of manifesting as mysterious connection or tooling errors.
- **User-controlled failure handling.** On timeout, the user chooses how to proceed — stop, terminate, or debug. No silent resource destruction.
- **Supply-chain integrity.** Hash pinning prevents tampered bootstrap scripts from reaching EC2, at the cost of requiring a binary update when the script changes.
- **Drift detection.** The restart reconciliation unit catches configuration drift but does not auto-fix. Repair requires explicit `mint doctor --fix`, preserving auditability.
- **Added boot time.** The health check adds seconds to the first-boot sequence. Acceptable given it runs once.
- **Tag dependency.** The verification protocol depends on the instance being able to tag itself, requiring the IAM instance role to include `ec2:CreateTags` permission on its own resource.

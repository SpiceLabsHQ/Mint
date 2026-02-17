# ADR-0019: SSH Host Key Trust-on-First-Use

## Status
Accepted

## Context
Mint VMs generate new SSH host keys on first boot and after `mint recreate` (which launches a fresh instance). Standard SSH behavior prompts for host key verification on every new connection, and `mint recreate` triggers a host key mismatch warning that most developers dismiss without thought — weakening the security signal.

Mint needs a host key trust model that:
- Avoids the "just type yes" fatigue that trains developers to ignore security prompts
- Detects legitimate host key changes (after `mint recreate`) vs. potential MITM attacks
- Works within Mint's `~/.config/mint/` directory rather than modifying `~/.ssh/known_hosts`

## Decision

Adopt Trust-on-First-Use (TOFU) with explicit change detection.

### First Connection

On first SSH/mosh connection to a VM, Mint records the host key in `~/.config/mint/known_hosts` keyed by VM name (not IP address, since IPs are stable via Elastic IP but the VM name is the user's mental model). No prompt — the first key is trusted automatically.

### Subsequent Connections

On reconnect, Mint validates the host key against the stored key. If the key matches, the connection proceeds silently.

### Host Key Change

When a new host key appears for a VM name that already has a stored key (e.g. after `mint recreate`), Mint:

1. Blocks the connection
2. Displays a prominent warning: the VM name, the old and new key fingerprints, and the likely cause ("Did you recently run `mint recreate`?")
3. Prompts the user to accept or reject the new key
4. If accepted, updates `~/.config/mint/known_hosts`

The warning must be visually distinct from normal output — this is the one security prompt that matters and it must not blend into routine CLI noise.

### Accepted Risk

TOFU does not protect against a MITM on the very first connection. This is accepted because:
- EC2 Instance Connect provides the initial SSH channel, which is authenticated via AWS IAM
- The first connection is typically made seconds after `mint up` reports success
- The attack window is narrow and requires compromising the network path between the user and AWS

## Consequences
- **No key management burden.** Developers never generate, distribute, or rotate host keys. TOFU handles it automatically.
- **Meaningful security prompts.** By suppressing the routine first-connection prompt and making the change-detection prompt loud, Mint trains developers to pay attention when it matters.
- **`mint recreate` awareness.** The change prompt includes context about likely causes, reducing the chance of blind acceptance.
- **TOFU limitation.** A compromised first connection is undetectable. The combination of EC2 Instance Connect and short attack windows makes this an acceptable trade-off for a trusted-team tool.
- **File location.** Using `~/.config/mint/known_hosts` instead of `~/.ssh/known_hosts` keeps Mint's state self-contained and avoids conflicts with the user's SSH configuration.

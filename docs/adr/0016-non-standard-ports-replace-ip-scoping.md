# ADR-0016: Non-Standard Ports Replace IP Scoping

## Status
Accepted (supersedes ADR-0006)

## Context
ADR-0006 scoped security group rules to the user's current public IP. This provided defense-in-depth against SSH scanning and brute-force attacks, but introduced a fundamental problem: it breaks the mobile workflow.

Mint's secondary workflow is iPad with Termius connecting via mosh. When a user is mobile, they're on a different IP than when they ran `mint init` or `mint up`. They don't have access to the Mint CLI on their iPad, so they can't update the security group. They're locked out of their own VM.

Even on the primary MacBook workflow, IP changes from switching networks (home, office, coffee shop) cause silent connection failures that are difficult to diagnose.

Meanwhile, the actual security provided by IP scoping is marginal given Mint's auth model:
- SSH uses EC2 Instance Connect with ephemeral keys or `authorized_keys` — no passwords, no brute-force vector.
- mosh authenticates over SSH first, then uses a shared secret for the UDP channel.
- The real threat IP scoping defends against is SSH zero-day exploits — a low-probability event that doesn't justify breaking a primary workflow.

## Decision
Replace IP-scoped security groups with non-standard ports and auth-layer security.

- **SSH listens on a non-standard high port** (configured during bootstrap, default 41122). This avoids the vast majority of automated scanning, which targets port 22 and a handful of known alternatives (2222, 22222, etc.).
- **mosh uses its standard port range** (UDP 60000-61000), which is already non-standard and not a common scan target.
- **Security group allows inbound from `0.0.0.0/0`** on the SSH and mosh ports. Network-level IP restriction is removed entirely.
- **Password authentication is disabled** in sshd. Only key-based auth (EC2 Instance Connect ephemeral keys or `authorized_keys`) is accepted.

The SSH port is baked into the VM's sshd config during bootstrap, written into the `mint ssh-config` ProxyCommand, and used by all `mint ssh`/`mint mosh`/`mint code` commands transparently. Users never need to know or type the port number.

## Consequences
- **Mobile workflow works.** Connect from any IP, any device, any network. No security group updates needed.
- **No IP change friction.** Switching networks never causes connection failures.
- **Reduced scanning exposure.** Non-standard SSH port eliminates >99% of automated scanning. Combined with key-only auth, the remaining attack surface is negligible.
- **Simpler `mint init`.** No need to detect the user's public IP or maintain IP-update commands.
- **Port 22 is closed.** If something does find the VM, the standard SSH port doesn't respond.
- **Not a VPN replacement.** Traffic is still SSH/mosh encrypted but traverses the public internet. This is the same security model as Codespaces, Gitpod, and every other cloud dev environment.
- **Zero-day risk accepted.** A zero-day in SSH on a non-standard port with key-only auth is an extremely low-probability event. The trusted-team model (ADR-0005) already accepts that Mint is not suitable for hostile multi-tenant environments.

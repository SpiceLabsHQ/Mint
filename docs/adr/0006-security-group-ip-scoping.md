# ADR-0006: Security Group IP Scoping

## Status
Superseded by [ADR-0016](0016-non-standard-ports-replace-ip-scoping.md)

## Context
Mint VMs expose SSH (TCP 22) and mosh (UDP 60000-61000) to inbound traffic. The original spec did not specify the source IP range for the security group. The simplest approach is `0.0.0.0/0` (open to the internet), but this exposes the VM to SSH scanning, brute-force attempts, and exploitation of any SSH/mosh vulnerabilities.

Mint VMs use EC2 Instance Connect for SSH authentication (ephemeral keys, no persistent credentials — see ADR-0007), which mitigates brute-force risk but does not eliminate the attack surface from port scanning and zero-day exploits.

## Decision
`mint init` creates the security group scoped to the user's current public IP (`/32` CIDR). This is detected automatically at init time (e.g., via an HTTP check to a public IP echo service or AWS's `checkip.amazonaws.com`).

Users on dynamic IPs whose address changes (ISP reassignment, switching networks) re-run a command to update the security group with their new IP.

## Consequences
- **No drive-by attacks.** SSH and mosh ports are invisible to arbitrary internet scanners. Only traffic from the user's IP reaches the VM.
- **Dynamic IP friction.** Users on dynamic IPs must update the security group when their IP changes. This is a manual step but infrequent for most residential and office connections.
- **Multiple location support.** Users connecting from multiple IPs (home, office, coffee shop) need to update the rule or add multiple CIDR entries.
- **Not a VPN replacement.** IP scoping reduces attack surface but does not encrypt traffic beyond what SSH/mosh already provide. It is a defense-in-depth measure.

# ADR-0004: Single 200GB gp3 Root Volume

## Status
Accepted

## Context
Docker stores images, layers, and container filesystems under `/var/lib/docker`. On a busy dev machine with multiple devcontainers, this can consume 50-100GB. Two storage strategies were evaluated:

1. **Separate EBS volume** mounted at `/var/lib/docker`. Isolates Docker storage from the OS. If Docker fills the disk, the root filesystem is unaffected. Adds provisioning complexity (create volume, attach, format, mount, update fstab).
2. **Single root volume.** Everything on one gp3 disk. Simpler provisioning. If Docker fills the disk, the OS is also affected.

The original spec called for a 100GB root volume.

## Decision
Use a single 200GB gp3 EBS root volume. No dedicated Docker volume.

The volume size was increased from the original spec's 100GB to 200GB to provide adequate headroom for Docker images, build layers, multiple project repositories, and OS overhead.

`mint status` will report disk usage so developers can see when they are running low. A future v2 release may introduce a separate Docker volume if disk contention proves problematic in practice.

## Consequences
- **Simpler provisioning.** One volume to create, no mount/format/fstab logic in the bootstrap script.
- **Simpler teardown.** `mint destroy` deletes the instance and its root volume. No orphaned EBS volumes to track.
- **Blast radius.** A Docker storage runaway (large images, build cache accumulation) can fill the root filesystem and destabilize the OS. Mitigated by the 200GB size and `mint status` disk reporting.
- **No resize friction.** Users can adjust volume size in config before `mint up` without coordinating two volumes.
- **Upgrade path exists.** Separating Docker storage onto a dedicated volume in v2 is a non-breaking change that only affects the bootstrap script and volume provisioning logic.

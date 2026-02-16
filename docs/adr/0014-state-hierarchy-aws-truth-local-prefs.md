# ADR-0014: State Hierarchy — AWS as Source of Truth, Local Config for Preferences

## Status
Accepted

## Context
A CLI tool that manages cloud resources must decide where different categories of data live. There are three natural tiers:

1. **AWS itself** (EC2 instances, tags, security groups) — the resources are the state.
2. **Local files** (config) — fast, user-owned, no network dependency.
3. **Dedicated AWS resources** (DynamoDB tables, S3 buckets, SSM Parameter Store) — durable and shared, but add infrastructure that must itself be provisioned and maintained.

Mint needs to store two kinds of data: **resource state** (which VMs exist, their IPs, their status) and **user preferences** (region, instance type, idle timeout). A third kind — **derived data** (owner identity, EIP addresses, bootstrap status) — could be cached locally for speed or queried fresh each time.

The temptation is to cache AWS state locally for faster CLI response times, or to create dedicated AWS resources for shared metadata. Both introduce synchronization problems disproportionate to Mint's scope.

## Decision
Mint follows a strict state hierarchy:

**Tier 1 — AWS is the source of truth for resource state.** Instance existence, status, IPs, tags, and security groups are always queried live from AWS APIs. Nothing about resource state is cached locally. If AWS says a VM is running, it's running. If a tag is missing, the resource is not Mint-managed. This eliminates state drift, stale caches, and the "works on my machine" class of bugs.

**Tier 2 — Local files store user preferences.** Configuration (`~/.config/mint/config.toml`) holds user preferences: region, default instance type, default volume size, idle timeout. No SSH keys or secrets are stored locally — SSH access uses EC2 Instance Connect with ephemeral keys (see ADR-0007). These config files are user-owned, machine-specific, and do not need to be shared or synchronized. They represent what the user *wants*, not what *exists*.

**Tier 3 — No dedicated AWS resources for Mint metadata.** Mint does not create DynamoDB tables, S3 buckets, SSM parameters, or any other AWS resource to store its own metadata. The admin-created IAM role and instance profile are the only shared AWS resources, and they exist for EC2 permissions, not for data storage. Tags on existing resources carry all the metadata Mint needs.

**Derived data is re-derived, not cached.** Owner identity comes from `sts get-caller-identity` on every invocation (see ADR-0013). Elastic IPs come from `ec2 describe-addresses` filtered by tags. Bootstrap status comes from instance tags. The ~100ms per API call is acceptable for a CLI that manages a handful of resources.

## Consequences
- **No synchronization bugs.** There is no local cache that can disagree with AWS reality.
- **Multi-machine use works naturally.** A developer can run Mint from their laptop, a CI runner, or a colleague's machine and see the same resources — only the local preferences differ.
- **No infrastructure bootstrapping beyond IAM.** New users run `mint init` and start working. No "first, create this DynamoDB table" step.
- **Slower than a local cache.** Every command makes AWS API calls. For a tool managing 1-5 VMs, this adds sub-second latency, not a usability problem.
- **Local config is not backed up.** If a user loses their machine, they lose their config file. Config is trivially recreated via `mint init`. No SSH keys are stored locally (ADR-0007).
- **AWS API rate limits are a non-concern at this scale.** A single user running CLI commands will never approach EC2 API rate limits.

# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What is Mint?

Mint is a CLI tool that provisions and manages EC2-based development environments for running Claude Code. It manages the **host VM only** — provisioning, connectivity, and lifecycle. Users may develop directly on the VM or inside devcontainers; most projects provide a devcontainer. Mint does not control, configure, or manage containers. Its job ends at getting the user connected to the host via VS Code Remote-SSH, mosh, or SSH — from there, VS Code's Dev Containers extension and the user's own tooling take over.

**Primary workflow**: MacBook → VS Code Remote-SSH → EC2 host → (user opens devcontainer via VS Code)
**Secondary workflow**: iPad (Termius) → mosh → EC2 host → tmux → (user connects to container manually)

**Current state**: Specification and architecture phase. `docs/SPEC.md` is the authoritative specification. `docs/adr/` contains Architecture Decision Records that are binding design constraints.

## The Five Keys

These values guide every decision in Mint's development — from architecture to error messages. All five matter. The ordering only applies when values conflict.

**1. Correctness** — Quality is non-negotiable. Tests pass, gates hold, code works. When speed conflicts with correctness, correctness wins. Every time.

**2. Transparency** — Show your work. Make state visible, explain decisions, surface errors clearly. No magic, no silent failures. Developers should always know what happened and why.

**3. Craft** — Every interaction should feel considered. Helpful errors, sensible defaults, zero unnecessary friction. Polish isn't vanity — it's respect for the developer's time.

**4. Conviction** — Be opinionated. Strong defaults guide developers toward the pit of success. Provide escape hatches, not blank canvases. When the tool knows better, it should lead.

**5. Fun** — This tool has a voice. Themed commands spark personality, power features reward the curious, and visible progress celebrates your wins. Developer tooling doesn't have to be joyless.

## Core Architecture Principles

These are non-negotiable constraints from the ADRs. Do not deviate without updating the relevant ADR first.

- **Tag-based state** (ADR-0001): No local state files. All AWS resources discovered via tags (`mint`, `mint:component`, `mint:vm`, `mint:owner`, `mint:owner-arn`, `mint:bootstrap`, `Name`). Multi-user isolation via `mint:owner` filtering.
- **AWS is source of truth** (ADR-0014): Always query live AWS state. Local config (`~/.config/mint/config.toml`) stores only user preferences (region, instance type, volume size, idle timeout). No DynamoDB/S3/SSM for metadata.
- **Runtime owner derivation** (ADR-0013): Owner derived from `aws sts get-caller-identity` on every invocation. Never stored in config. ARN normalized to friendly name.
- **Trusted-team security** (ADR-0005): Tags are conventions for UX, not access control. All users share PowerUser permissions.
- **EC2 Instance Connect** (ADR-0007): Ephemeral SSH keys, no key management. `mint key add` escape hatch for clients that can't use Instance Connect.
- **Single VM, multiple projects** (ADR-0002): One VM hosts multiple projects. Advanced users can run multiple VMs via `--vm`.
- **tmux on host** (ADR-0003): Not inside containers. Survives container rebuilds and iOS app suspension.
- **Single 200GB gp3 root volume** (ADR-0004): No separate Docker volume.
- **Default VPC** (ADR-0010): No custom networking, no bastion, no NAT gateway.
- **Non-standard ports, open inbound** (ADR-0016): SSH on non-standard high port, mosh on 60000-61000. Open to all IPs; security via key-only auth, not network restriction.
- **Permission before modifying user files** (ADR-0015): Mint prompts before writing files outside `~/.config/mint/`. Approval remembered in config.

## CLI UX Conventions (ADR-0012)

- TOML config at `~/.config/mint/config.toml`
- `--json` flag on list/status commands for machine-readable output
- `--verbose` (progress steps) and `--debug` (AWS SDK details) global flags
- `--yes` to skip confirmation on destructive operations (`mint destroy`)
- `--vm <name>` defaults to `default` and can be omitted for single-VM users

## Key Reference Documents

| Document | Purpose |
|----------|---------|
| `docs/SPEC.md` | Complete specification — the authoritative source |
| `docs/adr/0001-*.md` through `docs/adr/0016-*.md` | Architecture Decision Records — binding constraints |

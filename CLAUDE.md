# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What is Mint?

Mint is a CLI tool that provisions and manages EC2-based development environments for running Claude Code. It manages the **host VM only** — provisioning, connectivity, and lifecycle. Users may develop directly on the VM or inside devcontainers; most projects provide a devcontainer. Mint does not control, configure, or manage containers. Its job ends at getting the user connected to the host via VS Code Remote-SSH, mosh, or SSH — from there, VS Code's Dev Containers extension and the user's own tooling take over.

**Primary workflow**: MacBook → VS Code Remote-SSH → EC2 host → (user opens devcontainer via VS Code)
**Secondary workflow**: iPad (Termius) → mosh → EC2 host → tmux → (user connects to container manually)

**Current state**: Phase 0–1 implemented. Full provisioning lifecycle (up, recreate, destroy), SSH/mosh connectivity, projects, sessions, idle detection, self-update, and developer tooling. `docs/SPEC.md` is the authoritative specification. `docs/adr/` contains Architecture Decision Records that are binding design constraints.

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
- **Three-tier storage** (ADR-0004): Root EBS (200GB, ephemeral), User EFS (mounted at `/mint/user`, persistent), Project EBS (50GB default, VM-scoped).
- **Default VPC** (ADR-0010): No custom networking, no bastion, no NAT gateway.
- **Non-standard ports, open inbound** (ADR-0016): SSH on non-standard high port, mosh on 60000-61000. Open to all IPs; security via key-only auth, not network restriction.
- **Permission before modifying user files** (ADR-0015): Mint prompts before writing files outside `~/.config/mint/`. Approval remembered in config.
- **Auto-stop idle detection** (ADR-0018): systemd timer checks SSH/mosh sessions, tmux clients, `claude` processes in containers, manual extend. `mint list` auto-warns on VMs exceeding idle timeout.
- **SSH host key TOFU** (ADR-0019): Trust-on-first-use with loud change detection. Keys stored in `~/.config/mint/known_hosts`.
- **Binary signing deferred** (ADR-0020): v1 uses checksum verification only. Signing (minisign → cosign) planned for v2.

## CLI UX Conventions (ADR-0012)

- TOML config at `~/.config/mint/config.toml`
- `--json` flag on list/status commands for machine-readable output
- `--verbose` (progress steps) and `--debug` (AWS SDK details) global flags
- `--yes` to skip confirmation on destructive operations (`mint destroy`)
- `--vm <name>` defaults to `default` and can be omitted for single-VM users
- `--profile <name>` overrides the AWS profile; persisted to config as `aws_profile`

## Build & Test Commands

```bash
go build ./...                    # build all packages
go test ./... -v -count=1         # run all tests (599 tests, 17 packages)
go test ./... -coverprofile=c.out # coverage (85.1%)
go vet ./...                      # lint
go generate ./...                 # regenerate bootstrap hash (run before build)
go mod tidy                       # always run after adding new dependencies
```

## Project Structure

| Package | Purpose |
|---------|---------|
| `cmd/` | Cobra CLI commands (up, destroy, ssh, sessions, extend, doctor, ssh-config, …) |
| `internal/cli/` | CLIContext struct — global flag propagation via `context.Context` |
| `internal/config/` | Viper/TOML config at `~/.config/mint/config.toml` with composable validators |
| `internal/identity/` | STS owner derivation + ARN normalization (ADR-0013) |
| `internal/aws/` | Narrow EC2/EFS/IC interfaces for mock injection; waiter interfaces |
| `internal/bootstrap/` | Bootstrap script SHA256 hash embedding via `go:generate` (ADR-0009) |
| `internal/logging/` | Structured JSON logs + audit log (JSON Lines format) |
| `internal/tags/` | Tag constants (`TagBootstrap`, `BootstrapComplete`, …) and `TagBuilder` fluent API |
| `internal/vm/` | VM discovery via tags — `FindVM` / `ListVMs`; never call DescribeInstances directly |
| `internal/progress/` | TTY-aware spinner; honors `MINT_NO_SPINNER=1`; injectable `Interactive bool` |
| `internal/provision/` | Provisioning lifecycle (up, destroy, bootstrap polling with `isTerminal` injectable) |
| `internal/session/` | Idle detection per ADR-0018: tmux, SSH, claude processes, extended-until |
| `internal/sshconfig/` | Managed SSH config blocks with checksum hand-edit detection (ADR-0008/ADR-0019) |
| `internal/selfupdate/` | GitHub Releases self-update with SHA256 verification (ADR-0020) |
| `internal/version/` | Embedded version string |
| `scripts/` | `bootstrap.sh` — full VM setup script for Ubuntu 24.04; `bootstrap-stub.sh` — 871-byte EC2 user-data stub that fetches and verifies bootstrap.sh at runtime |
| `tests/e2e/` | End-to-end test scaffolding (see `/live-test` slash command) |

## Development Patterns

- Run `go mod tidy` after adding new Go dependencies — Viper's `WriteConfigAs` and AWS SDK imports frequently get incorrect `// indirect` annotations without it
- Config tests use `MINT_CONFIG_DIR` env var and `t.TempDir()` to avoid writing to real `~/.config/mint/`
- **Devcontainer config dir**: The devcontainer sets `MINT_CONFIG_DIR=/workspaces/mint/.mint-test`. Any files that `mint` reads from the config dir at runtime (e.g. `user-bootstrap.sh`) must be placed there, not in `~/.config/mint/`, when testing inside the devcontainer.
- AWS clients use narrow interfaces (e.g., `STSClient`, `DescribeInstanceTypesAPI`) for mock injection in tests
- Config validation uses a callback pattern (`InstanceTypeValidatorFunc`) to keep the config package decoupled from AWS
- Bootstrap script hash is embedded at compile time — always run `go generate ./...` before building if `scripts/bootstrap.sh` changes
- **Tag constants**: All tag keys and bootstrap status values live in `internal/tags/tags.go`. Never inline tag strings — always use `tags.TagBootstrap`, `tags.BootstrapComplete`, etc.
- **VM discovery**: Always use `vm.FindVM(ctx, client, owner, vmName)` or `vm.ListVMs(...)`. `FindVM` returns `(nil, nil)` when no VM exists — that is not an error. Never call DescribeInstances without `tags.FilterByOwner()`.
- **EC2 state sequencing**: Use waiter interfaces before dependent operations: `WaitInstanceRunningAPI` before EBS attach; `WaitInstanceTerminatedAPI` before volume deletion. Waiters are injected via `WithWaitRunning()` / `WithWaitTerminated()` builder methods.
- **Bootstrap script size**: `scripts/bootstrap.sh` is **no longer size-constrained** — EC2 user-data is now the 871-byte `scripts/bootstrap-stub.sh` which fetches bootstrap.sh at runtime (ADR-0009, #159). After any change to `bootstrap.sh`: run `go generate ./internal/bootstrap/...` and confirm `hash_generated.go` changed.
- **Bootstrap pre-check before SSH**: Check `found.BootstrapStatus` before SSH operations. Use `tags.BootstrapPending` / `tags.BootstrapFailed` constants. Use `isSSHConnectionError(err)` and `isTOFUError(err)` helpers in `cmd/sshutil.go`.
- **Optional-AWS commands**: Commands that sometimes need AWS (doctor, ssh-config) return `false` from `commandNeedsAWS()` and self-initialize clients in `RunE`. Follow the doctor pattern with `errorIdentityResolver` for graceful credential failures.
- **TTY-aware behavior**: For code that checks terminal state, inject a `func() bool` field (e.g., `isTerminal`) rather than calling `term.IsTerminal()` directly. This allows test-time override without subprocess spawning.

## Key Reference Documents

| Document | Purpose |
|----------|---------|
| `docs/SPEC.md` | Complete specification — the authoritative source |
| `docs/ROADMAP.md` | Phased implementation plan (Phase 0–4) |
| `docs/adr/0001-*.md` through `docs/adr/0024-*.md` | Architecture Decision Records — binding constraints |
| `docs/command-reference.md` | Complete command documentation with ADR cross-references |
| `.devcontainer/` | Developer isolated environment (Go 1.24, AWS CLI, isolated `MINT_CONFIG_DIR`) |

## Troubleshooting: SSH Access to a Mint VM

When a VM is running but `mint ssh` isn't available (e.g. bootstrap failed, devcontainer context, no mint binary), use EC2 Instance Connect directly:

1. **List available AWS profiles**
   ```bash
   aws configure list-profiles
   ```

2. **Get the instance's AZ** (instance ID is shown in bootstrap failure messages and `mint list` output)
   ```bash
   aws --profile <profile> --region <region> ec2 describe-instances \
     --instance-ids <instance-id> \
     --query 'Reservations[0].Instances[0].Placement.AvailabilityZone' \
     --output text
   ```

3. **Generate a temporary key pair**
   ```bash
   ssh-keygen -t ed25519 -f /tmp/mint-tmp-key -N ""
   ```

4. **Push the public key** (valid for 60 seconds)
   ```bash
   aws --profile <profile> --region <region> ec2-instance-connect send-ssh-public-key \
     --instance-id <instance-id> \
     --instance-os-user ubuntu \
     --availability-zone <az> \
     --ssh-public-key file:///tmp/mint-tmp-key.pub
   ```

5. **SSH in immediately**
   ```bash
   ssh -i /tmp/mint-tmp-key -p 41122 ubuntu@<public-ip>
   ```

6. **Check bootstrap logs**
   ```bash
   sudo journalctl -u cloud-final --no-pager | grep mint-bootstrap
   ```

The Mint SSH port is **41122** (ADR-0016). The OS user is always `ubuntu`. The profile to use is typically `PowerUserAccess-<account-id>` — run `aws configure list-profiles` to confirm.

## Quality Gates

The following commands are used during automated quality gates:

**Test command**: `go test ./... -count=1`
**Lint command**: `go vet ./...`

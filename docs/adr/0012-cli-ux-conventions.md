# ADR-0012: CLI UX Conventions

## Status
Accepted

## Context
Mint is a CLI tool used daily by developers. CLI UX decisions affect every interaction. Conventions need to cover configuration format and location, output modes for human and machine consumption, verbosity controls, destructive operation safeguards, progress feedback for long-running AWS operations, and a standard command surface including maintenance, self-update, and VM lifecycle operations.

## Decision
Adopt the following conventions across the Mint CLI:

**Configuration**: `~/.config/mint/config.toml`
- Follows XDG Base Directory conventions (`~/.config/`).
- TOML format: human-readable, well-supported in the Rust/Go/Python ecosystems.
- Flat structure. No nesting. All keys are snake_case.
- Config covers: AWS region, default instance type, default volume size, idle timeout, and whether Mint has permission to write SSH config files. Owner identity is derived at runtime from AWS credentials, not stored in config (see ADR-0013).

  | Key | Type | Description |
  |-----|------|-------------|
  | `region` | string | AWS region (e.g. `us-east-1`) |
  | `instance_type` | string | Default EC2 instance type (e.g. `t3.medium`) |
  | `volume_size_gb` | integer | Project EBS volume size in GB (default 50; root EBS is fixed at 200GB) |
  | `idle_timeout_minutes` | integer | Minutes of idle before auto-stop |
  | `ssh_config_approved` | boolean | Whether user has approved Mint writing SSH config entries (see ADR-0015) |

**Config validation** (`mint config set` validates aggressively on write):
- `instance_type` is validated against the AWS API — Mint confirms the type exists in the configured region before accepting it.
- `volume_size_gb` must be >= 50.
- `idle_timeout_minutes` must be >= 15.
- Unknown keys are rejected with a clear error.
- All validation failures surface immediately with an actionable message. Mint does not silently accept bad config and fail later.

**Machine-readable output**: `--json` flag on all read commands (`list`, `status`, `config`, `project list`). Outputs structured JSON for scripting and piping. Not available on write commands (`up`, `down`, `destroy`) where the primary output is progress feedback.

**Verbosity**: `--verbose` and `--debug` flags available globally.
- `--verbose`: shows progress steps and operational phases during long-running commands.
- `--debug`: shows AWS SDK call details and request/response payloads. For troubleshooting.

**Destructive operation safety**: `mint destroy` requires interactive confirmation by default. The user must type the VM name to confirm. A `--yes` flag bypasses confirmation for use in scripts and automation.

**Progress feedback**: Long-running operations (`mint up`, `mint destroy`) display progress spinners with phase labels (e.g., "Launching instance...", "Waiting for bootstrap...", "Allocating Elastic IP..."). Phases correspond to actual AWS operations, not arbitrary progress percentages.

**Standard commands**: The following commands are part of the required command surface:

- `mint version` — Non-negotiable CLI standard. Prints the Mint version and exits. No flags required.

- `mint doctor` — Validates environment health. Checks AWS credentials, region configuration, service quota headroom (Elastic IPs, vCPUs), SSH config sanity, and VM health (including `mint:health` tag status) for any running VMs. Use `--vm` to target a specific VM. Recommended after `mint init`. On-demand at any time.
  - `mint doctor --fix` is the explicit repair path. When doctor detects drift or fixable issues, it does not auto-repair. The user must pass `--fix` to authorize corrective action.
  - `--json` outputs structured results for scripting.

- `mint resize [--vm <name>] <instance-type>` — Changes the EC2 instance type of a running or stopped VM. Sequence: stop → modify → start. All volumes are preserved. Fails fast with a clear error if the requested type does not exist in the configured region. See ADR-0017.

- `mint recreate [--vm <name>]` — Rebuilds the VM OS and Docker environment from scratch while preserving project data (EBS volume contents, mounted project directories) and user config. Use when the host environment is corrupted or needs a clean slate. See ADR-0017.

- `mint update` — Self-updates the Mint binary using atomic replacement: downloads the new binary to a temp path, verifies the checksum, then replaces the existing binary via `mv`. No package manager required. Fails and leaves the existing binary untouched if the checksum does not match.

## Consequences
- **Predictable behavior.** Developers familiar with XDG conventions and common CLI patterns (`--json`, `--verbose`, `--yes`) can use Mint without reading documentation for basic operations.
- **Scriptable.** The `--json` flag and `--yes` flag enable Mint integration into shell scripts, CI/CD pipelines, and wrapper tools.
- **Safe defaults.** Destructive operations require confirmation. Users must opt in to bypass safety with `--yes`. `mint doctor --fix` makes repair intent explicit.
- **TOML trade-off.** TOML is less universal than JSON or YAML but more readable for config files. The flat snake_case schema keeps the format simple and avoids any ambiguity from nested keys.
- **Aggressive config validation.** Validating `instance_type` against the AWS API adds a network round-trip on every `mint config set instance_type ...`, but catches typos and unsupported types before they cause confusing failures later.
- **Spinner dependency.** Progress spinners require a TTY. Non-interactive environments (CI, pipes) should detect this and fall back to line-by-line progress output.
- **Atomic self-update.** Checksum verification and atomic `mv` mean a failed or interrupted `mint update` leaves the existing binary intact. Users are never left with a broken Mint installation.

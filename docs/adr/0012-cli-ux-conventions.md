# ADR-0012: CLI UX Conventions

## Status
Accepted

## Context
Mint is a CLI tool used daily by developers. CLI UX decisions affect every interaction. Conventions need to cover configuration format and location, output modes for human and machine consumption, verbosity controls, destructive operation safeguards, and progress feedback for long-running AWS operations.

## Decision
Adopt the following conventions across the Mint CLI:

**Configuration**: `~/.config/mint/config.toml`
- Follows XDG Base Directory conventions (`~/.config/`).
- TOML format: human-readable, supports nested keys, well-supported in the Rust/Go/Python ecosystems.
- Config covers: AWS region, default instance type, default volume size, idle timeout. Owner identity is derived at runtime from AWS credentials, not stored in config (see ADR-0013).

**Machine-readable output**: `--json` flag on all read commands (`list`, `status`, `config`, `project list`). Outputs structured JSON for scripting and piping. Not available on write commands (`up`, `down`, `destroy`) where the primary output is progress feedback.

**Verbosity**: `--verbose` and `--debug` flags available globally.
- `--verbose`: shows progress steps and operational phases during long-running commands.
- `--debug`: shows AWS SDK call details and request/response payloads. For troubleshooting.

**Destructive operation safety**: `mint destroy` requires interactive confirmation by default. The user must type the VM name to confirm. A `--yes` flag bypasses confirmation for use in scripts and automation.

**Progress feedback**: Long-running operations (`mint up`, `mint destroy`) display progress spinners with phase labels (e.g., "Launching instance...", "Waiting for bootstrap...", "Allocating Elastic IP..."). Phases correspond to actual AWS operations, not arbitrary progress percentages.

## Consequences
- **Predictable behavior.** Developers familiar with XDG conventions and common CLI patterns (--json, --verbose, --yes) can use Mint without reading documentation for basic operations.
- **Scriptable.** The `--json` flag and `--yes` flag enable Mint integration into shell scripts, CI/CD pipelines, and wrapper tools.
- **Safe defaults.** Destructive operations require confirmation. Users must opt in to bypass safety with `--yes`.
- **TOML trade-off.** TOML is less universal than JSON or YAML but more readable for config files with nested keys. The config file is simple enough that format choice is low-stakes.
- **Spinner dependency.** Progress spinners require a TTY. Non-interactive environments (CI, pipes) should detect this and fall back to line-by-line progress output.

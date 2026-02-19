# ADR-0021: Developer Devcontainer for Mint Contributors

## Status
Accepted

## Context
Mint's development cycle creates two categories of side effects that are unsafe to run directly on a contributor's machine without isolation.

**Config and credential writes.** Every `mint` invocation resolves config from `~/.config/mint/config.toml`, writes logs to `~/.config/mint/logs/`, and stores SSH host keys in `~/.config/mint/known_hosts`. `mint connect` and `mint ssh` may write to `~/.ssh/config` after prompting for approval (ADR-0015). Running tests or CLI commands without isolation risks corrupting a contributor's real Mint config, polluting their SSH config, or mixing test host keys into their known-hosts store.

**Real AWS calls.** Any command that reaches EC2 — `mint up`, `mint destroy`, `mint list` — provisions real resources and incurs real charges. A careless `go test ./...` run that touches integration paths without mocks could create orphaned instances, security groups, or EBS volumes in the team's shared AWS account. While the current test suite uses mock AWS clients throughout (`go test ./...` makes zero real AWS calls), this boundary requires active maintenance. A devcontainer makes the zero-AWS-calls invariant visible and enforceable: contributors working inside the container can observe that no AWS credentials are mounted, preventing accidental real-resource operations while writing tests.

**Runtime binary dependencies.** Mint shells out to several binaries at runtime that may not be present on a contributor's machine: `ssh`, `ssh-keyscan`, and `ssh-keygen` (from `openssh-client`), `mosh`, `tmux`, `jq`, and `nc` (netcat). Integration tests that exercise these paths — even with mock AWS — need the binaries present. Without a reproducible environment, contributors encounter silent skip-or-fail behavior depending on what happens to be installed on their machine.

**Bootstrap hash generation.** The bootstrap script's SHA256 hash is embedded in the Go binary at compile time via `go:generate` (ADR-0009). Contributors who skip `go generate ./...` before building get a stale hash and a confusing test failure. A devcontainer `postCreateCommand` runs `go generate ./...` automatically, eliminating this class of setup error.

A committed devcontainer configuration solves all four problems: it provides a reproducible build environment, isolates Mint's config writes, makes the no-credentials boundary explicit, and ensures all runtime binaries are present from the first `code .`.

## Decision

Provide a `.devcontainer/` directory committed to the repository that defines the contributor development environment.

### Base image

Use `mcr.microsoft.com/devcontainers/go:1-1.24-bookworm` as the base image.

This image is the official VS Code Go devcontainer published by Microsoft. It ships Go 1.24 on Debian Bookworm and includes the Go toolchain, VS Code server infrastructure, common CLI utilities, and the devcontainer feature framework. Using the official image rather than a custom Dockerfile keeps maintenance burden low — Microsoft publishes security updates regularly and the image tracks Go minor releases. Pinning to `1-1.24-bookworm` (floating patch, pinned minor) keeps the environment current without requiring manual image updates for every Go patch release.

### Features

Enable the `ghcr.io/devcontainers/features/aws-cli:1` feature.

The AWS CLI is required to configure credentials for manual testing of AWS-backed commands. It is not required for `go test ./...` (which uses mocks), but contributors who want to run `mint up` or `mint list` against the real team account need `aws` in PATH. Installing via the feature mechanism keeps the Dockerfile minimal and benefits from the feature's own maintenance cycle.

### Apt packages

Install the following packages in the `devcontainer.json` `postCreateCommand` or via the `ghcr.io/devcontainers/features/common-utils` feature's `installPackages` option:

| Package | Provides | Why needed |
|---------|----------|------------|
| `openssh-client` | `ssh`, `ssh-keyscan`, `ssh-keygen` | Mint shells out to all three for connection management and host key operations |
| `mosh` | `mosh` | Required for `mint connect --mosh` paths; integration tests that exercise mosh invocation need the binary present |
| `tmux` | `tmux` | Mint manages tmux sessions on the host VM; contributors testing tmux-related code paths need the binary |
| `jq` | `jq` | Used in `scripts/bootstrap.sh` and in ad-hoc debugging of `--json` flag output during development |
| `netcat-openbsd` | `nc` | Mint uses `nc` for port-availability checks before SSH and mosh connections; the OpenBSD variant's flags match the invocation in Mint's source |

### MINT_CONFIG_DIR isolation

Set `MINT_CONFIG_DIR=${containerWorkspaceFolder}/.mint-test` as a container environment variable in `devcontainer.json`.

This env var causes `internal/config.DefaultConfigDir()` to return the workspace-relative path instead of `~/.config/mint/`. All config reads, log writes, and known-hosts storage are redirected to `.mint-test/` inside the workspace. The directory is git-ignored. This means:

- Running `go test ./...` inside the container never touches the contributor's real `~/.config/mint/`.
- Running CLI commands manually (e.g., `go run . config`) writes into the container-local `.mint-test/` directory, not into the contributor's home directory.
- Tests that use `t.TempDir()` already override `MINT_CONFIG_DIR` explicitly; the container-level default acts as a safety net for any test that doesn't.

`.mint-test/` should be added to `.gitignore` so contributors do not accidentally commit test state.

### No host credential mounting

The devcontainer does not mount the host's `~/.aws/` directory or any credential files.

This is intentional. The container's default state has no AWS credentials, which means `go test ./...` (which uses mocks) works without credentials, and any attempt to run a real AWS command fails immediately with a clear "no credentials configured" error rather than silently operating against the real account. Contributors who need to run live AWS commands configure credentials explicitly inside the container using `aws configure` or by mounting credentials through VS Code's credential forwarding mechanism — both of which require deliberate action.

This boundary enforces the invariant that unit and integration tests never make real AWS calls, and makes violations visible rather than silent.

### go generate in postCreateCommand

The `postCreateCommand` in `devcontainer.json` runs:

```bash
go generate ./... && go build ./...
```

`go generate ./...` regenerates `internal/bootstrap/hash_generated.go` from the current `scripts/bootstrap.sh`. This ensures contributors start with a correct hash constant and do not hit the "ScriptSHA256 constant is empty; run go generate" test failure that would otherwise appear on a fresh clone.

`go build ./...` validates that the generated code compiles correctly and pre-warms the Go module cache, making the first `go test ./...` faster.

## Consequences

- **Reproducible environment.** All contributors build and test against the same Go version, toolchain, and runtime binaries. "Works on my machine" failures from missing `mosh` or `nc` are eliminated.
- **Isolated test state.** `MINT_CONFIG_DIR=${containerWorkspaceFolder}/.mint-test` ensures no test run ever writes to a contributor's real `~/.config/mint/`. The isolation is on by default — contributors do not need to remember to set the variable.
- **Explicit AWS credential boundary.** No credentials are mounted by default. Contributors must perform a deliberate action to configure AWS access inside the container. This makes the mock-only test suite the path of least resistance and prevents accidental real-resource operations.
- **Manual credential setup for AWS-backed commands.** Contributors who want to run `mint up`, `mint destroy`, or `mint list` against the real team account must configure AWS credentials inside the container themselves. This is a one-time setup step documented in the contribution guide. VS Code's `aws-credentials` forwarding or `aws configure` inside the terminal are the two supported paths.
- **`.mint-test/` cleanup.** The `.mint-test/` directory accumulates state from manual CLI invocations inside the container. Contributors can delete it at any time — `rm -rf .mint-test/` — with no consequence beyond losing any locally configured test preferences. It is never committed. The postCreateCommand does not pre-create it; Mint creates it on first use.
- **go generate required for correct builds.** The `postCreateCommand` handles this automatically on container creation. If a contributor modifies `scripts/bootstrap.sh` during development, they must re-run `go generate ./...` manually before building. The test suite's assertion on `ScriptSHA256` being non-empty will catch a forgotten regeneration.
- **Image update cadence.** Pinning to `1-1.24-bookworm` floats on patch releases. When Go 1.25 ships, a contributor updates the image tag in `devcontainer.json` and opens a PR. No other maintenance is required between minor releases.

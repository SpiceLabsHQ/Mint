# Contributing to Mint

This guide covers everything needed to build, test, and iterate on Mint itself. For codebase conventions and Claude Code guidance, see [`CLAUDE.md`](../CLAUDE.md).

## Prerequisites

**Recommended: VS Code with Dev Containers extension**

The repository ships a devcontainer (`.devcontainer/devcontainer.json`) that provides a fully isolated, reproducible environment — correct Go version, all runtime binaries, and config isolation out of the box. See [ADR-0021](adr/0021-developer-devcontainer.md) for rationale.

Required: VS Code with the [Dev Containers extension](https://marketplace.visualstudio.com/items?itemName=ms-vscode-remote.remote-containers) and Docker.

**Alternative: Local Go toolchain**

Go 1.24.0 or later. No other tooling is required to build and run the test suite. For manual testing against AWS, you also need the AWS CLI.

## Quick Start

1. Clone the repository and open it in VS Code.
2. When prompted, click **Reopen in Container** (or run `Dev Containers: Reopen in Container` from the command palette).
3. The `postCreateCommand` runs automatically: it installs runtime dependencies, downloads Go modules, and runs `go generate ./...` to embed the bootstrap script hash.
4. Verify the environment:

```bash
go test ./... -v -count=1
```

All tests should pass. Zero real AWS calls are made — the test suite uses mock clients throughout.

## Build & Test Commands

```bash
go build ./...                    # build all packages
go test ./... -v -count=1         # run all tests
go test ./... -coverprofile=c.out # run with coverage
go vet ./...                      # lint
go generate ./...                 # regenerate bootstrap hash (run before build)
go mod tidy                       # always run after adding new dependencies
```

Run `go generate ./...` before building any time `scripts/bootstrap.sh` changes. The test suite will fail with a clear message if the embedded hash is stale.

## Manual Testing with AWS

The devcontainer sets `MINT_CONFIG_DIR=${containerWorkspaceFolder}/.mint-test` in its environment. This redirects all config reads, log writes, and known-hosts storage to `.mint-test/` inside the workspace — your real `~/.config/mint/` is never touched. The directory is git-ignored.

**The container has no AWS credentials by default.** This is intentional: `go test ./...` works without credentials (mock clients only), and any accidental attempt to reach real AWS fails immediately with a clear error.

To run commands against real AWS (e.g., `mint up`, `mint list`):

1. Configure credentials inside the container terminal:
   ```bash
   aws configure
   # or export AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY / AWS_SESSION_TOKEN
   ```
2. Build and run the CLI:
   ```bash
   go build -o mint . && ./mint list
   ```

To reset all test state accumulated from manual runs:

```bash
rm -rf .mint-test/
```

Mint recreates `.mint-test/` on next use. No other cleanup is needed.

## Architecture Orientation

Start here, in order:

| Document | What it covers |
|----------|----------------|
| [`docs/SPEC.md`](SPEC.md) | Complete specification — authoritative source for all behavior |
| [`docs/adr/`](adr/) | Architecture Decision Records 0001–0021 — binding design constraints |
| [`CLAUDE.md`](../CLAUDE.md) | Package map, development patterns, key conventions |

The ADRs are the most important documents to read before making structural changes. Each ADR is a binding decision; deviating requires updating the relevant ADR in the same PR.

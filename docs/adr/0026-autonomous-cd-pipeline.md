# ADR-0026: Autonomous Continuous Delivery via go-semantic-release

## Status
Accepted

## Context
Mint's releases have been manual. A developer tags a commit, pushes the tag, and the release workflow runs goreleaser to build binaries and create a GitHub Release. This process has three friction points:

1. **Manual version decisions.** The developer must determine whether a change is a patch, minor, or major bump, then construct and push the correct tag. This is error-prone — a breaking change tagged as a patch violates semver, and the decision is not auditable after the fact.
2. **Release frequency limited by ceremony.** Because each release requires a human to decide "now is a good time to release," changes accumulate on `main` between tags. Users running `mint update` do not see fixes until someone remembers to cut a release.
3. **No connection between commit intent and version.** The changelog is generated from git history by goreleaser's built-in changelog, which sorts commits alphabetically and filters by prefix. There is no structured mapping from commit type to version bump.

The goal is fully autonomous continuous delivery: every merge to `main` that passes CI automatically determines the next version, creates a tag, publishes a GitHub Release with binaries, and makes the new version available to `mint update`. No human intervention, no manual tagging, no release PRs.

This requires adopting conventional commits as the structured signal for version determination. Commits following the `type(scope): description` format — where `feat` triggers a minor bump, `fix` triggers a patch bump, and `BREAKING CHANGE` triggers a major bump — provide the machine-readable intent that eliminates manual version decisions.

### Constraints

- **Artifact format preserved.** The `mint update` self-update mechanism (ADR-0020, `internal/selfupdate/`) expects `mint_<version>_<os>_<arch>.tar.gz` archives and a `checksums.txt` file. Any CD pipeline must produce these exact artifacts.
- **Checksum verification preserved.** `mint update` performs SHA256 verification of downloaded archives against `checksums.txt` (ADR-0020). The pipeline must not alter the checksum workflow.
- **Go toolchain.** Mint is a Go project. The release tooling should not introduce a Node.js or Python runtime dependency into the CI environment.
- **goreleaser for builds.** goreleaser already handles cross-compilation, archive naming, checksum generation, and the `go generate` pre-build hook (ADR-0009 bootstrap hash embedding). Replacing it would mean reimplementing all of that.

## Decision

Adopt [go-semantic-release](https://github.com/go-semantic-release/semantic-release) for autonomous version management and tag creation. Retain goreleaser for binary builds and artifact publishing. The two tools run in a two-stage pipeline triggered by merges to `main`.

### Two-stage pipeline design

```
merge to main
  -> CI tests pass (build, test, lint)
  -> go-semantic-release analyzes commits since last tag
  -> determines bump type (feat = minor, fix/perf = patch, BREAKING = major)
  -> if no conventional bump signal: forces patch bump (force-bump-patch-version)
  -> creates git tag (vX.Y.Z) + GitHub Release (with raw commit notes)
  -> tag push triggers release.yml
  -> goreleaser builds binaries for darwin/linux amd64/arm64
  -> goreleaser uploads mint_<ver>_<os>_<arch>.tar.gz + checksums.txt
  -> goreleaser replaces GitHub Release body with grouped changelog
```

**Stage 1: Version and tag (ci.yml, on push to main).** After CI tests pass, a `release` job runs `go-semantic-release`. It analyzes all commits since the last semver tag and determines the bump type from conventional commit prefixes (`feat:` = minor, `fix:`/`perf:` = patch, `feat!:` or `BREAKING CHANGE` footer = major). If no commits carry a conventional bump signal, `force-bump-patch-version` ensures a patch bump occurs anyway. go-semantic-release creates the tag and a GitHub Release with raw commit notes. Every merge to `main` produces a release.

**Stage 2: Build and publish (release.yml, on tag push).** The existing release workflow triggers on `v*` tag pushes. goreleaser runs `go generate ./...` (embedding the bootstrap script hash per ADR-0009), cross-compiles for all target platforms, produces the `mint_<ver>_<os>_<arch>.tar.gz` archives and `checksums.txt`, and uploads them to the GitHub Release created in stage 1. goreleaser's changelog configuration replaces the raw release notes with a grouped, filtered changelog.

### Why two stages

Combining both tools in a single workflow is possible but creates two problems:

1. **goreleaser expects a tag to already exist.** It reads the tag to determine `{{ .Version }}` and `{{ .Tag }}` template variables used in archive names, ldflags, and the changelog. Running goreleaser before the tag exists requires `--snapshot` mode, which does not publish to GitHub Releases.
2. **Separation of concerns.** go-semantic-release owns the "what version is this" decision. goreleaser owns the "build and package binaries" execution. Neither tool needs to know about the other's configuration. The tag push event is the clean handoff point.

### go-semantic-release configuration

go-semantic-release is configured entirely through the GitHub Action's `with:` inputs in `ci.yml`. There is no `.semrelrc` or other configuration file in the repository. The action inputs are:

- **`github-token`**: The `GITHUB_TOKEN` secret, granting permission to create tags and GitHub Releases.
- **`allow-initial-development-versions: true`**: Permits `0.x.y` releases. Mint is pre-1.0; without this flag, go-semantic-release would refuse to create `v0.x.y` tags.
- **`force-bump-patch-version: true`**: Guarantees every merge to `main` produces a release. When commits since the last tag do not contain a conventional commit that maps to a bump (`feat`, `fix`, `perf`), go-semantic-release forces a patch bump rather than skipping the release. This ensures that housekeeping changes (`chore:`, `ci:`, `docs:`, `refactor:`, or non-conventional messages) still ship — users running `mint update` always get the latest code.

No plugins, hooks, or file updaters are configured. Mint does not maintain a VERSION file or `package.json` — the version is embedded via goreleaser's ldflags at build time. The action's built-in conventional commit analyzer handles all version determination.

### Conventional commits as the version signal

With this decision, conventional commit format becomes the primary mechanism for controlling version bumps on `main`. Commits following the format determine the bump type: `feat` triggers a minor bump, `fix` and `perf` trigger a patch bump, and a `!` suffix or `BREAKING CHANGE:` footer triggers a major bump.

The enforced format is:

```
type(scope): description

[optional body]

[optional footer(s)]
```

Where `type` is one of: `feat`, `fix`, `perf`, `refactor`, `docs`, `test`, `ci`, `chore`, `build`, `style`. Only `feat`, `fix`, and `perf` map to explicit version bumps. A `!` after the type/scope or a `BREAKING CHANGE:` footer triggers a major bump.

Commits that do not match a bump-triggering type — including non-conventional messages, `chore:`, `ci:`, `docs:`, `refactor:`, and `test:` — still produce a release because `force-bump-patch-version` is enabled. go-semantic-release forces a patch bump when no conventional bump signal is present. This means every merge to `main` ships, regardless of commit format. The practical effect is that conventional commits control whether a release is a minor or major bump, but a patch release happens unconditionally.

### goreleaser changelog replaces release notes

go-semantic-release creates the GitHub Release with raw commit-based notes. When goreleaser runs in stage 2, its changelog configuration (already present in `.goreleaser.yaml`) replaces the release body with a grouped, filtered changelog that excludes `docs:`, `test:`, `ci:`, and `chore:` commits. This means the final release notes seen by users are goreleaser's formatted output, not go-semantic-release's raw list.

## Alternatives Rejected

### release-please (Google)

[release-please](https://github.com/googleapis/release-please) uses a PR-based workflow: it opens a "Release PR" that bumps version files and updates a CHANGELOG.md. Merging the PR creates the tag and release.

Rejected because: (a) the PR-based model reintroduces human ceremony — someone must review and merge the release PR, defeating the goal of fully autonomous delivery; (b) release-please expects a version file or `package.json` to update, which Mint does not have — the version is injected via goreleaser ldflags at build time; (c) the intermediate PR creates noise in the repository's PR history, with every release producing a mechanical PR that no one meaningfully reviews; (d) release-please is designed for monorepos and multi-package projects, adding configuration complexity that Mint does not need.

### semantic-release (Node.js)

The original [semantic-release](https://github.com/semantic-release/semantic-release) is a mature, widely-adopted tool with a rich plugin ecosystem.

Rejected because: (a) it requires a Node.js runtime, which Mint's CI environment does not otherwise need — adding `actions/setup-node` and `npm install` to the release pipeline introduces a runtime dependency unrelated to the Go project; (b) its plugin system (npm packages) requires a `package.json` and `node_modules` in the repository, polluting a Go project with Node.js artifacts; (c) go-semantic-release provides the same core functionality (conventional commit analysis, tag creation, GitHub Release) as a single Go binary with no runtime dependencies, matching Mint's toolchain.

### Custom shell scripts

Write a shell script that parses `git log` since the last tag, determines the bump type from commit prefixes, and creates the tag.

Rejected because: (a) conventional commit parsing has edge cases (multi-line footers, `BREAKING CHANGE` in body vs. footer, scope with special characters) that a shell script would need to handle correctly — this is a solved problem in go-semantic-release; (b) the script becomes a maintenance burden that must be tested, documented, and debugged independently; (c) GitHub Release creation, error handling, and idempotency (re-running on the same commit) are non-trivial to implement correctly in shell; (d) Mint's engineering time is better spent on the tool itself than on release infrastructure.

### Manual tagging with goreleaser (status quo)

Continue the current workflow where a developer manually creates and pushes a semver tag.

Rejected because: (a) it is the problem this ADR solves — manual ceremony, infrequent releases, no connection between commit intent and version; (b) the current `workflow_dispatch` escape hatch in `release.yml` will be preserved for emergency releases, but it should not be the primary release mechanism.

## Consequences

- **Conventional commits control bump magnitude.** Conventional commit prefixes determine whether a release is a patch, minor, or major bump. Commits without a conventional bump signal (`feat`, `fix`, `perf`, or breaking change marker) still produce a patch release due to `force-bump-patch-version`. Conventional commit format is strongly encouraged — it controls changelog grouping and version semantics — but non-conventional commits do not block releases. Enforcement via commit linting (e.g., commitlint in a PR check) is recommended but not mandated by this ADR.
- **Every qualifying main merge ships.** There is no batching, no release train, no "is this a good time to release." A `feat:` commit merged to `main` will produce a new minor version within minutes. This is the desired behavior — users get fixes and features as soon as they pass CI.
- **goreleaser remains the build authority.** go-semantic-release does not build binaries or produce artifacts. It creates the tag and a placeholder GitHub Release. goreleaser owns cross-compilation, archive naming, checksum generation, and the final changelog. The `mint_<ver>_<os>_<arch>.tar.gz` and `checksums.txt` format required by `mint update` is preserved without modification.
- **Two-workflow architecture.** The release process spans two GitHub Actions workflows (`ci.yml` and `release.yml`), connected by the tag push event. A failure in stage 2 (goreleaser) means a tag and empty release exist without binaries. This is recoverable by re-running the release workflow manually or pushing a no-op fix commit that triggers stage 1 again.
- **No version file in the repository.** The version continues to be injected at build time via goreleaser's ldflags (`-X ...cmd.version={{ .Version }}`). There is no `VERSION` file, no `version.go` constant to update, and no release PR that bumps a version string. The git tag is the single source of truth for the version.
- **`workflow_dispatch` preserved as escape hatch.** The manual trigger on `release.yml` remains for emergency releases or re-runs. It is not the primary release mechanism.
- **Binary signing unaffected.** This ADR does not change the checksum-only verification model from ADR-0020. When binary signing is implemented (v2), the signing step will be added to the goreleaser stage of the pipeline.
- **Implementing PR:** #219

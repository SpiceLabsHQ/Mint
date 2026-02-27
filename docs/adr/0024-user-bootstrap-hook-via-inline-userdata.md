# ADR-0024: User Bootstrap Hook via Inline User-Data

## Status
Accepted

## Context
Mint provisions EC2 VMs with a standard set of tools (Docker, devcontainer CLI, mosh, tmux, Claude Code, etc.) via `scripts/bootstrap.sh`. After provisioning completes, developers frequently need personal VM customization: dotfiles, editor plugins, shell configuration, personal tooling, language-specific SDKs, or team-specific packages. Without a supported hook, users must SSH in after every `mint up` or `mint recreate` and manually run setup commands — a friction point that defeats the goal of one-command readiness.

The customization hook must satisfy three constraints:

1. **Per-user, not per-project.** Dotfiles and personal tools are the same regardless of which project the developer is working on. The hook should be sourced from the developer's local machine, not from a project repository.
2. **Private by default.** User bootstrap scripts may reference private registries, personal tokens, or internal URLs. The delivery mechanism must not require hosting the script on a publicly accessible endpoint.
3. **Size-bounded.** EC2 cloud-init enforces a hard 16,384-byte limit on user-data. The bootstrap stub, environment variables, and user bootstrap payload must all fit within this budget.

## Decision

Deliver user bootstrap customization via the `MINT_USER_BOOTSTRAP` environment variable in the EC2 user-data bootstrap stub. The variable carries the base64-encoded contents of `~/.config/mint/user-bootstrap.sh` from the developer's local machine.

### Mechanism

1. At `mint up` / `mint recreate` time, the CLI checks for the well-known path `~/.config/mint/user-bootstrap.sh` on the developer's machine.
2. If the file exists, its contents are base64-encoded and passed to `bootstrap.RenderStub(...)` as the `userBootstrap` parameter.
3. `RenderStub` substitutes the encoded payload into the `__MINT_USER_BOOTSTRAP__` placeholder in `scripts/bootstrap-stub.sh`.
4. The rendered stub (with all substitutions) is base64-encoded and sent as EC2 user-data.
5. On the instance, the stub fetches and executes `bootstrap.sh`. At the end of `bootstrap.sh` — after all Mint provisioning and the health check have passed — the script checks `MINT_USER_BOOTSTRAP`. If non-empty, it base64-decodes the value into a temporary file and executes it.
6. If the user script exits non-zero, bootstrap is marked failed (`mint:bootstrap=failed`). If it exits zero, bootstrap proceeds to tag `mint:bootstrap=complete`.

### Well-known path

The user bootstrap script lives at `~/.config/mint/user-bootstrap.sh`. This follows the XDG convention already established by Mint's config file (`~/.config/mint/config.toml`). There is no config key, CLI flag, or environment variable to override the path. One well-known location means one place to look, one thing to document, and zero ambiguity.

### Execution order guarantee

The user hook runs **after** all Mint-managed setup is complete — Docker, devcontainer CLI, mosh, tmux, Claude Code, EFS mount, idle detection, and the health check have all succeeded. This means the user script can depend on any tool that Mint installs. It runs **before** the final `mint:bootstrap=complete` tag is set, so a failing user script prevents `mint up` from reporting success.

### Size constraint

The 16,384-byte EC2 user-data limit applies to the entire rendered stub after base64 encoding. The stub itself is approximately 900 bytes before substitution. After accounting for the stub, SHA256 hash, GitHub URL, EFS ID, and other parameters, roughly 10,000 bytes remain available for the base64-encoded user script. Since base64 encoding expands data by approximately 33%, the practical limit on `user-bootstrap.sh` is approximately 7,500 bytes of script content. Mint validates this at `mint up` time and aborts with a clear error if the rendered user-data exceeds the limit.

### Failure semantics

A non-zero exit from the user bootstrap script causes `bootstrap.sh` to mark the instance as `mint:bootstrap=failed`. This is intentional: a developer who relies on their user bootstrap (e.g., to install a required tool or configure credentials) should not get a "ready" signal from a VM that silently skipped their setup. The developer sees the same bootstrap failure UX as any other bootstrap problem — stop, terminate, or debug (ADR-0009).

## Alternatives Rejected

### S3 presigned URL

Upload `user-bootstrap.sh` to S3, generate a presigned URL, and pass only the URL in user-data. This removes the size constraint entirely.

Rejected because: (a) Mint explicitly removed all S3 client code and dependencies in #180 to simplify the AWS surface area — reintroducing S3 for this feature contradicts that decision; (b) presigned URLs require the CLI to have `s3:PutObject` and `s3:GetObject` permissions, expanding the IAM footprint; (c) the presigned URL has a TTL, creating a window where a recreate could fail if the URL expires before the instance boots; (d) it requires creating and managing an S3 bucket, which conflicts with the "no infrastructure beyond EC2/EFS" principle (ADR-0010, ADR-0014).

### Separate URL hosting (personal web server, GitHub Gist, etc.)

Allow users to specify a URL to their bootstrap script via a config key like `user_bootstrap_url`.

Rejected because: (a) there is no private channel — a URL on a public gist or personal server exposes the script contents (which may contain tokens or internal URLs) to anyone who discovers the URL; (b) the fetch can fail due to DNS, rate limiting, or authentication, introducing a new class of transient bootstrap failures unrelated to Mint or AWS; (c) there is no integrity verification — unlike the main `bootstrap.sh` which has compile-time hash pinning (ADR-0009), a URL-fetched user script has no way to detect tampering without adding a separate verification mechanism.

### Config key or CLI flag for script path

Allow `mint config set user_bootstrap_path /path/to/my/setup.sh` or `mint up --user-bootstrap /path/to/setup.sh`.

Rejected because: (a) a config key adds a knob that must be validated, documented, and maintained for a feature that has exactly one natural location; (b) a CLI flag encourages per-invocation variation, making it harder to reason about what a given VM was provisioned with; (c) the well-known path pattern (`~/.config/mint/user-bootstrap.sh`) is already established by Mint's config file and known_hosts — users know where to look. One path, no configuration.

### EFS-based user script

Store the user bootstrap script on the EFS mount (`/mint/user/`) and have `bootstrap.sh` read it from there after mounting EFS.

Rejected because: (a) the EFS mount happens during bootstrap, creating a chicken-and-egg dependency — the user script would need to be placed on EFS by a previous VM, meaning first-time users have no way to populate it; (b) EFS contents persist across VM lifecycles, so a broken user script on EFS would break every subsequent `mint up` until the user manually SSH's in to fix it; (c) changes to the local `user-bootstrap.sh` would not take effect until the user manually copies the file to EFS, breaking the mental model of "edit locally, provision remotely."

## Consequences

- **One-command personalization.** Developers write `~/.config/mint/user-bootstrap.sh` once. Every `mint up` and `mint recreate` automatically applies it. No post-provisioning SSH required.
- **No new AWS dependencies.** The feature uses only the existing EC2 user-data mechanism. No S3, no SSM Parameter Store, no Secrets Manager.
- **Size-constrained.** User scripts are limited to approximately 7,500 bytes due to the EC2 user-data 16,384-byte limit. This is sufficient for dotfile cloning, package installation, and tool configuration, but not for large binary installations or complex multi-file setups. Users needing more space should use their user bootstrap to fetch a larger script from a private location.
- **Failure blocks provisioning.** A broken user script prevents the VM from reaching "ready" state. This is the correct default — silent partial setup is worse than a visible failure — but users must understand that their script is part of the bootstrap critical path.
- **No runtime updates.** The user script is baked into user-data at launch time. Changing `user-bootstrap.sh` locally has no effect on running VMs. The new version takes effect on the next `mint up` or `mint recreate`.
- **Transparent delivery.** The script travels inline in user-data, visible in the EC2 console's "View user data" panel and in cloud-init logs. This aids debugging when a user script fails.

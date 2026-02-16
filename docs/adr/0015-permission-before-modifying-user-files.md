# ADR-0015: Permission Before Modifying Non-Mint Files

## Status
Accepted

## Context
Mint manages its own configuration at `~/.config/mint/config.toml` and can freely read and write files in that directory. However, some commands modify files outside Mint's config directory — most notably `mint ssh-config`, which writes to `~/.ssh/config`.

Silently modifying files the user considers "theirs" violates transparency and risks clobbering manual configurations. Tools that edit dotfiles without warning erode trust.

At the same time, `mint ssh-config` is on the critical path of the primary workflow. Requiring the user to run it manually after every `mint up` is unnecessary friction. The tool knows it needs to happen — it should just ask first.

## Decision
Mint asks for explicit confirmation before creating or modifying any file outside `~/.config/mint/`. On first confirmation, Mint records the approval in its config so it does not re-prompt for that file path on subsequent runs.

Specifically:
- `mint up` auto-runs `ssh-config` generation at the end of a successful provision or start, but prompts the user before writing to `~/.ssh/config` the first time.
- After the user approves, `~/.config/mint/config.toml` records `ssh_config_approved = true` and future `mint up` runs update `~/.ssh/config` silently.
- `mint ssh-config` run directly also respects this — prompts on first write, remembers approval.

## Consequences
- **No surprise file modifications.** Users always know before Mint touches files outside its own directory.
- **Frictionless after first run.** The prompt is one-time. Subsequent `mint up` invocations update SSH config automatically.
- **Extensible pattern.** If future commands need to modify other user files, the same prompt-and-remember pattern applies.
- **`mint up` becomes a single command for the full workflow.** Provision, bootstrap, and SSH config in one step.

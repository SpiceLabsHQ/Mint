# ADR-0007: SSH Keys with BYOK Support

## Status
Accepted

## Context
Mint users connect to VMs via SSH and mosh, which require key-based authentication. Users have diverse SSH setups:

- Some have no existing SSH keys and need Mint to generate one.
- Some use SSH agents (macOS Keychain, `ssh-agent`) with existing keys.
- Some use hardware-backed keys via 1Password SSH agent, Yubikey, or similar.
- Users connect from multiple clients: VS Code Remote-SSH, macOS Terminal, and Termius on iPad.

A single key management approach does not fit all these cases. Generated keys must be importable into Termius. Agent-backed keys must work with VS Code Remote-SSH's `SSH_AUTH_SOCK` forwarding.

## Decision
`mint init` generates an Ed25519 key pair by default and registers the public key with AWS. The private key is stored locally for direct SSH use and for manual import into Termius.

A `--public-key` flag on `mint init` allows users to provide their own public key instead (Bring Your Own Key). When BYOK is used:

- Mint registers the provided public key with AWS instead of generating one.
- `mint ssh-config` omits the `IdentityFile` directive so the user's SSH agent handles authentication.
- The user's agent (`SSH_AUTH_SOCK`, 1Password agent, Yubikey) provides the private key at connection time.

## Consequences
- **Works out of the box.** Users with no SSH setup run `mint init` and get a working key pair.
- **Respects existing workflows.** Users with SSH agents, hardware keys, or organizational key management bring their own keys without Mint overriding their setup.
- **Termius compatibility.** Generated keys produce a private key file that can be imported into Termius for iPad access.
- **VS Code compatibility.** BYOK users with `SSH_AUTH_SOCK` get seamless VS Code Remote-SSH connections. Generated key users specify `IdentityFile` in their SSH config.
- **No key rotation.** Mint does not manage key lifecycle. Users who need key rotation handle it outside Mint.

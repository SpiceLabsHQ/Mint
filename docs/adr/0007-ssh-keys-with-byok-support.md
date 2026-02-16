# ADR-0007: EC2 Instance Connect as Primary SSH with Key Add Escape Hatch

## Status
Accepted (supersedes original BYOK key pair design)

## Context
SSH key management is the most complex user-facing aspect of a remote dev environment tool. Users have wildly different setups: some have no SSH keys, some use macOS Keychain, some use 1Password or Bitwarden SSH agents, some use Yubikeys, some use Termius on iPad with its own key storage. Any key management design that Mint owns becomes a matrix of edge cases across clients and key storage backends.

EC2 Instance Connect eliminates the problem entirely for the primary workflow. It pushes an ephemeral public key to the instance via the AWS API (valid 60 seconds), then opens a standard SSH connection. The user's existing AWS credentials — which Mint already requires — become their SSH credentials. No keys to generate, store, rotate, or synchronize across devices.

The gap is clients that cannot use EC2 Instance Connect: Termius on iPad, CI runners, and third-party SSH tools that need a traditional key in `authorized_keys`.

## Decision
EC2 Instance Connect is the primary SSH mechanism. Mint does not generate, store, or manage SSH key pairs.

- `mint ssh` and `mint mosh` use EC2 Instance Connect under the hood.
- `mint ssh-config` writes a `ProxyCommand` entry routing through Instance Connect, enabling VS Code Remote-SSH and other standard SSH clients.
- No AWS key pair resource is created. Instances launch without a key pair.
- The EC2 Instance Connect agent is installed during VM bootstrap.

For clients that cannot use Instance Connect, `mint key add <public-key>` appends a public key to the VM's `~/.ssh/authorized_keys` via an Instance Connect session. This is an imperative action on the VM — the key is not tracked by Mint or stored in tags.

## Consequences
- **Zero key management.** No keys to generate, store, rotate, import into Termius, or worry about in password managers. AWS credentials are the only credential.
- **One fewer AWS resource.** No key pair to create, tag, or clean up on destroy.
- **VS Code works via ProxyCommand.** `mint ssh-config` generates the right SSH config entry. No `IdentityFile` needed.
- **Escape hatch for non-Instance-Connect clients.** `mint key add` lets users register keys for Termius, CI, or any tool that needs direct SSH access. The operation itself uses Instance Connect, so no bootstrap key is needed.
- **Requires AWS CLI on the connecting machine.** Users already need this for Mint, so no new dependency.
- **Requires `ec2-instance-connect:SendSSHPublicKey` IAM permission.** Included in PowerUser access.
- **iPad/Termius workflow requires an extra step.** Users must `mint key add` a key that Termius can use. This is a one-time step per VM, not a blocking limitation.
- **Ephemeral key latency.** Each connection makes an API call to push the ephemeral key. Adds ~100-200ms per connection. Acceptable.

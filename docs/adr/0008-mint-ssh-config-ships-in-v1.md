# ADR-0008: mint ssh-config Ships in v1

## Status
Accepted

## Context
The primary Mint workflow is VS Code Remote-SSH connecting to the EC2 instance. This requires an entry in `~/.ssh/config` with the host alias, IP address, user, and (optionally) identity file path. The original spec listed `mint ssh-config` as a future consideration, expecting users to manually configure SSH.

Manual SSH config is the biggest UX gap in the v1 workflow. Every `mint up` or IP change requires the user to find the Elastic IP and edit their SSH config by hand. This is error-prone and creates friction on the critical path of the primary use case.

## Decision
Promote `mint ssh-config` from "future consideration" to v1. The command auto-generates `~/.ssh/config` entries by discovering Elastic IPs via the AWS CLI and Mint's tag-based resource lookup.

The command writes `Host mint-<vm-name>` blocks containing:
- `HostName` set to the VM's Elastic IP
- `User` set to `ubuntu`
- `ProxyCommand` routing through EC2 Instance Connect (see ADR-0007), enabling keyless SSH access

## Consequences
- **Closes the primary workflow gap.** After `mint up` and `mint ssh-config`, the user can immediately `code --remote ssh-remote+mint-default /path` or select the host in VS Code's Remote-SSH picker.
- **Reproducible config.** Re-running `mint ssh-config` after adding or removing VMs updates the config to match current state. No manual editing.
- **SSH config ownership.** Mint writes to the user's `~/.ssh/config`. It must handle this carefully -- using a managed block with markers or a separate include file to avoid clobbering user-managed entries.
- **Scope increase.** Adding this command to v1 increases the v1 surface area, but the implementation is straightforward (tag query + file generation) and the UX payoff is high.

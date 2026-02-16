# ADR-0013: Owner Identity Derived at Runtime

## Status
Accepted

## Context
Mint tags all resources with `mint:owner` for resource discovery and cost attribution in a shared AWS account. The original spec stored the owner identifier in local config (`~/.config/mint/config.toml`). This creates problems:

1. A stored owner can become stale if the user switches AWS profiles or credentials.
2. It requires an explicit setup step during `mint init` that could be automated.
3. It's one more piece of config to manage and potentially misconfigure.

AWS provides `aws sts get-caller-identity` which returns the caller's ARN for any authentication method (IAM user, SSO, assumed role, federated). This is fast, always current, and requires no additional permissions.

## Decision
Derive the owner identity at runtime from `aws sts get-caller-identity` on every command invocation. Do not store the owner in config.

The ARN's trailing identifier is normalized to a friendly name:
- Strip `@domain` from SSO email addresses
- Lowercase
- Replace non-alphanumeric characters with `-`

Two tags capture identity:
- `mint:owner` — the normalized friendly name, used for resource discovery and filtering
- `mint:owner-arn` — the full caller ARN, used for auditability and disambiguation if friendly names collide

If a user authenticates with a different AWS identity, they will not find resources created under the previous identity. This is intentional — different identity, different owner.

## Consequences
- **No config to maintain.** Owner is always correct for the current credentials.
- **No stale state.** Switching AWS profiles naturally scopes resource visibility to the new identity.
- **Auditability.** The full ARN tag (`mint:owner-arn`) enables precise identification even when friendly names collide (e.g., two users both normalizing to `ryan`).
- **Extra API call.** Every Mint command calls `sts get-caller-identity`. This adds ~100ms latency. Acceptable for a CLI tool that already makes EC2 API calls.
- **Collision risk.** Two users with ARNs normalizing to the same friendly name (e.g., `ryan@company.com` and `ryan@other.com`) would share a `mint:owner` value. The `mint:owner-arn` tag disambiguates, but resource filtering would show both users' resources. Acceptable for a trusted-team tool; rare in practice.

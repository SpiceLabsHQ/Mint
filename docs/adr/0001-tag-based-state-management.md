# ADR-0001: Tag-Based State Management

## Status
Accepted

## Context
CLI tools that manage cloud resources need to track which resources they own. The common approaches are local state files (Terraform-style), remote state in a database/S3, or resource tagging. Local state files introduce the "who has the state file" problem in shared accounts, state locking complexity, and state drift when resources are modified outside the tool. Remote state adds infrastructure dependencies (DynamoDB, S3 bucket) and operational burden disproportionate to Mint's scope as a dev tool.

Mint operates in a shared AWS account where multiple developers provision independent VMs. Each developer runs Mint from their own machine.

## Decision
Mint discovers all resources exclusively via AWS resource tags. There is no local state file and no remote state store. Every resource Mint creates receives these tags:

| Tag | Purpose |
|-----|---------|
| `mint=true` | Primary filter for all Mint-managed resources |
| `mint:component` | Resource type (instance, volume, security-group, key-pair, elastic-ip) |
| `mint:vm` | VM name this resource belongs to |
| `mint:owner` | IAM username or configured owner identifier |
| `Name` | `mint/<owner>/<vm-name>` for AWS Console display |

Multi-user isolation is achieved by filtering on `mint:owner`. Billing review filters Cost Explorer on `mint=true` and groups by `mint:vm` or `mint:owner`.

## Consequences
- **No state drift.** Tags live on the resources themselves. There is no secondary record that can diverge from reality.
- **No state locking.** Multiple Mint invocations cannot corrupt a shared state file because there is none.
- **No "where is the state" problem.** Any developer can run Mint from any machine and discover their resources.
- **Billing visibility for free.** Cost Explorer tag filters give per-user and per-VM cost breakdowns without additional tooling.
- **Slower resource discovery.** Every command must query AWS APIs with tag filters instead of reading a local file. Acceptable for a CLI managing a handful of resources.
- **Tag limits.** AWS allows 50 tags per resource. Mint uses 5, leaving headroom but establishing a dependency on the tagging system.
- **Tag-based isolation is not access control.** Any PowerUser in the account can modify another user's resources. See ADR-0005.

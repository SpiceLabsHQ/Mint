# ADR-0005: Trusted-Team Security Model

## Status
Accepted

## Context
Mint uses `mint:owner` tags to associate resources with individual developers (ADR-0001). However, AWS tags are metadata, not access control. Any IAM principal with `ec2:*` permissions (including all PowerUsers in the shared account) can read, modify, or delete resources tagged with another user's `mint:owner` value.

This was flagged during review: tag-based isolation provides organizational convention, not a security boundary. A developer could accidentally or intentionally stop, terminate, or reconfigure another developer's VM.

## Decision
Accept the trusted-team model explicitly. `mint:owner` tags are a convention for filtering and billing, not access control. This is documented, not hidden.

Mint operates under the assumption that all users in the shared AWS account are trusted colleagues. The CLI filters on `mint:owner` so users only see and operate on their own resources by default, but this is a UX convenience, not a security enforcement.

**Escalation path**: If the threat model changes (e.g., untrusted users join the account, compliance requirements emerge), IAM permission boundaries with `aws:ResourceTag/mint:owner` conditions can enforce that users can only modify resources tagged with their own identity. This requires admin IAM changes, not Mint code changes.

## Consequences
- **Simple IAM.** No per-user IAM policies, no resource-level permission boundaries. All Mint users share the same PowerUser role.
- **Accident risk.** A user could run `aws ec2 terminate-instances` against another user's VM. Mint's CLI prevents this by filtering on `mint:owner`, but raw AWS API/CLI access has no guardrails.
- **Documented assumption.** The trust model is explicit in Mint's documentation. Teams adopting Mint understand the boundary before onboarding.
- **Incremental hardening available.** Moving to IAM-enforced isolation is an operational change (IAM policies), not an architectural one. No Mint code changes required.

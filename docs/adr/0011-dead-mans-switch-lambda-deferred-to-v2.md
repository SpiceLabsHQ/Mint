# ADR-0011: Dead-Man's Switch Lambda Deferred to v2

## Status
Accepted

## Context
Mint VMs auto-stop themselves via a systemd timer that checks for activity every 5 minutes (SSH sessions, tmux clients, running Claude processes). When idle, the instance uses its IAM role to call `ec2:StopInstances` on itself. This has failure modes:

- The systemd timer service could crash or be disabled.
- The IAM `StopInstances` call could fail (API error, credential issue).
- The instance could enter a state where the idle check never triggers (kernel panic, hung process).

In any of these cases, the VM runs indefinitely with no notification, accumulating cost.

A Lambda-based watchdog was proposed: a CloudWatch-scheduled Lambda that periodically checks all `mint=true` instances for a heartbeat tag (updated by the systemd timer). Instances with stale heartbeats get force-stopped. This catches all failure modes above.

## Decision
Defer the Lambda dead-man's switch to v2. Accept the risk that in v1, a failed auto-stop mechanism results in an indefinitely running VM with no external notification.

The v2 implementation will be an admin-deployed CloudFormation template containing the Lambda function, CloudWatch schedule, and IAM role. This keeps the admin setup minimal for v1 (just the instance role and profile).

## Consequences
- **Cost risk.** A stuck VM runs at ~$0.19/hour (m6i.xlarge). A weekend of undetected running costs ~$9. A month costs ~$140. This is annoying but not catastrophic for individual developers.
- **No external alerting.** In v1, there is no mechanism to notify users that their VM failed to auto-stop. Users must check `mint list` or their AWS bill.
- **Simpler admin setup.** v1 admin setup is a single IAM role and instance profile. No Lambda, no CloudWatch rules, no additional IAM roles for the watchdog.
- **Clear upgrade path.** The v2 Lambda watchdog is additive. It reads heartbeat tags and stops stale instances. No changes to Mint CLI or the existing systemd timer are required. Note: The heartbeat tag described above is a v2 design element and does not appear in the v1 tag table (ADR-0001).
- **Mitigation available.** Users can set up their own CloudWatch billing alarms as a partial substitute.

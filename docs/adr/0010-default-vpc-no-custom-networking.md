# ADR-0010: Default VPC, No Custom Networking

## Status
Accepted

## Context
AWS accounts have a default VPC in each region with public subnets, an internet gateway, and standard routing. Production workloads commonly replace this with custom VPCs featuring private subnets, NAT gateways, bastion hosts, or SSM Session Manager for access.

For Mint, the networking requirements are minimal: the EC2 instance needs a public IP for direct SSH/mosh access and outbound internet for package installation and Docker image pulls. Options considered:

1. **Custom VPC** with private subnets and a bastion or SSM Session Manager. Secure but adds significant provisioning complexity, ongoing cost (NAT gateway: ~$30/month), and operational overhead for individual dev environments.
2. **Default VPC** with Elastic IP and security group. Simple, no additional infrastructure, direct connectivity.

## Decision
Use the default VPC. No custom VPC, no bastion host, no NAT gateway, no SSM Session Manager.

`mint init` validates that the default VPC exists and has at least one public subnet in the configured region. If the default VPC was deleted (some organizations do this), `mint init` exits with an error and guidance.

Network security is handled by non-standard ports with key-only authentication (ADR-0016) and EC2 Instance Connect with ephemeral keys (ADR-0007).

## Consequences
- **Zero networking setup.** No VPC, subnet, route table, NAT gateway, or bastion to provision or pay for.
- **Direct connectivity.** SSH and mosh connect directly to the Elastic IP. No bastion hop, no SSM plugin, no port forwarding.
- **Public exposure.** The VM has a public IP. Mitigated by non-standard SSH port and key-only authentication (ADR-0016), but the instance is on the public internet, unlike a private-subnet approach.
- **Default VPC dependency.** If the default VPC is deleted or misconfigured, Mint cannot provision. This is detected at `mint init` with a clear error.
- **Not suitable for regulated environments.** Organizations requiring private subnets, VPC flow logs, or network-level compliance controls cannot use Mint without modification.

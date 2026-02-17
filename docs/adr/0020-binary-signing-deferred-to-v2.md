# ADR-0020: Binary Signing Deferred to v2

## Status
Accepted

## Context
Mint's `mint update` command performs atomic self-update: download new binary, verify checksum, replace via `mv`. The bootstrap script's SHA256 hash is also pinned in the Go binary (ADR-0009). Both mechanisms protect against corrupted downloads but neither protects against a compromised distribution channel — checksums served from the same origin as the binary prove nothing against a supply-chain attack.

The squadron review identified this as a security gap. The panel consensus was that cryptographic signing is the correct solution, with two proposed approaches:
- **minisign** (v1): Single static binary, one keypair, public key embedded in the Go binary at compile time. Minimal build complexity.
- **cosign with keyless signing** (v2): GitHub Actions OIDC identity, Rekor transparency log integration. Full provenance chain.

## Decision

Defer binary signing to v2. Ship v1 with checksum verification only.

### Rationale

Mint is a trusted-team tool (ADR-0005) distributed within a known team in a shared AWS account. The supply-chain threat model for v1 is:
- Binary distribution via GitHub Releases (authenticated download over TLS)
- Users are known team members who install from a known repository
- The attack surface is a compromised GitHub release, not a public package registry

Checksum verification catches download corruption and CDN cache poisoning. It does not catch a compromised GitHub release — but that attack requires GitHub account compromise, which is outside the trusted-team threat model.

### v2 Plan

When Mint has CI infrastructure and a broader user base, implement signing:
1. **minisign** as the initial signing mechanism: single keypair, public key embedded in the current binary, signature as a separate release artifact
2. **cosign with keyless signing** as the long-term target: GitHub Actions OIDC identity eliminates key management, Rekor transparency log provides auditability
3. CI must sign every release artifact and the bootstrap script
4. `mint update` verifies the signature before replacing the binary

### Bootstrap Script Hash

The bootstrap script hash pinning (ADR-0009) provides stronger guarantees than binary checksums because the hash is embedded at compile time via `go:generate` — it cannot be tampered with without rebuilding the binary. This mechanism is unchanged by this decision and remains the primary integrity check for the most sensitive artifact (the script that runs as root on new instances).

## Consequences
- **Accepted supply-chain risk.** A compromised GitHub release could distribute a malicious `mint` binary. This is mitigated by the trusted-team distribution model and TLS-authenticated downloads.
- **Clear v2 path.** The signing implementation is specified (minisign → cosign) so it can be prioritized when the threat model changes (public distribution, untrusted users, compliance requirements).
- **No build complexity in v1.** Signing infrastructure (key management, CI signing step, signature verification) is deferred, keeping the v1 build pipeline simple.
- **Bootstrap script is protected.** Hash pinning at compile time (ADR-0009) provides integrity verification for the bootstrap script independent of binary signing.

# ADR-0023: SSO Re-authentication Error Classification

## Status
Accepted

## Context
Mint uses `aws_profile` in `~/.config/mint/config.toml` to let users select a named AWS profile (ADR-0012). When that profile is backed by AWS SSO, the SSO token has a finite lifetime — typically 8 hours. After expiry, every AWS API call fails immediately, before any VM discovery or provisioning work begins.

The AWS SDK does not surface this as a typed SSO error at the call site. It wraps the token expiry inside a `NoCredentialProviders` chain that looks identical to "AWS is not configured at all." Mint's `PersistentPreRunE` already detects this class of failure via `isCredentialError` and replaces the raw SDK chain with a friendlier message. But the friendly message said "AWS credentials are not configured" — accurate for the unconfigured case, but wrong and unhelpful for the SSO-expired case, where credentials *are* configured and the fix is a single command: `aws sso login --profile <profile>`.

The distinction matters because the two errors have different remediation paths:

| Situation | Message shown | Required action |
|-----------|---------------|-----------------|
| No AWS credentials at all | "AWS credentials are not configured…" | Run `aws configure` or set env vars |
| SSO token expired | (was: same generic message) | Run `aws sso login --profile <profile>` |

A developer with an expired SSO token who sees "credentials are not configured" will waste time reconfiguring a working AWS setup rather than simply refreshing the token.

## Decision

Add keyword-based SSO re-auth detection to `cmd/awsdeps.go`, parallel to the existing `isCredentialError` approach.

### isSSOReAuthError

```go
// ssoReAuthKeywords are substrings found in AWS SDK SSO token expiry errors.
// When any of these appear the user must re-authenticate via `aws sso login`.
var ssoReAuthKeywords = []string{
	"token has expired",
	"InvalidClientId",
	"failed to refresh cached credentials",
	"SSOProviderInvalidToken",
	"Error loading SSO Token",
}

// isSSOReAuthError reports whether err looks like an SSO token expiry that
// requires the user to run `aws sso login`. Returns false for nil.
func isSSOReAuthError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, kw := range ssoReAuthKeywords {
		if strings.Contains(msg, kw) {
			return true
		}
	}
	return false
}
```

### credentialErrMessage

```go
// credentialErrMessage returns an actionable error message for AWS credential
// failures. When the error is an SSO token expiry and a profile is known, it
// directs the user to run `aws sso login --profile <profile>`. Otherwise it
// returns the generic credential setup message.
func credentialErrMessage(err error, profile string) string {
	if isSSOReAuthError(err) && profile != "" {
		return fmt.Sprintf("SSO token expired — run: aws sso login --profile %s", profile)
	}
	return `AWS credentials unavailable — run "aws configure", set AWS_PROFILE, or use --profile`
}
```

Call sites in `PersistentPreRunE`, `cmd/doctor.go`, and any future command that handles credential errors must call `credentialErrMessage(err, profile)` rather than hardcoding credential error strings. The `profile` argument comes from the resolved config value (`mintConfig.AWSProfile`), which is empty when no profile is set.

### Ordering invariant

`isSSOReAuthError` must always be evaluated before `isCredentialError`. SSO token expiry errors contain substrings like "failed to refresh cached credentials" that also match `credentialErrorKeywords`. Checking SSO first ensures the more specific message wins.

### Keyword rationale

The five keywords target the literal strings present in `smithy-go` and the AWS SDK v2 credential provider chain as of 2026: `"token has expired"` (generic expiry text), `"InvalidClientId"` and `"SSOProviderInvalidToken"` (SDK-level SSO error codes), `"failed to refresh cached credentials"` (provider chain message), and `"Error loading SSO Token"` (token loader message). No `strings.ToLower` normalization is applied — the existing `isCredentialError` approach does not normalize, and consistency between the two functions is more important than case-folding.

## Alternatives Rejected

**`errors.As` / smithy `OperationError` unwrapping**: The AWS SDK v2 wraps errors through `smithy-go`'s operation error chain. In principle, one could unwrap to the innermost error and type-assert to a specific SSO error type. In practice this is brittle: the wrapping depth varies by SDK version, the SSO provider's error type is not part of the public API contract, and the unwrapping code requires importing `smithy-go` internals. Keyword matching on `err.Error()` is the same technique already used by `isCredentialError` — it survives SDK minor version bumps without code changes.

**Separate `mint auth check` subcommand**: A dedicated subcommand could explicitly probe each credential provider and report its status. Rejected because: (a) this is a complete UX surface change for what is fundamentally a message improvement; (b) it requires users to know to run a check command before discovering the problem, rather than getting a good error on the command they actually wanted to run; (c) implementation complexity is disproportionate to the problem.

**Case-insensitive matching via `strings.ToLower`**: Normalizing both the keyword and the error message to lowercase would allow single entries without case variants. Rejected because the existing `isCredentialError` does not normalize, and diverging from that pattern for `isSSOReAuthError` would create a subtle inconsistency. Both functions should behave the same way.

## Consequences

- **Targeted remediation.** Users with an expired SSO token see `aws sso login --profile <profile>` immediately, without needing to diagnose a generic credential error.
- **Consistent error handling.** All credential-related error messages flow through `credentialErrMessage`. New commands get correct SSO vs. generic behavior by calling the shared function — no per-command logic required.
- **Ordering dependency.** `isSSOReAuthError` must precede `isCredentialError` at every call site. This is a code convention enforced by code review and documented in the function's godoc. There is no compile-time enforcement.
- **Keyword maintenance.** If the AWS SDK changes its error strings, the keywords may need updating. This is the same maintenance model as `credentialErrorKeywords` and is an accepted trade-off for the simplicity of the approach.
- **Profile-free SSO case.** When `aws_profile` is empty but the error matches SSO keywords (e.g., SSO configured as the default profile), `credentialErrMessage` does not produce a special SSO message. The `isSSOReAuthError(err) && profile != ""` condition is false, so it falls through to the generic "AWS credentials unavailable" message. Users relying on the default SSO profile will see the generic message and should run `aws sso login` manually.

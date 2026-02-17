// Package identity derives the owner identity from AWS STS caller identity.
// The owner is used for tagging AWS resources (mint:owner, mint:owner-arn)
// and is derived at runtime per ADR-0013.
package identity

import (
	"fmt"
	"regexp"
	"strings"
)

// nonAlphanumeric matches any character that is not a lowercase letter or digit.
var nonAlphanumeric = regexp.MustCompile(`[^a-z0-9]+`)

// NormalizeARN extracts the trailing identifier from an AWS ARN and normalizes
// it to a friendly owner name. The normalization rules (from ADR-0013) are:
//   - Extract the last path segment of the ARN resource
//   - Strip @domain from email addresses
//   - Lowercase
//   - Replace runs of non-alphanumeric characters with a single hyphen
//   - Trim leading and trailing hyphens
func NormalizeARN(arn string) (string, error) {
	if arn == "" {
		return "", fmt.Errorf("empty ARN")
	}

	// AWS ARNs have the format: arn:partition:service:region:account:resource
	// The resource part may contain slashes (e.g., user/ryan, assumed-role/Role/session).
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) < 6 {
		return "", fmt.Errorf("malformed ARN: expected at least 6 colon-separated fields, got %d", len(parts))
	}

	resource := parts[5]
	if resource == "" {
		return "", fmt.Errorf("malformed ARN: empty resource field")
	}

	// Extract the trailing identifier (last segment after /).
	// For "user/ryan" -> "ryan", for "assumed-role/Role/session" -> "session",
	// for "root" -> "root".
	segments := strings.Split(resource, "/")
	identifier := segments[len(segments)-1]

	if identifier == "" {
		return "", fmt.Errorf("malformed ARN: empty trailing identifier")
	}

	// Strip @domain from email addresses (SSO identities).
	if idx := strings.Index(identifier, "@"); idx > 0 {
		identifier = identifier[:idx]
	}

	// Lowercase.
	identifier = strings.ToLower(identifier)

	// Replace runs of non-alphanumeric characters with a single hyphen.
	identifier = nonAlphanumeric.ReplaceAllString(identifier, "-")

	// Trim leading and trailing hyphens.
	identifier = strings.Trim(identifier, "-")

	if identifier == "" {
		return "", fmt.Errorf("ARN normalized to empty string: %s", arn)
	}

	return identifier, nil
}

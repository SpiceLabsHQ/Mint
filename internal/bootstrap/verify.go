// Package bootstrap provides integrity verification for the EC2 user-data
// bootstrap script. The script's SHA256 hash is embedded at compile time via
// go:generate and verified before the script is sent to EC2 (ADR-0009).
package bootstrap

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

//go:generate go run hash_gen.go

// Verify checks that the given script content matches the SHA-256 hash
// embedded at compile time via go:generate.
//
// IMPORTANT: This hash verifies the committed template, NOT the rendered
// script after variable substitution. If substitution variables ever
// reference external data (AMI IDs, dynamic package versions), the
// integrity guarantee no longer holds. Do not extend substitution scope
// without re-evaluating this boundary.
//
// If the hash does not match, mint up must abort immediately. The script must
// never be sent to EC2 with a mismatched hash (ADR-0009).
func Verify(content []byte) error {
	if len(content) == 0 {
		return fmt.Errorf("bootstrap script is empty")
	}

	actual := sha256.Sum256(content)
	actualHex := hex.EncodeToString(actual[:])

	if actualHex != ScriptSHA256 {
		return fmt.Errorf(
			"bootstrap script hash mismatch: expected %s, got %s — "+
				"update your mint binary or re-run go generate",
			ScriptSHA256, actualHex,
		)
	}

	return nil
}

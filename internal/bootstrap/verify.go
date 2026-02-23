// Package bootstrap provides integrity verification for the EC2 bootstrap
// script and template rendering for the bootstrap stub. The real bootstrap.sh
// SHA256 hash is embedded at compile time (via go generate) so that the stub
// can pass a pinned hash to EC2; the stub fetches and re-verifies at runtime
// (ADR-0009).
package bootstrap

import "fmt"

//go:generate go run hash_gen.go

// Verify is a compile-time sanity check that go generate has been run and
// that ScriptSHA256 is non-empty. The content parameter is accepted for API
// compatibility but is not hashed here — the stub no longer embeds the full
// script, so there is nothing to hash at provision time.
//
// The real integrity check happens on the EC2 instance: the stub script
// downloads bootstrap.sh, verifies its sha256sum against ScriptSHA256, and
// aborts if they differ.
//
// If ScriptSHA256 is empty (go generate was not run), mint up must abort
// before sending user-data to EC2.
func Verify(content []byte) error {
	if ScriptSHA256 == "" {
		return fmt.Errorf("ScriptSHA256 is empty — run go generate ./internal/bootstrap/...")
	}
	return nil
}

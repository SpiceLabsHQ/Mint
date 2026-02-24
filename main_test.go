package main

import (
	"testing"

	"github.com/nicholasgasior/mint/internal/bootstrap"
)

func TestEmbeddedBootstrapStubIsNonEmpty(t *testing.T) {
	if len(bootstrapStub) == 0 {
		t.Fatal("embedded bootstrap stub is empty; go:embed may not be working")
	}
}

// TestEmbeddedBootstrapStubPassesVerify checks that the compile-time
// ScriptSHA256 constant is non-empty (i.e., go generate has been run).
// Verify no longer hashes the embedded content — the stub is a template,
// not the full script — so this is purely a sanity check on the constant.
func TestEmbeddedBootstrapStubPassesVerify(t *testing.T) {
	if err := bootstrap.Verify(bootstrapStub); err != nil {
		t.Fatalf("bootstrap.Verify sanity check failed: %v", err)
	}
}

func TestEmbeddedBootstrapStubHasShebang(t *testing.T) {
	if len(bootstrapStub) < 2 || string(bootstrapStub[:2]) != "#!" {
		t.Fatal("embedded bootstrap stub does not start with a shebang (#!)")
	}
}

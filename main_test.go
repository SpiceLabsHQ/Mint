package main

import (
	"testing"

	"github.com/nicholasgasior/mint/internal/bootstrap"
)

func TestEmbeddedBootstrapScriptIsNonEmpty(t *testing.T) {
	if len(bootstrapScript) == 0 {
		t.Fatal("embedded bootstrap script is empty; go:embed may not be working")
	}
}

func TestEmbeddedBootstrapScriptPassesVerify(t *testing.T) {
	if err := bootstrap.Verify(bootstrapScript); err != nil {
		t.Fatalf("embedded bootstrap script failed verification: %v", err)
	}
}

func TestEmbeddedBootstrapScriptHasShebang(t *testing.T) {
	if len(bootstrapScript) < 2 || string(bootstrapScript[:2]) != "#!" {
		t.Fatal("embedded bootstrap script does not start with a shebang (#!)")
	}
}

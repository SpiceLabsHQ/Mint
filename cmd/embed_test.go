package cmd

import (
	"testing"
)

func TestExecuteWithBootstrapScriptStoresScript(t *testing.T) {
	// Reset the package-level variable after this test.
	original := embeddedBootstrapScript
	defer func() { embeddedBootstrapScript = original }()

	script := []byte("#!/bin/bash\necho hello")
	embeddedBootstrapScript = nil

	// We can't easily call ExecuteWithBootstrapScript because it runs the
	// full CLI, but we can verify the storage mechanism directly.
	SetBootstrapScript(script)

	if embeddedBootstrapScript == nil {
		t.Fatal("SetBootstrapScript did not store the script")
	}
	if string(embeddedBootstrapScript) != string(script) {
		t.Errorf("stored script = %q, want %q", embeddedBootstrapScript, script)
	}
}

func TestGetBootstrapScriptReturnsStoredScript(t *testing.T) {
	original := embeddedBootstrapScript
	defer func() { embeddedBootstrapScript = original }()

	script := []byte("#!/bin/bash\necho test")
	embeddedBootstrapScript = script

	got := GetBootstrapScript()
	if string(got) != string(script) {
		t.Errorf("GetBootstrapScript() = %q, want %q", got, script)
	}
}

func TestGetBootstrapScriptReturnsNilWhenUnset(t *testing.T) {
	original := embeddedBootstrapScript
	defer func() { embeddedBootstrapScript = original }()

	embeddedBootstrapScript = nil

	got := GetBootstrapScript()
	if got != nil {
		t.Errorf("GetBootstrapScript() = %q, want nil", got)
	}
}

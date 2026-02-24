package bootstrap

import (
	"strings"
	"testing"
)

func TestSetStubAndGetStub(t *testing.T) {
	original := embeddedStub
	defer func() { embeddedStub = original }()

	data := []byte("#!/bin/bash\necho hello\n")
	SetStub(data)

	got := GetStub()
	if string(got) != string(data) {
		t.Errorf("GetStub() = %q, want %q", got, data)
	}
}

func TestRenderStubReturnsErrorWhenNotLoaded(t *testing.T) {
	original := embeddedStub
	defer func() { embeddedStub = original }()

	embeddedStub = nil

	_, err := RenderStub("sha", "url", "efs-id", "/dev/xvdf", "default", "60")
	if err == nil {
		t.Fatal("expected error when stub template not loaded, got nil")
	}
	if !strings.Contains(err.Error(), "not loaded") {
		t.Errorf("error should mention 'not loaded', got: %v", err)
	}
}

func TestRenderStubSubstitutesAllPlaceholders(t *testing.T) {
	original := embeddedStub
	defer func() { embeddedStub = original }()

	template := `#!/bin/bash
export MINT_EFS_ID="__MINT_EFS_ID__"
export MINT_PROJECT_DEV="__MINT_PROJECT_DEV__"
export MINT_VM_NAME="__MINT_VM_NAME__"
export MINT_IDLE_TIMEOUT="__MINT_IDLE_TIMEOUT__"
_URL="__MINT_BOOTSTRAP_URL__"
_SHA="__MINT_BOOTSTRAP_SHA256__"
`
	embeddedStub = []byte(template)

	rendered, err := RenderStub(
		"abc123sha",
		"https://example.com/bootstrap.sh",
		"fs-0abc123",
		"/dev/xvdf",
		"myvm",
		"120",
	)
	if err != nil {
		t.Fatalf("RenderStub returned unexpected error: %v", err)
	}

	result := string(rendered)

	checks := []struct {
		desc  string
		token string
		value string
	}{
		{"sha256", "__MINT_BOOTSTRAP_SHA256__", "abc123sha"},
		{"url", "__MINT_BOOTSTRAP_URL__", "https://example.com/bootstrap.sh"},
		{"efs id", "__MINT_EFS_ID__", "fs-0abc123"},
		{"project dev", "__MINT_PROJECT_DEV__", "/dev/xvdf"},
		{"vm name", "__MINT_VM_NAME__", "myvm"},
		{"idle timeout", "__MINT_IDLE_TIMEOUT__", "120"},
	}

	for _, c := range checks {
		if strings.Contains(result, c.token) {
			t.Errorf("RenderStub left placeholder %q unsubstituted", c.token)
		}
		if !strings.Contains(result, c.value) {
			t.Errorf("RenderStub missing value %q for %s", c.value, c.desc)
		}
	}
}

func TestScriptURL(t *testing.T) {
	tests := []struct {
		version string
		want    string
	}{
		{"", "https://raw.githubusercontent.com/SpiceLabsHQ/Mint/develop/scripts/bootstrap.sh"},
		{"dev", "https://raw.githubusercontent.com/SpiceLabsHQ/Mint/develop/scripts/bootstrap.sh"},
		{"1.2.3", "https://raw.githubusercontent.com/SpiceLabsHQ/Mint/v1.2.3/scripts/bootstrap.sh"},
	}
	for _, tc := range tests {
		got := ScriptURL(tc.version)
		if got != tc.want {
			t.Errorf("ScriptURL(%q) = %q; want %q", tc.version, got, tc.want)
		}
	}
}

func TestRenderStubNoRemainingPlaceholders(t *testing.T) {
	original := embeddedStub
	defer func() { embeddedStub = original }()

	// Use a template containing all six __PLACEHOLDER__ tokens defined in
	// scripts/bootstrap-stub.sh to verify none survive substitution.
	template := `#!/bin/bash
export MINT_EFS_ID="__MINT_EFS_ID__"
export MINT_PROJECT_DEV="__MINT_PROJECT_DEV__"
export MINT_VM_NAME="__MINT_VM_NAME__"
export MINT_IDLE_TIMEOUT="__MINT_IDLE_TIMEOUT__"
_URL="__MINT_BOOTSTRAP_URL__"
_SHA="__MINT_BOOTSTRAP_SHA256__"
`
	embeddedStub = []byte(template)

	rendered, err := RenderStub("sha", "url", "efs", "dev", "vm", "60")
	if err != nil {
		t.Fatalf("RenderStub error: %v", err)
	}

	if strings.Contains(string(rendered), "__MINT_") {
		t.Errorf("rendered stub still contains unsubstituted __MINT_ placeholder:\n%s", rendered)
	}
}

package bootstrap

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScriptHashMatchesEmbeddedConstant(t *testing.T) {
	// The generated hash constant must match the actual SHA256 of bootstrap.sh.
	scriptPath := filepath.Join("..", "..", "scripts", "bootstrap.sh")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("failed to read bootstrap script: %v", err)
	}

	actual := sha256.Sum256(content)
	actualHex := hex.EncodeToString(actual[:])

	if ScriptSHA256 == "" {
		t.Fatal("ScriptSHA256 constant is empty; run go generate")
	}
	if actualHex != ScriptSHA256 {
		t.Errorf("embedded hash does not match script content\n  embedded: %s\n  actual:   %s", ScriptSHA256, actualHex)
	}
}

func TestVerifyAcceptsValidScript(t *testing.T) {
	scriptPath := filepath.Join("..", "..", "scripts", "bootstrap.sh")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("failed to read bootstrap script: %v", err)
	}

	if err := Verify(content); err != nil {
		t.Errorf("Verify returned error for valid script: %v", err)
	}
}

func TestVerifyRejectsTamperedScript(t *testing.T) {
	tampered := []byte("#!/bin/bash\necho 'this is not the real script'")

	err := Verify(tampered)
	if err == nil {
		t.Fatal("Verify accepted tampered script content; expected error")
	}
	if !strings.Contains(err.Error(), "hash mismatch") {
		t.Errorf("error should mention 'hash mismatch', got: %v", err)
	}
}

func TestVerifyRejectsEmptyContent(t *testing.T) {
	err := Verify([]byte{})
	if err == nil {
		t.Fatal("Verify accepted empty content; expected error")
	}
}

func TestVerifyErrorContainsBothHashes(t *testing.T) {
	tampered := []byte("tampered content")
	err := Verify(tampered)
	if err == nil {
		t.Fatal("expected error for tampered content")
	}

	// Error message should contain the expected hash for diagnostics.
	errMsg := err.Error()
	if !strings.Contains(errMsg, ScriptSHA256) {
		t.Errorf("error should contain expected hash %q, got: %s", ScriptSHA256, errMsg)
	}
}

func TestScriptContent(t *testing.T) {
	// Verify the bootstrap script has expected structural elements.
	scriptPath := filepath.Join("..", "..", "scripts", "bootstrap.sh")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("failed to read bootstrap script: %v", err)
	}

	script := string(content)

	requiredElements := []struct {
		desc    string
		pattern string
	}{
		{"shebang", "#!/bin/bash"},
		{"set -euo pipefail", "set -euo pipefail"},
		{"Docker installation", "docker"},
		{"devcontainer CLI", "devcontainer"},
		{"tmux installation", "tmux"},
		{"mosh installation", "mosh"},
		{"git installation", "git"},
		{"GitHub CLI", "gh"},
		{"Node.js", "node"},
		{"AWS CLI", "aws"},
		{"EC2 Instance Connect", "ec2-instance-connect"},
		{"SSH port 41122", "41122"},
		{"password auth disabled", "PasswordAuthentication no"},
		{"EFS mount", "/mint/user"},
		{"project mount", "/mint/projects"},
		{"fstab", "fstab"},
		{"bootstrap version", "/var/lib/mint/bootstrap-version"},
		{"health check", "mint:bootstrap=complete"},
		{"idle detection", "mint-idle"},
		{"Docker Compose checksum verification", "DOCKER_COMPOSE_SHA256"},
		{"AWS CLI checksum verification", "AWSCLI_SHA256"},
		{"sha256sum check invocation", "sha256sum --check"},
		{"checksum mismatch fatal", "checksum mismatch"},
	}

	for _, elem := range requiredElements {
		if !strings.Contains(script, elem.pattern) {
			t.Errorf("bootstrap script missing %s (expected to find %q)", elem.desc, elem.pattern)
		}
	}
}

func TestScriptHasCorrectPermissions(t *testing.T) {
	scriptPath := filepath.Join("..", "..", "scripts", "bootstrap.sh")
	info, err := os.Stat(scriptPath)
	if err != nil {
		t.Fatalf("failed to stat bootstrap script: %v", err)
	}

	mode := info.Mode()
	// Script should be executable by owner at minimum.
	if mode&0100 == 0 {
		t.Errorf("bootstrap script is not executable (mode: %o)", mode)
	}
}

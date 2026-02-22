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
		{"Node.js GPG keyring", "nodesource.gpg"},
		{"Node.js signed-by apt repo", "signed-by=${NODESOURCE_KEYRING}"},
		{"EFS NFSv4 mount", "nfsvers=4.1"},
		{"EFS symlinks .ssh", "ln -sfn /mint/user/.ssh /home/ubuntu/.ssh"},
		{"EFS symlinks .config", "ln -sfn /mint/user/.config /home/ubuntu/.config"},
		{"EFS symlinks projects", "ln -sfn /mint/user/projects /home/ubuntu/projects"},
		{"reconciliation service", "mint-reconcile.service"},
		{"reconciliation script", "/usr/local/bin/mint-reconcile"},
		{"reconciliation after network", "network-online.target"},
		{"reconciliation oneshot", "Type=oneshot"},
		// Drift detection and health tagging in reconcile script
		{"drift detection array", "DRIFT_ISSUES"},
		{"health status variable", "HEALTH_STATUS"},
		{"health tag", "mint:health"},
		{"reconcile IMDSv2 token", "X-aws-ec2-metadata-token-ttl-seconds"},
		{"docker drift check", "docker_missing"},
		{"ssh port drift check", "ssh_port_drift"},
		{"mosh drift check", "mosh_missing"},
		{"tmux drift check", "tmux_missing"},
		{"nodejs drift check", "nodejs_missing"},
		{"reconcile health logging", "reconciliation complete: health="},
		// Fix #94: NVMe polling loop replaces udevadm settle
		{"NVMe polling loop present", "lsblk -rno NAME,TYPE"},
		{"NVMe polling uses findmnt for root disk", "findmnt -no SOURCE /"},
		{"NVMe polling timeout 90s", "_t=90"},
		{"NVMe polling sleep interval", "sleep 5"},
		// Fix #95: EXIT trap for mint:bootstrap tagging
		{"EXIT trap registered", "trap '_bootstrap_exit' EXIT"},
		{"bootstrap ok flag initialised false", "_bootstrap_ok=false"},
		{"bootstrap ok flag set true on success", "_bootstrap_ok=true"},
		{"exit trap tags failed on early exit", "mint:bootstrap=failed"},
		{"exit trap uses pre-captured instance id", "_TRAP_INSTANCE_ID"},
		{"exit trap uses pre-captured region", "_TRAP_REGION"},
		{"IMDS token fetched before trap registration", "_IMDS_TOKEN=$(curl"},
	}

	for _, elem := range requiredElements {
		if !strings.Contains(script, elem.pattern) {
			t.Errorf("bootstrap script missing %s (expected to find %q)", elem.desc, elem.pattern)
		}
	}
}

// TestScriptNoUdevadmSettle verifies that the udevadm settle call has been
// replaced by the NVMe polling loop (Fix #94).  "udevadm settle" may appear in
// comments, but must not appear as an executable statement (i.e., on a
// non-comment line).
func TestScriptNoUdevadmSettle(t *testing.T) {
	scriptPath := filepath.Join("..", "..", "scripts", "bootstrap.sh")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("failed to read bootstrap script: %v", err)
	}

	for i, line := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimSpace(line)
		// Skip comment lines — "udevadm settle" is allowed in explanatory comments.
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(trimmed, "udevadm settle") {
			t.Errorf("line %d: bootstrap script contains 'udevadm settle' as a command — replace with NVMe polling loop (Fix #94): %q", i+1, line)
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

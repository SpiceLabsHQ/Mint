package bootstrap

import (
	"fmt"
	"strings"
)

// embeddedStub holds the bootstrap stub template loaded from
// scripts/bootstrap-stub.sh via SetStub (called from main.go's go:embed).
var embeddedStub []byte

// SetStub stores the stub template bytes. Called from main.go immediately
// after the go:embed directive loads scripts/bootstrap-stub.sh.
func SetStub(b []byte) {
	embeddedStub = b
}

// GetStub returns the raw stub template bytes as set by SetStub.
func GetStub() []byte {
	return embeddedStub
}

// RenderStub substitutes the given runtime values into the bootstrap stub
// template and returns the rendered user-data bytes ready to send to EC2.
// It replaces __PLACEHOLDER__ tokens (not bash ${VAR} syntax) so the template
// is safe to store as plain bash without unintended shell evaluation.
//
// Parameters:
//   - sha256:      expected SHA256 hex digest of bootstrap.sh (from ScriptSHA256)
//   - url:         presigned S3 URL to fetch bootstrap.sh (from UploadAndPresign)
//   - efsID:       EFS file system ID to mount
//   - projectDev:  project EBS device path
//   - vmName:      VM name tag
//   - idleTimeout: idle timeout in minutes
func RenderStub(sha256, url, efsID, projectDev, vmName, idleTimeout string) ([]byte, error) {
	if len(embeddedStub) == 0 {
		return nil, fmt.Errorf("bootstrap stub template not loaded; call bootstrap.SetStub before RenderStub")
	}

	rendered := string(embeddedStub)
	rendered = strings.ReplaceAll(rendered, "__MINT_BOOTSTRAP_SHA256__", sha256)
	rendered = strings.ReplaceAll(rendered, "__MINT_BOOTSTRAP_URL__", url)
	rendered = strings.ReplaceAll(rendered, "__MINT_EFS_ID__", efsID)
	rendered = strings.ReplaceAll(rendered, "__MINT_PROJECT_DEV__", projectDev)
	rendered = strings.ReplaceAll(rendered, "__MINT_VM_NAME__", vmName)
	rendered = strings.ReplaceAll(rendered, "__MINT_IDLE_TIMEOUT__", idleTimeout)

	return []byte(rendered), nil
}

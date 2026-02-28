package e2e_test

import (
	"testing"

	"github.com/SpiceLabsHQ/Mint/internal/bootstrap"
)

// stubTemplateForE2ETests is a minimal bootstrap stub used by e2e tests.
// It contains all __PLACEHOLDER__ tokens expected by bootstrap.RenderStub so
// that any test exercising the provision launch path succeeds without real AWS.
const stubTemplateForE2ETests = `#!/bin/bash
export MINT_EFS_ID="__MINT_EFS_ID__"
export MINT_PROJECT_DEV="__MINT_PROJECT_DEV__"
export MINT_VM_NAME="__MINT_VM_NAME__"
export MINT_IDLE_TIMEOUT="__MINT_IDLE_TIMEOUT__"
export MINT_USER_BOOTSTRAP="__MINT_USER_BOOTSTRAP__"
_STUB_URL="__MINT_BOOTSTRAP_URL__"
_STUB_SHA256="__MINT_BOOTSTRAP_SHA256__"
exec /tmp/bootstrap.sh
`

// TestMain loads the test stub template once for the entire e2e test package.
// This ensures that bootstrap.RenderStub does not fail with "stub template not
// loaded" during any test that exercises the provision launch path.
func TestMain(m *testing.M) {
	bootstrap.SetStub([]byte(stubTemplateForE2ETests))
	m.Run()
}

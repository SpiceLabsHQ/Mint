package cmd

import (
	"testing"

	"github.com/SpiceLabsHQ/Mint/internal/bootstrap"
)

// stubTemplateForTests is a minimal bootstrap stub used by cmd-layer tests.
// It contains all __PLACEHOLDER__ tokens expected by bootstrap.RenderStub so
// that any test exercising the provision path succeeds without real AWS.
const stubTemplateForTests = `#!/bin/bash
export MINT_EFS_ID="__MINT_EFS_ID__"
export MINT_PROJECT_DEV="__MINT_PROJECT_DEV__"
export MINT_VM_NAME="__MINT_VM_NAME__"
export MINT_IDLE_TIMEOUT="__MINT_IDLE_TIMEOUT__"
_STUB_URL="__MINT_BOOTSTRAP_URL__"
_STUB_SHA256="__MINT_BOOTSTRAP_SHA256__"
exec /tmp/bootstrap.sh
`

// TestMain loads the test stub template once for the entire cmd test package.
// This ensures that bootstrap.RenderStub does not fail with "stub template not
// loaded" during any test that exercises the provision launch path.
func TestMain(m *testing.M) {
	bootstrap.SetStub([]byte(stubTemplateForTests))
	m.Run()
}

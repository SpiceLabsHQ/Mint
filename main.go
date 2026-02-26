package main

import (
	_ "embed"
	"fmt"
	"os"

	"github.com/SpiceLabsHQ/Mint/cmd"
	"github.com/SpiceLabsHQ/Mint/internal/bootstrap"
)

// bootstrapStub is the tiny EC2 user-data stub template embedded at compile
// time. It contains __PLACEHOLDER__ tokens that RenderStub substitutes at
// provision time. The real bootstrap.sh is fetched by the stub at runtime.
// Because go:embed paths are relative to the source file, the embed directive
// must live here in the project root where scripts/ is accessible.
//
//go:embed scripts/bootstrap-stub.sh
var bootstrapStub []byte

func main() {
	// Store the stub template in the bootstrap package so provision code can
	// call bootstrap.RenderStub(...) without needing main.go in scope.
	bootstrap.SetStub(bootstrapStub)

	// Pass the stub bytes to cmd so that GetBootstrapScript() returns them
	// for any code that still reads the raw template (e.g. tests, doctor).
	// Provision commands (up, recreate) call bootstrap.RenderStub() directly
	// with runtime values to produce the final interpolated user-data.
	if err := cmd.ExecuteWithBootstrapScript(bootstrapStub); err != nil {
		// silentExitError has an empty message — it signals failure without
		// printing (the command already reported the error, e.g., via JSON
		// output on stdout). Only print when the message is non-empty.
		if msg := err.Error(); msg != "" {
			fmt.Fprintln(os.Stderr, msg)
		}
		os.Exit(1)
	}
}

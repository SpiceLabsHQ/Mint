package main

import (
	_ "embed"
	"fmt"
	"os"

	"github.com/nicholasgasior/mint/cmd"
)

// bootstrapScript is the EC2 user-data bootstrap script embedded at compile
// time. Because go:embed paths are relative to the source file, the embed
// directive must live here in the project root where scripts/ is accessible.
//
//go:embed scripts/bootstrap.sh
var bootstrapScript []byte

func main() {
	if err := cmd.ExecuteWithBootstrapScript(bootstrapScript); err != nil {
		// silentExitError has an empty message — it signals failure without
		// printing (the command already reported the error, e.g., via JSON
		// output on stdout). Only print when the message is non-empty.
		if msg := err.Error(); msg != "" {
			fmt.Fprintln(os.Stderr, msg)
		}
		os.Exit(1)
	}
}

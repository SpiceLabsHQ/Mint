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
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

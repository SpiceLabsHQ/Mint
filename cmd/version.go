package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/nicholasgasior/mint/internal/cli"
)

// Build-time variables injected via ldflags. Dev defaults used when building
// without ldflags (e.g., go run, go test).
//
// Set at build time with:
//
//	go build -ldflags "-X github.com/nicholasgasior/mint/cmd.version=1.0.0
//	  -X github.com/nicholasgasior/mint/cmd.commit=abc1234
//	  -X github.com/nicholasgasior/mint/cmd.date=2024-01-15"
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// versionJSON is the JSON representation of version information.
type versionJSON struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version of mint",
		Long:  "Print the version, commit hash, and build date of this mint binary.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cliCtx := cli.FromCommand(cmd)
			if cliCtx != nil && cliCtx.JSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(versionJSON{
					Version: version,
					Commit:  commit,
					Date:    date,
				})
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(),
				"mint version: %s\ncommit: %s\ndate: %s\n",
				version, commit, date,
			)
			return err
		},
	}
}

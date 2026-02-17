// Package cli provides shared CLI infrastructure for the mint command tree.
package cli

import (
	"context"

	"github.com/spf13/cobra"
)

// contextKey is an unexported type for context value keys in this package.
type contextKey struct{}

// CLIContext captures all global persistent flags for propagation through the
// command tree. Created once in PersistentPreRunE and retrieved by subcommands.
type CLIContext struct {
	Verbose bool
	Debug   bool
	JSON    bool
	Yes     bool
	VM      string
}

// NewCLIContext extracts global flag values from a cobra command's persistent
// flags and returns a populated CLIContext.
func NewCLIContext(cmd *cobra.Command) *CLIContext {
	verbose, _ := cmd.Flags().GetBool("verbose")
	debug, _ := cmd.Flags().GetBool("debug")
	jsonFlag, _ := cmd.Flags().GetBool("json")
	yes, _ := cmd.Flags().GetBool("yes")
	vm, _ := cmd.Flags().GetString("vm")

	return &CLIContext{
		Verbose: verbose,
		Debug:   debug,
		JSON:    jsonFlag,
		Yes:     yes,
		VM:      vm,
	}
}

// WithContext returns a new context.Context carrying the given CLIContext.
func WithContext(ctx context.Context, cliCtx *CLIContext) context.Context {
	return context.WithValue(ctx, contextKey{}, cliCtx)
}

// FromContext extracts the CLIContext from a context.Context, or returns nil if
// none is present.
func FromContext(ctx context.Context) *CLIContext {
	cliCtx, _ := ctx.Value(contextKey{}).(*CLIContext)
	return cliCtx
}

// FromCommand extracts the CLIContext from a cobra command's context, or returns
// nil if none is present. This is the primary accessor for subcommands.
func FromCommand(cmd *cobra.Command) *CLIContext {
	if cmd.Context() == nil {
		return nil
	}
	return FromContext(cmd.Context())
}

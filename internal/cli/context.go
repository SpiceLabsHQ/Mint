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
	Profile string
}

// NewCLIContext extracts global flag values from a cobra command's persistent
// flags and returns a populated CLIContext. It resolves flags via the root
// command's PersistentFlags so that persistent flags registered on a parent
// are accessible regardless of which subcommand cmd points to.
func NewCLIContext(cmd *cobra.Command) *CLIContext {
	pflags := cmd.Root().PersistentFlags()
	verbose, _ := pflags.GetBool("verbose")
	debug, _ := pflags.GetBool("debug")
	jsonFlag, _ := pflags.GetBool("json")
	yes, _ := pflags.GetBool("yes")
	vm, _ := pflags.GetString("vm")
	profile, _ := pflags.GetString("profile")

	return &CLIContext{
		Verbose: verbose,
		Debug:   debug,
		JSON:    jsonFlag,
		Yes:     yes,
		VM:      vm,
		Profile: profile,
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

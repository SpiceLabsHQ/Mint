package cli

import (
	"context"
	"testing"

	"github.com/spf13/cobra"
)

func newTestCommand(flags map[string]any) *cobra.Command {
	cmd := &cobra.Command{Use: "test"}
	// Register persistent flags matching root command conventions
	cmd.PersistentFlags().Bool("verbose", false, "")
	cmd.PersistentFlags().Bool("debug", false, "")
	cmd.PersistentFlags().Bool("json", false, "")
	cmd.PersistentFlags().Bool("yes", false, "")
	cmd.PersistentFlags().String("vm", "default", "")

	// Override values by parsing args
	var args []string
	for k, v := range flags {
		switch val := v.(type) {
		case bool:
			if val {
				args = append(args, "--"+k)
			}
		case string:
			args = append(args, "--"+k+"="+val)
		}
	}
	_ = cmd.ParseFlags(args)
	return cmd
}

func TestNewCLIContextDefaults(t *testing.T) {
	cmd := newTestCommand(nil)
	ctx := NewCLIContext(cmd)

	if ctx.Verbose {
		t.Error("Verbose should default to false")
	}
	if ctx.Debug {
		t.Error("Debug should default to false")
	}
	if ctx.JSON {
		t.Error("JSON should default to false")
	}
	if ctx.Yes {
		t.Error("Yes should default to false")
	}
	if ctx.VM != "default" {
		t.Errorf("VM should default to %q, got %q", "default", ctx.VM)
	}
}

func TestNewCLIContextCapturesFlags(t *testing.T) {
	cmd := newTestCommand(map[string]any{
		"verbose": true,
		"debug":   true,
		"json":    true,
		"yes":     true,
		"vm":      "staging",
	})
	ctx := NewCLIContext(cmd)

	if !ctx.Verbose {
		t.Error("Verbose should be true")
	}
	if !ctx.Debug {
		t.Error("Debug should be true")
	}
	if !ctx.JSON {
		t.Error("JSON should be true")
	}
	if !ctx.Yes {
		t.Error("Yes should be true")
	}
	if ctx.VM != "staging" {
		t.Errorf("VM should be %q, got %q", "staging", ctx.VM)
	}
}

func TestNewCLIContextPartialFlags(t *testing.T) {
	cmd := newTestCommand(map[string]any{
		"verbose": true,
		"vm":      "prod",
	})
	ctx := NewCLIContext(cmd)

	if !ctx.Verbose {
		t.Error("Verbose should be true")
	}
	if ctx.Debug {
		t.Error("Debug should remain false")
	}
	if ctx.JSON {
		t.Error("JSON should remain false")
	}
	if ctx.Yes {
		t.Error("Yes should remain false")
	}
	if ctx.VM != "prod" {
		t.Errorf("VM should be %q, got %q", "prod", ctx.VM)
	}
}

func TestFromContextRoundTrip(t *testing.T) {
	original := &CLIContext{
		Verbose: true,
		Debug:   false,
		JSON:    true,
		Yes:     false,
		VM:      "myvm",
	}

	goCtx := WithContext(context.Background(), original)
	retrieved := FromContext(goCtx)

	if retrieved == nil {
		t.Fatal("FromContext returned nil")
	}
	if retrieved.Verbose != original.Verbose {
		t.Error("Verbose mismatch after round-trip")
	}
	if retrieved.JSON != original.JSON {
		t.Error("JSON mismatch after round-trip")
	}
	if retrieved.VM != original.VM {
		t.Errorf("VM mismatch: got %q, want %q", retrieved.VM, original.VM)
	}
}

func TestFromContextMissingReturnsNil(t *testing.T) {
	goCtx := context.Background()
	retrieved := FromContext(goCtx)
	if retrieved != nil {
		t.Error("FromContext should return nil when no CLIContext is set")
	}
}

func TestFromCommandIntegration(t *testing.T) {
	// Simulate the full flow: create context, set on cobra command, retrieve
	cmd := newTestCommand(map[string]any{
		"json": true,
		"vm":   "dev",
	})

	ctx := NewCLIContext(cmd)
	cmd.SetContext(WithContext(context.Background(), ctx))

	retrieved := FromCommand(cmd)
	if retrieved == nil {
		t.Fatal("FromCommand returned nil")
	}
	if !retrieved.JSON {
		t.Error("JSON should be true after FromCommand")
	}
	if retrieved.VM != "dev" {
		t.Errorf("VM should be %q, got %q", "dev", retrieved.VM)
	}
}

func TestNewCLIContextFromChildCommand(t *testing.T) {
	// Create a parent with persistent flags (simulating root command)
	parent := newTestCommand(map[string]any{
		"verbose": true,
		"vm":      "staging",
	})

	// Add a child subcommand
	child := &cobra.Command{Use: "child"}
	parent.AddCommand(child)

	// NewCLIContext called on the child must still resolve root persistent flags
	ctx := NewCLIContext(child)

	if !ctx.Verbose {
		t.Error("Verbose should be true when read from child command")
	}
	if ctx.VM != "staging" {
		t.Errorf("VM should be %q, got %q", "staging", ctx.VM)
	}
	if ctx.Debug {
		t.Error("Debug should remain false")
	}
}

func TestFromCommandWithoutContextReturnsNil(t *testing.T) {
	cmd := &cobra.Command{Use: "bare"}
	retrieved := FromCommand(cmd)
	if retrieved != nil {
		t.Error("FromCommand should return nil when no context is set on command")
	}
}

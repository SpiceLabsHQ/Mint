# ADR-0022: Progress Feedback Convention

## Status
Accepted

## Context
Mint commands that call AWS APIs — `up`, `recreate`, `resize`, `destroy`, `list`, `status` — need progress feedback to satisfy the transparency principle (show your work, no silent waiting). ADR-0012 established that long-running operations display spinners with phase labels, and that non-interactive environments fall back to timestamped lines. The `internal/progress` package implements this: a `Spinner` with `Start`, `Update`, `Stop`, and `Fail` methods, TTY detection via `golang.org/x/term`, and `MINT_NO_SPINNER=1` as an escape hatch.

The problem is wiring. Each command needs a spinner that respects `--verbose`: when verbose is false, the spinner writes to `io.Discard` so the user sees no progress noise; when verbose is true, the spinner writes to the command's output writer with full TTY detection. This wiring logic was implemented as `newCommandSpinner(w io.Writer, verbose bool)` in `cmd/up.go` and called from `cmd/up.go`, `cmd/recreate.go`, and `cmd/resize.go`. The function was unexported and lived in the `cmd` package, which created two problems:

1. **Duplication pressure.** Every new command that needs progress feedback must know the pattern: check verbose, pick `io.Discard` vs real writer, set `Interactive = false` for the discard case. Without a canonical source, each author reimplements the logic or copies it from an existing command.

2. **Discoverability.** A new contributor looking at `internal/progress` sees `New(w)` but not the verbose-gating pattern. The convention for how commands wire spinners is implicit — buried in a specific command file rather than documented alongside the spinner itself.

## Decision

Promote the command spinner factory to `internal/progress` as an exported function:

```go
// NewCommandSpinner creates a Spinner for command progress output.
// When verbose is false, the spinner writes to io.Discard so no progress
// is shown. When verbose is true, the spinner writes to w with TTY
// detection setting Interactive automatically.
func NewCommandSpinner(w io.Writer, verbose bool) *Spinner {
    if !verbose {
        sp := New(io.Discard)
        sp.Interactive = false
        return sp
    }
    return New(w)
}
```

All `cmd/` files call `progress.NewCommandSpinner(cmd.OutOrStdout(), verbose)` instead of a local helper.

### Convention for New Commands

Every command that performs one or more AWS calls that may block (API calls, waiters, polling loops) must follow this pattern:

```go
func runMyCommand(ctx context.Context, cmd *cobra.Command, deps *myCommandDeps) error {
    verbose := false
    if cliCtx := cli.FromContext(ctx); cliCtx != nil {
        verbose = cliCtx.Verbose
    }

    sp := progress.NewCommandSpinner(cmd.OutOrStdout(), verbose)

    // Phase 1: Start the spinner before the first AWS call.
    sp.Start("Discovering VM...")

    found, err := vm.FindVM(ctx, deps.describe, deps.owner, deps.vmName)
    if err != nil {
        sp.Fail(err.Error())
        return fmt.Errorf("discovering VM: %w", err)
    }

    // Phase 2: Update between phases.
    sp.Update("Modifying instance...")

    if err := deps.modifyInstance(ctx, found.InstanceID); err != nil {
        sp.Fail(err.Error())
        return err
    }

    // Phase 3: Stop before printing results.
    sp.Stop("")

    return printResult(cmd, found)
}
```

The rules:

1. **One spinner per command invocation.** Do not create multiple spinners. Use `Update` to advance through phases.
2. **Start before the first AWS call.** The user should see feedback before any network latency.
3. **Update between phases.** Each logical phase (discover, modify, wait, attach) gets its own message via `sp.Update(msg)`.
4. **Fail on error.** Call `sp.Fail(err.Error())` before returning an error so the spinner line is cleared and the failure is visible.
5. **Stop before output.** Call `sp.Stop("")` before printing results (human or JSON). This clears the spinner line in interactive mode so results are not interleaved with spinner frames.
6. **Messages describe the current action.** Use present participle phrases: "Discovering VM...", "Waiting for bootstrap...", "Allocating Elastic IP...". Do not use progress percentages.

### Commands That Skip the Spinner

Commands that return immediately without blocking AWS calls — `version`, `config`, `config get`, `config set`, `completion` — do not need a spinner. The rule is simple: if the command does not call an AWS API that may take more than a few hundred milliseconds, skip the spinner.

`doctor` is a special case: it makes multiple independent AWS checks and prints results incrementally with `[OK]`/`[WARN]`/`[SKIP]` prefixes. It does not use a spinner because its output is already structured as a checklist.

### Alternatives Rejected

**`cmd/cmdutil.go` shared helper file**: A new file in the `cmd` package could house the helper without the promotion to `internal/progress`. Rejected because: (a) `internal/` package tests cannot import `cmd/`, so any test that needs to verify spinner wiring via the package boundary cannot access a `cmd`-layer helper; (b) placing the helper in `cmd/cmdutil.go` signals it is cmd-layer-only and invisible to the broader codebase, reducing discoverability; (c) `internal/progress` already owns all spinner construction — adding the verbose-gating factory there is a natural extension.

**Leaving duplicated across command files**: Each command could copy the three-line verbose-gating pattern directly. Rejected because duplication pressure is already observable (the pattern appeared identically in `up.go`, `recreate.go`, and `resize.go`). New command authors copy from the nearest example, which diverges over time. A canonical exported function is the only mechanism that creates a single source of truth that tooling (godoc, IDE navigation) can surface.

## Consequences

- **Single source of truth.** `progress.NewCommandSpinner` is the canonical factory. The `cmd` package has no local spinner helpers to maintain or copy.
- **No import cycles.** `internal/progress` has no dependencies on `cmd/` or `internal/cli/`. Commands import `internal/progress` — the dependency flows one way.
- **Discoverable convention.** New command authors find `NewCommandSpinner` next to `New` in the `progress` package. The function name and godoc make the verbose-gating pattern obvious without reading existing commands.
- **Consistent user experience.** All commands follow the same Start/Update/Fail/Stop lifecycle. Users see the same spinner behavior whether they run `mint up`, `mint resize`, or any future command.
- **Non-interactive fallback preserved.** The `verbose=false` path writes to `io.Discard`, and the `verbose=true` non-TTY path emits timestamped lines. CI pipelines and piped output continue to work without spinner artifacts.
- **Test ergonomics unchanged.** Tests that inject `*Deps` structs do not interact with the spinner directly — it writes to `cmd.OutOrStdout()`, which tests can capture via `bytes.Buffer`. Tests that need to verify spinner output can inspect the buffer; tests that do not care get silent spinners via `verbose=false`.

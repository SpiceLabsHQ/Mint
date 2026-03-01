# ADR-0022: Progress Feedback Convention

## Status
Accepted

## Context
Mint commands that call AWS APIs — `up`, `recreate`, `resize`, `destroy`, `list`, `status` — need progress feedback to satisfy the transparency principle (show your work, no silent waiting). ADR-0012 established that long-running operations display spinners with phase labels, and that non-interactive environments fall back to timestamped lines. The `internal/progress` package implements this: a `Spinner` with `Start`, `Update`, `Stop`, and `Fail` methods, TTY detection via `golang.org/x/term`, and `MINT_NO_SPINNER=1` as an escape hatch.

The problem is wiring. Each command needs a spinner that respects `--json`: when `--json` is passed, the spinner writes to `io.Discard` so machine-readable output is not corrupted by progress lines; in all other cases, the spinner writes to the command's output writer with full TTY detection. This wiring logic was originally implemented as `newCommandSpinner(w io.Writer, verbose bool)` in `cmd/up.go`, where `verbose=false` discarded all output and `verbose=true` showed it. That convention was inverted — it suppressed progress by default and required an explicit flag to see it. The correct behavior is the opposite: progress is shown by default, and only suppressed when the caller explicitly requests machine-readable output via `--json`.

The factory was promoted to `internal/progress` as `NewCommandSpinner(w io.Writer, quiet bool)` to fix the inversion and centralize the wiring:

1. **Show progress by default.** When `quiet` is false (the normal, non-JSON path), the spinner writes to `w` with TTY detection. Users see progress feedback without passing any flags.

2. **Suppress only for machine-readable output.** When `quiet` is true (the `--json` path), the spinner writes to `io.Discard` so JSON output is clean. The `quiet` parameter exists solely for this case.

3. **Discoverability.** A new contributor looking at `internal/progress` sees `NewCommandSpinner` next to `New`, with the `quiet` parameter and its godoc making the suppression semantics obvious.

## Decision

Promote the command spinner factory to `internal/progress` as an exported function:

```go
// NewCommandSpinner creates a Spinner for command progress output.
// When quiet is false (the default non-JSON path), the spinner writes to w
// with TTY detection setting Interactive automatically. When quiet is true
// (JSON / machine-readable path), the spinner writes to io.Discard so no
// progress is shown and Interactive is forced to false.
func NewCommandSpinner(w io.Writer, quiet bool) *Spinner {
    if quiet {
        sp := New(io.Discard)
        sp.Interactive = false
        return sp
    }
    return New(w)
}
```

All `cmd/` files call `progress.NewCommandSpinner(cmd.OutOrStdout(), jsonOutput)` where `jsonOutput` is the command's `--json` flag value. Commands without a `--json` flag pass `false` as the quiet parameter, which means progress is always shown.

### Convention for New Commands

Every command that performs one or more AWS calls that may block (API calls, waiters, polling loops) must follow this pattern:

```go
func runMyCommand(ctx context.Context, cmd *cobra.Command, deps *myCommandDeps) error {
    jsonOutput, _ := cmd.Flags().GetBool("json")

    sp := progress.NewCommandSpinner(cmd.OutOrStdout(), jsonOutput)

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

For commands that do not have a `--json` flag, pass `false` directly:

```go
sp := progress.NewCommandSpinner(cmd.OutOrStdout(), false)
```

This ensures progress is always shown for interactive commands. The `quiet` parameter should only be `true` when the command produces machine-readable output that would be corrupted by spinner lines.

The rules:

1. **One spinner per command invocation.** Do not create multiple spinners. Use `Update` to advance through phases.
2. **Start before the first AWS call.** The user should see feedback before any network latency.
3. **Update between phases.** Each logical phase (discover, modify, wait, attach) gets its own message via `sp.Update(msg)`.
4. **Fail on error.** Call `sp.Fail(err.Error())` before returning an error so the spinner line is cleared and the failure is visible.
5. **Stop before output.** Call `sp.Stop("")` before printing results (human or JSON). This clears the spinner line in interactive mode so results are not interleaved with spinner frames.
6. **Messages describe the current action.** Use present participle phrases: "Discovering VM...", "Waiting for bootstrap...", "Allocating Elastic IP...". Do not use progress percentages.
7. **Pass `jsonOutput` as the quiet parameter.** The `quiet` parameter maps directly to the command's `--json` flag. Do not pass `verbose`, `!verbose`, or any other derived value. If the command has no `--json` flag, pass `false`.

### Commands That Skip the Spinner

Commands that return immediately without blocking AWS calls — `version`, `config`, `config get`, `config set`, `completion` — do not need a spinner. The rule is simple: if the command does not call an AWS API that may take more than a few hundred milliseconds, skip the spinner.

`doctor` is a special case: it makes multiple independent AWS checks and prints results incrementally with `[OK]`/`[WARN]`/`[SKIP]` prefixes. It does not use a spinner because its output is already structured as a checklist.

### Alternatives Rejected

**`cmd/cmdutil.go` shared helper file**: A new file in the `cmd` package could house the helper without the promotion to `internal/progress`. Rejected because: (a) `internal/` package tests cannot import `cmd/`, so any test that needs to verify spinner wiring via the package boundary cannot access a `cmd`-layer helper; (b) placing the helper in `cmd/cmdutil.go` signals it is cmd-layer-only and invisible to the broader codebase, reducing discoverability; (c) `internal/progress` already owns all spinner construction — adding the quiet-gating factory there is a natural extension.

**Leaving duplicated across command files**: Each command could copy the three-line quiet-gating pattern directly. Rejected because duplication pressure is already observable (the pattern appeared identically in `up.go`, `recreate.go`, and `resize.go`). New command authors copy from the nearest example, which diverges over time. A canonical exported function is the only mechanism that creates a single source of truth that tooling (godoc, IDE navigation) can surface.

**Original `verbose bool` parameter (inverted semantics)**: The initial implementation used `verbose bool` where `verbose=false` discarded output and `verbose=true` showed it. This was rejected because it inverted the expected default: progress feedback should be visible without any flags, and suppressed only for machine-readable contexts. The `quiet bool` parameter correctly models the exception (JSON output) rather than the norm (human output).

## Consequences

- **Progress shown by default.** All commands display spinner feedback during AWS operations without requiring any flags. Users always know what Mint is doing.
- **Single source of truth.** `progress.NewCommandSpinner` is the canonical factory. The `cmd` package has no local spinner helpers to maintain or copy.
- **No import cycles.** `internal/progress` has no dependencies on `cmd/` or `internal/cli/`. Commands import `internal/progress` — the dependency flows one way.
- **Discoverable convention.** New command authors find `NewCommandSpinner` next to `New` in the `progress` package. The function name and godoc make the quiet-gating pattern obvious without reading existing commands.
- **Consistent user experience.** All commands follow the same Start/Update/Fail/Stop lifecycle. Users see the same spinner behavior whether they run `mint up`, `mint resize`, or any future command.
- **Clean machine-readable output.** The `quiet=true` path writes to `io.Discard`, ensuring `--json` output contains only valid JSON without spinner artifacts.
- **Non-interactive fallback preserved.** The `quiet=false` non-TTY path emits timestamped lines. CI pipelines and piped output continue to work without spinner artifacts.
- **Test ergonomics unchanged.** Tests that inject `*Deps` structs do not interact with the spinner directly — it writes to `cmd.OutOrStdout()`, which tests can capture via `bytes.Buffer`. Tests that need to verify spinner output can inspect the buffer; tests that do not care get silent spinners via `quiet=true`.

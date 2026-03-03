# ADR-0027: Command Display Formatting Convention

## Status
Accepted

## Context
Error messages and recovery suggestions across the Mint CLI embed CLI commands using four inconsistent quoting and formatting styles: bare (`mint destroy`), single-quoted (`'mint destroy'`), double-quoted (`"mint destroy"`), and backtick-wrapped (`` `mint destroy` ``). Separator words between prose and the suggested command also vary: `-- run`, `-- run:`, `-- try:`, `Run`, `Tip:`, `create one with:`, and others. The inconsistency makes commands harder to distinguish from surrounding prose in interactive terminals, and the lack of a canonical helper means each new error message invents its own formatting.

Mint's transparency and craft principles require that commands embedded in error output are immediately recognizable. When a developer reads "SSO token expired -- run: aws sso login --profile dev", the command portion should be visually distinct from the explanatory text. Today it is not, and the formatting choice depends on whichever pattern the author copied from the nearest existing call site.

The spinner package (`internal/progress`) already solves a similar consistency problem for progress feedback (ADR-0022): a canonical factory function with TTY-aware behavior, used by all commands. Command formatting needs the same treatment -- a single package that owns the decision of how commands are displayed, with TTY detection determining whether to use color or plain-text fallback.

## Decision

Centralize command display formatting in a new `internal/hint` package. The package provides three helpers that format CLI commands for embedding in error messages, status output, and recovery suggestions.

### Formatting Helpers

```go
// Cmd formats a single command for inline display.
// TTY: ANSI 256-color bold mint green.
// Non-TTY: backtick-wrapped.
func Cmd(cmd string) string

// Block formats one or more commands as an indented block with $ prefix.
// TTY: each line indented and colored.
// Non-TTY: each line indented with backtick wrapping.
func Block(cmds ...string) string

// Suggest formats a labeled command suggestion.
// TTY: "label: " followed by colored command.
// Non-TTY: "label: `command`".
func Suggest(label, cmd string) string
```

### TTY Detection

The package exposes a package-level variable for TTY state:

```go
var IsTTY bool
```

This variable is set at package init time by checking `os.Stdout` via `golang.org/x/term`, following the same pattern used by `internal/progress/progress.go` for spinner interactivity. Test code sets `hint.IsTTY = false` to get deterministic backtick-wrapped output without ANSI escape sequences.

The `MINT_NO_COLOR=1` environment variable disables color output even on TTY, paralleling `MINT_NO_SPINNER=1` in the progress package.

### Color Choice

TTY output uses ANSI 256-color bold mint green: `\033[1;38;5;48m`. This is a deliberate choice:

- **Bold** ensures the command stands out from surrounding regular-weight text.
- **Mint green (color 48)** is thematically consistent with the project name and visually distinct from the default terminal foreground color, red (used for errors by many shells), and yellow (used for warnings).
- **256-color mode** is supported by every terminal emulator Mint targets (iTerm2, Terminal.app, VS Code integrated terminal, Termius on iPad, and all Linux terminal emulators from the last decade). True-color (`\033[38;2;...m`) would offer no benefit and reduces compatibility with older terminals.

Reset is `\033[0m`, applied after every colored span.

### Separator Convention

All error messages that suggest a command use an em-dash separator between the explanatory text and the command:

```
Bootstrap failed (phase: efs-mount) -- run `mint destroy` to clean up
SSO token expired -- run `aws sso login --profile dev`
```

The em-dash (`--`, rendered as two hyphens in terminal output) provides a consistent visual break. Colons, periods, and other separators are not used between prose and inline commands. The `Suggest` helper enforces this for labeled suggestions; inline usage with `Cmd` relies on the convention documented here and enforced by code review.

### Multi-Command Recovery Blocks

When an error requires multiple recovery steps, use `Block` to format them as an indented block:

```
Volume is still attached. Detach it first:
    $ mint destroy --yes
    $ mint up
```

Each line is prefixed with `$ ` to visually indicate a shell command. The indentation separates the block from the surrounding error message.

### Usage in Call Sites

Call sites replace hardcoded formatting with the appropriate helper:

```go
// Before (inconsistent):
fmt.Sprintf("SSO token expired -- run: aws sso login --profile %s", profile)
fmt.Sprintf(`Bootstrap failed. Run "mint destroy" to clean up.`)
fmt.Sprintf("No VM found — create one with: mint up")

// After (consistent):
fmt.Sprintf("SSO token expired -- run %s", hint.Cmd("aws sso login --profile "+profile))
fmt.Sprintf("Bootstrap failed -- run %s to clean up", hint.Cmd("mint destroy"))
fmt.Sprintf("No VM found -- create one with %s", hint.Cmd("mint up"))
```

## Alternatives Rejected

### ANSI-only formatting (no plain-text fallback)

Always emit ANSI escape sequences regardless of output context.

Rejected because: (a) ANSI escapes corrupt `--json` output, which is parsed by scripts and jq; (b) ANSI escapes in log files (structured JSON logs per `internal/logging`) produce unreadable noise; (c) test assertions become fragile, requiring exact ANSI sequence matching or stripping helpers; (d) piped output (`mint up 2>&1 | tee log.txt`) renders poorly in editors that do not interpret ANSI. The dual-mode approach (color on TTY, backtick on non-TTY) handles all these contexts correctly.

### Backtick-only formatting (no color)

Always wrap commands in backticks, regardless of TTY state.

Rejected because: backticks provide minimal visual distinction in a terminal emulator where the surrounding text is the same font and weight. Color is the primary mechanism for drawing the eye to the actionable command in a multi-line error message. Backticks are the correct fallback for non-TTY contexts, but using them as the sole formatting mechanism misses the opportunity to improve the interactive experience -- which is the primary usage context for Mint (ADR-0012).

### External TUI library (lipgloss, termenv, or similar)

Use a third-party terminal UI library for styled text rendering.

Rejected because: (a) Mint's command formatting needs are narrow -- bold color on one string, reset after. A library that manages style profiles, adaptive color, and layout is unnecessary for this scope; (b) adding a dependency increases the surface area for supply-chain risk and version conflicts; (c) the Go standard library plus `golang.org/x/term` already provide everything needed (TTY detection and raw string concatenation); (d) consistency with `internal/progress`, which also avoids TUI libraries for its spinner implementation.

### Display-layer formatting via custom error type

Define a custom error type (e.g., `HintError`) that carries structured command metadata and formats at the display boundary.

Rejected because: (a) error messages in Mint are constructed at the call site and returned as `fmt.Errorf` wrapped errors -- introducing a custom error type requires refactoring every error return path; (b) the display boundary is not always clear (some errors are logged, some printed, some wrapped and re-returned); (c) the formatting decision (color vs. backtick) depends on the output context at the time of display, not at the time of error construction, but errors may cross context boundaries; (d) the complexity is disproportionate to the problem, which is string formatting, not error architecture.

## Consequences

- **All future error messages must use `hint.Cmd()`, `hint.Suggest()`, or `hint.Block()` for command display.** This is a code convention enforced by code review. Hardcoded backticks, quotes, or bare commands in error messages should be flagged during review and migrated to the `hint` helpers.
- **Tests should set `hint.IsTTY = false` and expect backtick-wrapped output.** This eliminates ANSI escape sequences from test assertions, making tests readable and deterministic. Tests that specifically verify color behavior can set `hint.IsTTY = true` and assert on the escape sequence.
- **Existing messages migrated incrementally.** Issues #211 through #215 track the migration of existing error messages across `cmd/` and `internal/` packages. New code uses the `hint` helpers immediately; existing code is migrated per-package in those issues.
- **No new dependencies.** The `internal/hint` package uses only the standard library and `golang.org/x/term` (already a transitive dependency via `internal/progress`). No new modules are introduced.
- **Consistent visual language.** Developers interacting with Mint see a uniform command formatting style across all error messages, whether in a color terminal, a piped log, or `--json` output. This aligns with the craft and transparency principles.
- **TTY detection is injectable for tests.** The package-level `IsTTY` variable can be set directly in test code, following the same pattern as `progress.Spinner.Interactive`. No subprocess spawning or terminal simulation is needed.

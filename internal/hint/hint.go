// Package hint provides TTY-aware command formatting helpers for consistent
// CLI error message formatting across mint commands.
//
// Cmd formats a command for inline use. Block formats one or more commands as
// an indented block. Suggest formats a labeled command suggestion. All three
// adapt their output based on whether stderr is a TTY (colored ANSI) or not
// (plain text with backtick wrapping).
package hint

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// ANSI escape sequences for bold mint green (256-color palette index 48).
const (
	colorMintGreen = "\033[1;38;5;48m"
	colorReset     = "\033[0m"
)

// IsTTY is exported for test override. Set at init from os.Stderr.
var IsTTY bool

func init() {
	IsTTY = term.IsTerminal(int(os.Stderr.Fd()))
}

// Cmd formats a command for inline use in messages.
// TTY: bold mint green ANSI. Non-TTY: backtick-wrapped.
func Cmd(cmd string) string {
	if IsTTY {
		return colorMintGreen + cmd + colorReset
	}
	return "`" + cmd + "`"
}

// Block formats one or more commands as an indented block.
// Each command on its own line with "  $ " prefix.
// Returns an empty string when no commands are provided.
func Block(cmds ...string) string {
	if len(cmds) == 0 {
		return ""
	}
	lines := make([]string, len(cmds))
	for i, cmd := range cmds {
		if IsTTY {
			lines[i] = fmt.Sprintf("  $ %s%s%s", colorMintGreen, cmd, colorReset)
		} else {
			lines[i] = fmt.Sprintf("  $ %s", cmd)
		}
	}
	return strings.Join(lines, "\n")
}

// Suggest formats a labeled command suggestion.
// Example: Suggest("Recover", "mint recreate") produces:
//
//	TTY:     "  Recover:  \033[1;38;5;48mmint recreate\033[0m"
//	Non-TTY: "  Recover:  `mint recreate`"
func Suggest(label, cmd string) string {
	return fmt.Sprintf("  %s:  %s", label, Cmd(cmd))
}

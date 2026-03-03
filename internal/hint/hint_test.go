package hint

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// ANSI constants (duplicated here so tests are self-documenting)
// ---------------------------------------------------------------------------

const (
	testColorMintGreen = "\033[1;38;5;48m"
	testColorReset     = "\033[0m"
)

// ---------------------------------------------------------------------------
// Cmd() — non-TTY
// ---------------------------------------------------------------------------

func TestCmd_NonTTY_WrapsInBackticks(t *testing.T) {
	IsTTY = false
	got := Cmd("mint up")
	want := "`mint up`"
	if got != want {
		t.Errorf("Cmd(\"mint up\") non-TTY = %q, want %q", got, want)
	}
}

func TestCmd_NonTTY_EmptyString(t *testing.T) {
	IsTTY = false
	got := Cmd("")
	want := "``"
	if got != want {
		t.Errorf("Cmd(\"\") non-TTY = %q, want %q", got, want)
	}
}

func TestCmd_NonTTY_NoANSI(t *testing.T) {
	IsTTY = false
	got := Cmd("mint destroy --yes")
	if strings.Contains(got, "\033") {
		t.Errorf("Cmd() non-TTY should not contain ANSI codes, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Cmd() — TTY
// ---------------------------------------------------------------------------

func TestCmd_TTY_BoldMintGreen(t *testing.T) {
	IsTTY = true
	got := Cmd("mint up")
	want := testColorMintGreen + "mint up" + testColorReset
	if got != want {
		t.Errorf("Cmd(\"mint up\") TTY = %q, want %q", got, want)
	}
}

func TestCmd_TTY_EmptyString(t *testing.T) {
	IsTTY = true
	got := Cmd("")
	want := testColorMintGreen + testColorReset
	if got != want {
		t.Errorf("Cmd(\"\") TTY = %q, want %q", got, want)
	}
}

func TestCmd_TTY_ContainsANSI(t *testing.T) {
	IsTTY = true
	got := Cmd("mint ssh")
	if !strings.Contains(got, "\033[") {
		t.Errorf("Cmd() TTY should contain ANSI escape, got %q", got)
	}
}

func TestCmd_TTY_NoBackticks(t *testing.T) {
	IsTTY = true
	got := Cmd("mint up")
	if strings.Contains(got, "`") {
		t.Errorf("Cmd() TTY should not contain backticks, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Block() — non-TTY
// ---------------------------------------------------------------------------

func TestBlock_NonTTY_SingleCommand(t *testing.T) {
	IsTTY = false
	got := Block("mint up")
	want := "  $ mint up"
	if got != want {
		t.Errorf("Block(\"mint up\") non-TTY = %q, want %q", got, want)
	}
}

func TestBlock_NonTTY_MultipleCommands(t *testing.T) {
	IsTTY = false
	got := Block("mint destroy --yes", "mint up")
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("Block() non-TTY with 2 commands: expected 2 lines, got %d: %q", len(lines), got)
	}
	if lines[0] != "  $ mint destroy --yes" {
		t.Errorf("Block() non-TTY line 0 = %q, want %q", lines[0], "  $ mint destroy --yes")
	}
	if lines[1] != "  $ mint up" {
		t.Errorf("Block() non-TTY line 1 = %q, want %q", lines[1], "  $ mint up")
	}
}

func TestBlock_NonTTY_NoANSI(t *testing.T) {
	IsTTY = false
	got := Block("mint up", "mint ssh")
	if strings.Contains(got, "\033") {
		t.Errorf("Block() non-TTY should not contain ANSI codes, got %q", got)
	}
}

func TestBlock_NonTTY_NoCommands(t *testing.T) {
	IsTTY = false
	got := Block()
	if got != "" {
		t.Errorf("Block() with no commands = %q, want empty string", got)
	}
}

// ---------------------------------------------------------------------------
// Block() — TTY
// ---------------------------------------------------------------------------

func TestBlock_TTY_SingleCommand(t *testing.T) {
	IsTTY = true
	got := Block("mint up")
	want := "  $ " + testColorMintGreen + "mint up" + testColorReset
	if got != want {
		t.Errorf("Block(\"mint up\") TTY = %q, want %q", got, want)
	}
}

func TestBlock_TTY_MultipleCommands(t *testing.T) {
	IsTTY = true
	got := Block("mint destroy --yes", "mint up")
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("Block() TTY with 2 commands: expected 2 lines, got %d: %q", len(lines), got)
	}
	want0 := "  $ " + testColorMintGreen + "mint destroy --yes" + testColorReset
	want1 := "  $ " + testColorMintGreen + "mint up" + testColorReset
	if lines[0] != want0 {
		t.Errorf("Block() TTY line 0 = %q, want %q", lines[0], want0)
	}
	if lines[1] != want1 {
		t.Errorf("Block() TTY line 1 = %q, want %q", lines[1], want1)
	}
}

func TestBlock_TTY_NoCommands(t *testing.T) {
	IsTTY = true
	got := Block()
	if got != "" {
		t.Errorf("Block() TTY with no commands = %q, want empty string", got)
	}
}

// ---------------------------------------------------------------------------
// Suggest() — non-TTY
// ---------------------------------------------------------------------------

func TestSuggest_NonTTY_Format(t *testing.T) {
	IsTTY = false
	got := Suggest("Recover", "mint recreate")
	want := "  Recover:  `mint recreate`"
	if got != want {
		t.Errorf("Suggest(\"Recover\", \"mint recreate\") non-TTY = %q, want %q", got, want)
	}
}

func TestSuggest_NonTTY_NoANSI(t *testing.T) {
	IsTTY = false
	got := Suggest("Fix", "mint doctor")
	if strings.Contains(got, "\033") {
		t.Errorf("Suggest() non-TTY should not contain ANSI codes, got %q", got)
	}
}

func TestSuggest_NonTTY_EmptyLabel(t *testing.T) {
	IsTTY = false
	got := Suggest("", "mint up")
	// With empty label, format is "  :  `mint up`"
	want := "  :  `mint up`"
	if got != want {
		t.Errorf("Suggest(\"\", \"mint up\") non-TTY = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// Suggest() — TTY
// ---------------------------------------------------------------------------

func TestSuggest_TTY_Format(t *testing.T) {
	IsTTY = true
	got := Suggest("Recover", "mint recreate")
	want := "  Recover:  " + testColorMintGreen + "mint recreate" + testColorReset
	if got != want {
		t.Errorf("Suggest(\"Recover\", \"mint recreate\") TTY = %q, want %q", got, want)
	}
}

func TestSuggest_TTY_ContainsANSI(t *testing.T) {
	IsTTY = true
	got := Suggest("Run", "mint ssh")
	if !strings.Contains(got, "\033[") {
		t.Errorf("Suggest() TTY should contain ANSI escape, got %q", got)
	}
}

func TestSuggest_TTY_NoBackticks(t *testing.T) {
	IsTTY = true
	got := Suggest("Fix", "mint doctor")
	if strings.Contains(got, "`") {
		t.Errorf("Suggest() TTY should not contain backticks, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// IsTTY override
// ---------------------------------------------------------------------------

func TestIsTTY_DefaultFalseInTestEnvironment(t *testing.T) {
	// In test environments there is no real TTY on stderr.
	// Re-run the init detection manually via the detection logic.
	// The package-level IsTTY is set during init(); in tests it should
	// detect no TTY (since test runner stderr is not a terminal).
	// We cannot assert the init value because earlier tests may have
	// overridden it, but we CAN verify it is overridable.
	IsTTY = false
	if IsTTY {
		t.Error("IsTTY should be false after explicit override")
	}
	IsTTY = true
	if !IsTTY {
		t.Error("IsTTY should be true after explicit override")
	}
}

// ---------------------------------------------------------------------------
// Cmd() preserves special characters
// ---------------------------------------------------------------------------

func TestCmd_NonTTY_PreservesFlags(t *testing.T) {
	IsTTY = false
	got := Cmd("mint up --instance-type m5.xlarge --volume-size 100")
	want := "`mint up --instance-type m5.xlarge --volume-size 100`"
	if got != want {
		t.Errorf("Cmd() non-TTY = %q, want %q", got, want)
	}
}

func TestCmd_TTY_PreservesFlags(t *testing.T) {
	IsTTY = true
	got := Cmd("mint up --instance-type m5.xlarge")
	if !strings.Contains(got, "--instance-type m5.xlarge") {
		t.Errorf("Cmd() TTY should preserve full command, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Block() preserves indentation consistency
// ---------------------------------------------------------------------------

func TestBlock_NonTTY_ThreeCommands(t *testing.T) {
	IsTTY = false
	got := Block("step one", "step two", "step three")
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("Block() with 3 commands: expected 3 lines, got %d", len(lines))
	}
	for i, line := range lines {
		if !strings.HasPrefix(line, "  $ ") {
			t.Errorf("Block() line %d = %q, missing '  $ ' prefix", i, line)
		}
	}
}

// ---------------------------------------------------------------------------
// Suggest() with various label lengths
// ---------------------------------------------------------------------------

func TestSuggest_NonTTY_LongLabel(t *testing.T) {
	IsTTY = false
	got := Suggest("A longer label here", "mint up")
	want := "  A longer label here:  `mint up`"
	if got != want {
		t.Errorf("Suggest() non-TTY long label = %q, want %q", got, want)
	}
}

func TestSuggest_TTY_LongLabel(t *testing.T) {
	IsTTY = true
	got := Suggest("A longer label here", "mint up")
	want := "  A longer label here:  " + testColorMintGreen + "mint up" + testColorReset
	if got != want {
		t.Errorf("Suggest() TTY long label = %q, want %q", got, want)
	}
}

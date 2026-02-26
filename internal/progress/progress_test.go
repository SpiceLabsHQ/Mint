package progress_test

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/nicholasgasior/mint/internal/progress"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newTestSpinner returns a Spinner configured for non-interactive mode,
// writing to buf. This is the primary pattern for unit testing.
func newTestSpinner(buf *bytes.Buffer) *progress.Spinner {
	s := progress.New(buf)
	s.Interactive = false
	return s
}

// ---------------------------------------------------------------------------
// Non-interactive output format
// ---------------------------------------------------------------------------

func TestStartWritesTimestampedLine(t *testing.T) {
	var buf bytes.Buffer
	s := newTestSpinner(&buf)

	s.Start("Launching instance")

	out := buf.String()
	if !strings.Contains(out, "Launching instance") {
		t.Errorf("Start() output %q does not contain message", out)
	}
	// Non-interactive mode should produce a newline-terminated line.
	if !strings.HasSuffix(strings.TrimRight(out, "\n"), "Launching instance") {
		t.Errorf("Start() output %q: expected message at end of line", out)
	}
}

func TestUpdateWritesTimestampedLine(t *testing.T) {
	var buf bytes.Buffer
	s := newTestSpinner(&buf)

	s.Start("Step 1")
	buf.Reset()
	s.Update("Step 2")

	out := buf.String()
	if !strings.Contains(out, "Step 2") {
		t.Errorf("Update() output %q does not contain updated message", out)
	}
}

func TestStopWritesSuccessLine(t *testing.T) {
	var buf bytes.Buffer
	s := newTestSpinner(&buf)

	s.Start("Working")
	buf.Reset()
	s.Stop("Done")

	out := buf.String()
	if !strings.Contains(out, "Done") {
		t.Errorf("Stop() output %q does not contain completion message", out)
	}
}

func TestFailWritesFailureLine(t *testing.T) {
	var buf bytes.Buffer
	s := newTestSpinner(&buf)

	s.Start("Working")
	buf.Reset()
	s.Fail("Something went wrong")

	out := buf.String()
	if !strings.Contains(out, "Something went wrong") {
		t.Errorf("Fail() output %q does not contain failure message", out)
	}
}

// ---------------------------------------------------------------------------
// Timestamp format
// ---------------------------------------------------------------------------

func TestNonInteractiveOutputHasTimestamp(t *testing.T) {
	var buf bytes.Buffer
	s := newTestSpinner(&buf)

	s.Start("Checking status")

	out := buf.String()
	// Expect timestamp bracket format [HH:MM:SS]
	if !strings.Contains(out, "[") || !strings.Contains(out, "]") {
		t.Errorf("non-interactive output %q should contain bracketed timestamp", out)
	}
}

// ---------------------------------------------------------------------------
// Full Start/Update/Stop/Fail sequence
// ---------------------------------------------------------------------------

func TestFullSequenceNonInteractive(t *testing.T) {
	var buf bytes.Buffer
	s := newTestSpinner(&buf)

	s.Start("Starting")
	s.Update("Progress")
	s.Stop("Complete")

	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 3 {
		t.Errorf("expected at least 3 output lines, got %d: %q", len(lines), out)
	}

	if !strings.Contains(lines[0], "Starting") {
		t.Errorf("line 0 %q should contain 'Starting'", lines[0])
	}
	if !strings.Contains(lines[1], "Progress") {
		t.Errorf("line 1 %q should contain 'Progress'", lines[1])
	}
	if !strings.Contains(lines[2], "Complete") {
		t.Errorf("line 2 %q should contain 'Complete'", lines[2])
	}
}

func TestFailSequenceNonInteractive(t *testing.T) {
	var buf bytes.Buffer
	s := newTestSpinner(&buf)

	s.Start("Starting")
	s.Fail("Timed out")

	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 {
		t.Errorf("expected at least 2 output lines, got %d: %q", len(lines), out)
	}

	if !strings.Contains(lines[1], "Timed out") {
		t.Errorf("line 1 %q should contain failure message", lines[1])
	}
}

// ---------------------------------------------------------------------------
// MINT_NO_SPINNER env var overrides to non-interactive mode
// ---------------------------------------------------------------------------

func TestMintNoSpinnerForcesNonInteractive(t *testing.T) {
	t.Setenv("MINT_NO_SPINNER", "1")

	var buf bytes.Buffer
	// Construct a spinner that would be interactive if not for the env var.
	s := progress.New(&buf)
	s.Interactive = true // would be interactive...

	// New() checks MINT_NO_SPINNER, so re-create to honor env:
	s2 := progress.New(&buf)
	buf.Reset()
	s2.Start("Hello")

	// When MINT_NO_SPINNER=1, even if Interactive were set, output must be plain lines.
	out := buf.String()
	// Plain non-interactive output ends with newline, not \r.
	if strings.Contains(out, "\r") {
		t.Errorf("MINT_NO_SPINNER=1 should produce non-interactive output without \\r, got %q", out)
	}
	if !strings.Contains(out, "Hello") {
		t.Errorf("output %q should contain the message", out)
	}

	_ = s // silence unused warning
}

func TestMintNoSpinnerZeroHasNoEffect(t *testing.T) {
	t.Setenv("MINT_NO_SPINNER", "0")

	var buf bytes.Buffer
	s := progress.New(&buf)
	// With MINT_NO_SPINNER=0 the env override is off; Interactive is determined by TTY.
	// In a test environment (no TTY), Interactive should be false.
	if s.Interactive {
		t.Error("expected Interactive=false in test environment (no TTY)")
	}
}

// ---------------------------------------------------------------------------
// New() defaults
// ---------------------------------------------------------------------------

func TestNewDefaultsToStdoutWriter(t *testing.T) {
	s := progress.New(nil)
	// With nil writer, New should set a default (os.Stdout).
	// We can't easily test the actual writer without reflection,
	// but we verify New does not panic and Interactive is a bool.
	_ = s.Interactive
}

func TestNewWithInjectableWriter(t *testing.T) {
	var buf bytes.Buffer
	s := progress.New(&buf)

	s.Interactive = false
	s.Start("Test message")

	if buf.Len() == 0 {
		t.Error("expected output to be written to injected writer")
	}
}

// ---------------------------------------------------------------------------
// Interactive mode: \r overwrite behaviour (structural test only)
// ---------------------------------------------------------------------------

func TestInteractiveModeUsesCarriageReturn(t *testing.T) {
	var buf bytes.Buffer
	s := progress.New(&buf)
	s.Interactive = true

	// In interactive mode, the spinner tick writes \r to overwrite the line.
	// Start() should prime the display; calling tick directly isn't exported,
	// but we can verify Stop() produces a final newline without \r.
	s.Start("Spinning")

	// Brief pause so the goroutine can write at least one frame.
	time.Sleep(20 * time.Millisecond)

	s.Stop("Finished")

	out := buf.String()
	// Interactive mode must end with a newline so the terminal cursor moves down.
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("interactive Stop() output %q should end with newline", out)
	}
}

// ---------------------------------------------------------------------------
// Spinner characters
// ---------------------------------------------------------------------------

func TestSpinnerCharsExported(t *testing.T) {
	// Verify the package exports the spinner frames constant or that they are
	// used correctly by checking output contains one of the known braille frames
	// when in interactive mode (by inspecting bytes, not relying on TTY).
	// This is a structural smoke test.
	frames := progress.SpinnerFrames
	if len(frames) == 0 {
		t.Fatal("SpinnerFrames must not be empty")
	}
	for _, f := range frames {
		if f == "" {
			t.Error("SpinnerFrames must not contain empty strings")
		}
	}
}

// ---------------------------------------------------------------------------
// Idempotency: Stop/Fail after already stopped must not panic
// ---------------------------------------------------------------------------

func TestStopAfterStopDoesNotPanic(t *testing.T) {
	var buf bytes.Buffer
	s := newTestSpinner(&buf)
	s.Start("Working")
	s.Stop("Done")
	// Second stop should be safe.
	s.Stop("Done again")
}

func TestFailAfterFailDoesNotPanic(t *testing.T) {
	var buf bytes.Buffer
	s := newTestSpinner(&buf)
	s.Start("Working")
	s.Fail("Error")
	s.Fail("Error again")
}

// ---------------------------------------------------------------------------
// Methods callable before Start (must not panic)
// ---------------------------------------------------------------------------

func TestUpdateBeforeStartDoesNotPanic(t *testing.T) {
	var buf bytes.Buffer
	s := newTestSpinner(&buf)
	s.Update("No start yet")
}

func TestStopBeforeStartDoesNotPanic(t *testing.T) {
	var buf bytes.Buffer
	s := newTestSpinner(&buf)
	s.Stop("No start yet")
}

func TestFailBeforeStartDoesNotPanic(t *testing.T) {
	var buf bytes.Buffer
	s := newTestSpinner(&buf)
	s.Fail("No start yet")
}

// ---------------------------------------------------------------------------
// NewCommandSpinner
// ---------------------------------------------------------------------------

// TestNewCommandSpinnerVerboseFalseDiscardsOutput verifies that when
// verbose=false the spinner writes to io.Discard and produces no visible
// output. The spinner must still be functional (Start/Stop without panic).
func TestNewCommandSpinnerVerboseFalseDiscardsOutput(t *testing.T) {
	var buf bytes.Buffer
	sp := progress.NewCommandSpinner(&buf, false)
	if sp == nil {
		t.Fatal("NewCommandSpinner returned nil")
	}

	sp.Start("Should not appear")
	sp.Update("Still nothing")
	sp.Stop("Done")

	// verbose=false discards all output; buf must be empty.
	if buf.Len() != 0 {
		t.Errorf("verbose=false: expected no output to writer, got %q", buf.String())
	}
}

// TestNewCommandSpinnerVerboseFalseInteractiveDisabled verifies that when
// verbose=false the spinner has Interactive=false (no goroutine races).
func TestNewCommandSpinnerVerboseFalseInteractiveDisabled(t *testing.T) {
	sp := progress.NewCommandSpinner(io.Discard, false)
	if sp.Interactive {
		t.Error("verbose=false: expected Interactive=false to prevent goroutine-based animation")
	}
}

// TestNewCommandSpinnerVerboseTrueWritesToWriter verifies that when
// verbose=true output is written to the provided writer.
func TestNewCommandSpinnerVerboseTrueWritesToWriter(t *testing.T) {
	var buf bytes.Buffer
	sp := progress.NewCommandSpinner(&buf, true)
	if sp == nil {
		t.Fatal("NewCommandSpinner returned nil")
	}

	// Force non-interactive so the test does not spin a goroutine on a non-TTY.
	sp.Interactive = false

	sp.Start("Running step")
	sp.Stop("Complete")

	out := buf.String()
	if !strings.Contains(out, "Running step") {
		t.Errorf("verbose=true: expected output to contain message, got %q", out)
	}
}

// TestNewCommandSpinnerNilWriterVerboseFalse verifies that a nil writer is
// safe when verbose=false (falls through to io.Discard path, no panic).
func TestNewCommandSpinnerNilWriterVerboseFalse(t *testing.T) {
	sp := progress.NewCommandSpinner(nil, false)
	if sp == nil {
		t.Fatal("NewCommandSpinner(nil, false) returned nil")
	}
	sp.Start("safe")
	sp.Stop("")
}

// TestNewCommandSpinnerNilWriterVerboseTrue verifies that a nil writer is
// safe when verbose=true (falls through to os.Stdout, no panic).
func TestNewCommandSpinnerNilWriterVerboseTrue(t *testing.T) {
	sp := progress.NewCommandSpinner(nil, true)
	if sp == nil {
		t.Fatal("NewCommandSpinner(nil, true) returned nil")
	}
	// Do not call Start in this case; os.Stdout write is fine but noisy in tests.
}

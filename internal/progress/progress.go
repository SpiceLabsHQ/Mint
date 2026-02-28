// Package progress provides TTY detection and a progress spinner for use
// across mint commands.
//
// In interactive mode (TTY detected and MINT_NO_SPINNER unset), the Spinner
// renders animated braille characters with \r overwrite so output stays on
// one line. In non-interactive mode (CI, pipes, MINT_NO_SPINNER=1), each
// update is a plain timestamped line such as:
//
//	[12:34:56] Waiting for bootstrap...
//
// The Spinner is safe to construct with New() and immediately Start/Stop. All
// methods are safe to call in any order; calling Stop or Fail before Start is
// a no-op.
package progress

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"golang.org/x/term"
)

// NewCommandSpinner creates a Spinner for command progress output.
// When quiet is false (the default non-JSON path), the spinner writes to w
// (or os.Stdout when w is nil) and TTY detection sets Interactive
// automatically; in non-interactive environments (CI, tests) the spinner emits
// plain timestamped lines. When quiet is true (JSON / machine-readable path),
// the spinner writes to io.Discard so no progress is shown and Interactive is
// forced to false.
func NewCommandSpinner(w io.Writer, quiet bool) *Spinner {
	if quiet {
		sp := New(io.Discard)
		sp.Interactive = false
		return sp
	}
	return New(w)
}

// SpinnerFrames are the braille characters used for the animated spinner in
// interactive (TTY) mode. Exported so callers can inspect them in tests.
var SpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// tickInterval controls how fast the spinner animates in interactive mode.
const tickInterval = 80 * time.Millisecond

// Spinner displays progress to the user. Create one with New() and call
// Start, Update, Stop, and Fail in sequence.
//
// Fields Interactive and Writer are public so tests can override them after
// construction without needing additional constructors.
type Spinner struct {
	// Interactive controls whether animated (TTY) or plain-line output is used.
	// New() sets this automatically; override in tests to force a mode.
	Interactive bool

	// Writer is the destination for all output. Defaults to os.Stdout.
	Writer io.Writer

	mu      sync.Mutex
	msg     string
	running bool    // true while the ticker goroutine is active
	stop    chan struct{}
	done    chan struct{}
	frame   int
}

// New constructs a Spinner. Pass an io.Writer to redirect output (useful in
// tests); pass nil to use os.Stdout.
//
// TTY detection: Interactive is set to true only when both of the following
// hold:
//   - MINT_NO_SPINNER env var is not "1"
//   - os.Stdout is a terminal (golang.org/x/term.IsTerminal)
//
// If w is non-nil, TTY detection is still performed against os.Stdout so that
// the output format matches what a real terminal would see. Override
// Interactive directly after construction to force a specific mode.
func New(w io.Writer) *Spinner {
	if w == nil {
		w = os.Stdout
	}

	interactive := false
	if os.Getenv("MINT_NO_SPINNER") != "1" {
		interactive = term.IsTerminal(int(os.Stdout.Fd()))
	}

	return &Spinner{
		Interactive: interactive,
		Writer:      w,
	}
}

// Start begins progress display with the given message. In interactive mode
// the spinner goroutine starts immediately. In non-interactive mode a single
// timestamped line is written.
//
// Calling Start on an already-started Spinner is equivalent to Update.
func (s *Spinner) Start(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.msg = msg

	if s.running {
		// Already running; treat as update.
		return
	}

	if s.Interactive {
		s.stop = make(chan struct{})
		s.done = make(chan struct{})
		s.running = true
		go s.spin()
	} else {
		s.writeLine(msg)
	}
}

// Update changes the displayed message. In interactive mode the next tick
// will pick up the new message. In non-interactive mode a new timestamped
// line is written immediately.
func (s *Spinner) Update(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.msg = msg

	if !s.Interactive {
		s.writeLine(msg)
	}
	// Interactive: the goroutine reads s.msg on every tick; no extra action needed.
}

// Stop halts the spinner and prints a final success message. In interactive
// mode the spinner goroutine is stopped and the line is cleared before
// printing the completion line.
func (s *Spinner) Stop(msg string) {
	s.finish(msg, false)
}

// Fail halts the spinner and prints a final failure message. Identical to
// Stop but semantically signals an error outcome.
func (s *Spinner) Fail(msg string) {
	s.finish(msg, true)
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// finish stops the spinner (if running) and writes the final message.
func (s *Spinner) finish(msg string, _ bool) {
	s.mu.Lock()

	if s.running {
		close(s.stop)
		s.running = false
		s.mu.Unlock()

		// Wait for the goroutine to exit before writing the final line so we
		// don't interleave with a tick in progress.
		<-s.done

		s.mu.Lock()
		// Clear the current spinner line with spaces before the final message.
		fmt.Fprint(s.Writer, "\r\033[K")
		fmt.Fprintln(s.Writer, msg)
		s.mu.Unlock()
		return
	}

	// Non-interactive or Stop/Fail before Start: just write a plain line.
	if msg != "" {
		if s.Interactive {
			fmt.Fprintln(s.Writer, msg)
		} else {
			s.writeLine(msg)
		}
	}
	s.mu.Unlock()
}

// spin is the goroutine that renders the animated spinner in interactive mode.
// It stops when the stop channel is closed.
func (s *Spinner) spin() {
	defer close(s.done)

	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.mu.Lock()
			frame := SpinnerFrames[s.frame%len(SpinnerFrames)]
			s.frame++
			msg := s.msg
			s.mu.Unlock()

			fmt.Fprintf(s.Writer, "\r%s %s", frame, msg)
		}
	}
}

// writeLine writes a single non-interactive timestamped line.
// Must be called with mu held.
func (s *Spinner) writeLine(msg string) {
	ts := time.Now().Format("15:04:05")
	fmt.Fprintf(s.Writer, "[%s] %s\n", ts, msg)
}

// Package session implements idle detection criteria from ADR-0018.
// It checks for active SSH/mosh sessions, tmux clients, claude processes
// in containers, and manual extend timestamps on a remote VM.
package session

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ExtendTimestampPath is the path on the VM where the manual extend
// timestamp is written by `mint extend`.
const ExtendTimestampPath = "/var/lib/mint/idle-extended-until"

// RemoteExecutor runs a command on a remote VM and returns stdout.
// Callers wrap their SSH/Instance Connect runner to match this interface,
// keeping the session package independent of transport details.
type RemoteExecutor func(ctx context.Context, command []string) ([]byte, error)

// ActiveSessions holds the results of all four ADR-0018 idle detection
// criteria. Each field is populated independently; any non-empty field
// indicates activity.
type ActiveSessions struct {
	// TmuxClients contains the formatted tmux client list, or empty if
	// no attached clients were found.
	TmuxClients string

	// SSHConnections contains the formatted `who` output, or empty if
	// no active SSH/mosh connections were found.
	SSHConnections string

	// ClaudeProcesses contains formatted docker top matches for claude
	// processes running inside containers, or empty if none found.
	ClaudeProcesses string

	// ExtendedUntil is the manual extend timestamp if it is still in the
	// future, or nil if no extend is active.
	ExtendedUntil *time.Time
}

// HasActivity returns true if any of the four ADR-0018 criteria indicate
// the VM is actively in use.
func (a *ActiveSessions) HasActivity() bool {
	return a.TmuxClients != "" ||
		a.SSHConnections != "" ||
		a.ClaudeProcesses != "" ||
		a.ExtendedUntil != nil
}

// Summary returns a formatted multi-line summary of all active session
// indicators, suitable for display in CLI output. Returns empty string
// if no activity is detected.
func (a *ActiveSessions) Summary() string {
	var parts []string

	if a.TmuxClients != "" {
		parts = append(parts, "  Tmux clients:\n    "+strings.ReplaceAll(a.TmuxClients, "\n", "\n    "))
	}
	if a.SSHConnections != "" {
		parts = append(parts, "  Active connections:\n    "+strings.ReplaceAll(a.SSHConnections, "\n", "\n    "))
	}
	if a.ClaudeProcesses != "" {
		parts = append(parts, "  Claude processes in containers:\n    "+strings.ReplaceAll(a.ClaudeProcesses, "\n", "\n    "))
	}
	if a.ExtendedUntil != nil {
		parts = append(parts, fmt.Sprintf("  Manual extend active until %s", a.ExtendedUntil.Format(time.RFC3339)))
	}

	return strings.Join(parts, "\n")
}

// nowFunc is a package-level variable to allow tests to override time.Now.
var nowFunc = time.Now

// DetectActiveSessions checks all four ADR-0018 idle detection criteria
// on the remote VM via the provided executor. It returns an ActiveSessions
// struct with the results. Individual check failures are treated as
// non-fatal (the criterion is simply not populated) unless the error
// indicates a fundamental connectivity problem.
func DetectActiveSessions(ctx context.Context, exec RemoteExecutor) (*ActiveSessions, error) {
	result := &ActiveSessions{}

	// Criterion 1: tmux attached clients.
	if err := detectTmux(ctx, exec, result); err != nil {
		return nil, err
	}

	// Criterion 2: SSH/mosh sessions.
	if err := detectSSH(ctx, exec, result); err != nil {
		return nil, err
	}

	// Criterion 3: Claude processes in containers.
	detectClaude(ctx, exec, result)

	// Criterion 4: Manual extend timestamp.
	detectExtend(ctx, exec, result)

	return result, nil
}

// detectTmux checks for attached tmux clients on the remote VM.
func detectTmux(ctx context.Context, exec RemoteExecutor, result *ActiveSessions) error {
	output, err := exec(ctx, []string{
		"tmux", "list-clients", "-F", "#{client_name} #{session_name}",
	})
	if err != nil {
		if isTmuxNoSessionsError(err) {
			return nil
		}
		return fmt.Errorf("checking tmux clients: %w", err)
	}

	clients := strings.TrimSpace(string(output))
	if clients != "" {
		result.TmuxClients = clients
	}
	return nil
}

// detectSSH checks for active SSH/mosh connections via the `who` command.
func detectSSH(ctx context.Context, exec RemoteExecutor, result *ActiveSessions) error {
	output, err := exec(ctx, []string{"who"})
	if err != nil {
		return fmt.Errorf("checking active connections: %w", err)
	}

	connections := strings.TrimSpace(string(output))
	if connections != "" {
		result.SSHConnections = connections
	}
	return nil
}

// detectClaude checks for claude processes running in Docker containers.
// Docker not being installed or having no running containers is not an
// error -- the criterion simply does not apply.
func detectClaude(ctx context.Context, exec RemoteExecutor, result *ActiveSessions) {
	// Get list of running container IDs.
	output, err := exec(ctx, []string{"docker", "ps", "-q"})
	if err != nil {
		// Docker not installed or not running -- not an error.
		return
	}

	containerIDs := strings.TrimSpace(string(output))
	if containerIDs == "" {
		return
	}

	var matches []string
	for _, id := range strings.Split(containerIDs, "\n") {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}

		topOutput, topErr := exec(ctx, []string{
			"docker", "top", id, "-o", "pid,comm",
		})
		if topErr != nil {
			// Container may have stopped between ps and top -- skip.
			continue
		}

		for _, line := range strings.Split(string(topOutput), "\n") {
			if strings.Contains(line, "claude") {
				matches = append(matches, fmt.Sprintf("%s: %s", id, strings.TrimSpace(line)))
			}
		}
	}

	if len(matches) > 0 {
		result.ClaudeProcesses = strings.Join(matches, "\n")
	}
}

// detectExtend checks for a manual extend timestamp on the VM.
func detectExtend(ctx context.Context, exec RemoteExecutor, result *ActiveSessions) {
	output, err := exec(ctx, []string{"cat", ExtendTimestampPath})
	if err != nil {
		// File not found or unreadable -- no extend active.
		return
	}

	tsStr := strings.TrimSpace(string(output))
	if tsStr == "" {
		return
	}

	ts, parseErr := time.Parse(time.RFC3339, tsStr)
	if parseErr != nil {
		// Invalid timestamp format -- treat as no extend.
		return
	}

	if nowFunc().Before(ts) {
		result.ExtendedUntil = &ts
	}
}

// isTmuxNoSessionsError returns true if the error indicates tmux has no
// sessions or the tmux server is not running. These are normal conditions,
// not real errors.
func isTmuxNoSessionsError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "no server running") ||
		strings.Contains(msg, "no sessions")
}

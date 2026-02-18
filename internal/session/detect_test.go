package session

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// mockExecutor dispatches commands to pre-configured responses.
type mockExecutor struct {
	responses map[string]mockResponse
}

type mockResponse struct {
	output []byte
	err    error
}

func (m *mockExecutor) run(ctx context.Context, command []string) ([]byte, error) {
	key := strings.Join(command, " ")
	if resp, ok := m.responses[key]; ok {
		return resp.output, resp.err
	}
	// Check prefix matches for docker top commands (container ID varies).
	for k, resp := range m.responses {
		if strings.HasPrefix(key, k) {
			return resp.output, resp.err
		}
	}
	return nil, fmt.Errorf("unexpected command: %v", command)
}

func newMockExecutor() *mockExecutor {
	return &mockExecutor{
		responses: make(map[string]mockResponse),
	}
}

func (m *mockExecutor) set(cmd string, output []byte, err error) {
	m.responses[cmd] = mockResponse{output: output, err: err}
}

// ---------------------------------------------------------------------------
// Tests — DetectActiveSessions
// ---------------------------------------------------------------------------

func TestDetectActiveSessions_AllCriteriaActive(t *testing.T) {
	mock := newMockExecutor()

	// tmux clients
	mock.set("tmux list-clients -F #{client_name} #{session_name}",
		[]byte("/dev/pts/0 main\n"), nil)

	// who
	mock.set("who",
		[]byte("ubuntu pts/0        2025-01-15 10:30 (192.168.1.100)\n"), nil)

	// docker ps
	mock.set("docker ps -q",
		[]byte("abc123\n"), nil)

	// docker top
	mock.set("docker top abc123 -o pid,comm",
		[]byte("PID COMMAND\n1 node\n42 claude\n"), nil)

	// extend timestamp (1 hour in the future)
	future := time.Now().Add(1 * time.Hour).Format(time.RFC3339)
	mock.set("cat "+ExtendTimestampPath,
		[]byte(future+"\n"), nil)

	result, err := DetectActiveSessions(context.Background(), mock.run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.HasActivity() {
		t.Fatal("expected HasActivity() to be true")
	}

	if result.TmuxClients == "" {
		t.Error("expected TmuxClients to be populated")
	}
	if result.SSHConnections == "" {
		t.Error("expected SSHConnections to be populated")
	}
	if result.ClaudeProcesses == "" {
		t.Error("expected ClaudeProcesses to be populated")
	}
	if result.ExtendedUntil == nil {
		t.Error("expected ExtendedUntil to be non-nil")
	}

	summary := result.Summary()
	if !strings.Contains(summary, "Tmux clients") {
		t.Errorf("summary missing tmux section, got:\n%s", summary)
	}
	if !strings.Contains(summary, "Active connections") {
		t.Errorf("summary missing connections section, got:\n%s", summary)
	}
	if !strings.Contains(summary, "Claude processes in containers") {
		t.Errorf("summary missing claude section, got:\n%s", summary)
	}
	if !strings.Contains(summary, "Manual extend active until") {
		t.Errorf("summary missing extend section, got:\n%s", summary)
	}
}

func TestDetectActiveSessions_NoActivity(t *testing.T) {
	mock := newMockExecutor()

	mock.set("tmux list-clients -F #{client_name} #{session_name}",
		nil, fmt.Errorf("no server running on /tmp/tmux-1000/default"))
	mock.set("who", []byte(""), nil)
	mock.set("docker ps -q", nil, fmt.Errorf("docker: command not found"))
	mock.set("cat "+ExtendTimestampPath,
		nil, fmt.Errorf("No such file or directory"))

	result, err := DetectActiveSessions(context.Background(), mock.run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.HasActivity() {
		t.Error("expected HasActivity() to be false with no activity")
	}

	if result.Summary() != "" {
		t.Errorf("expected empty summary, got: %q", result.Summary())
	}
}

func TestDetectActiveSessions_OnlyClaudeProcesses(t *testing.T) {
	mock := newMockExecutor()

	// No tmux, no SSH
	mock.set("tmux list-clients -F #{client_name} #{session_name}",
		nil, fmt.Errorf("no server running on /tmp/tmux-1000/default"))
	mock.set("who", []byte(""), nil)

	// Docker with claude process
	mock.set("docker ps -q", []byte("def456\n"), nil)
	mock.set("docker top def456 -o pid,comm",
		[]byte("PID COMMAND\n1 bash\n99 claude\n"), nil)

	// No extend
	mock.set("cat "+ExtendTimestampPath,
		nil, fmt.Errorf("No such file or directory"))

	result, err := DetectActiveSessions(context.Background(), mock.run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.HasActivity() {
		t.Fatal("expected HasActivity() to be true when claude process found")
	}
	if result.ClaudeProcesses == "" {
		t.Error("expected ClaudeProcesses to be populated")
	}
	if !strings.Contains(result.ClaudeProcesses, "def456") {
		t.Errorf("expected container ID in ClaudeProcesses, got: %q", result.ClaudeProcesses)
	}
	if !strings.Contains(result.ClaudeProcesses, "claude") {
		t.Errorf("expected 'claude' in ClaudeProcesses, got: %q", result.ClaudeProcesses)
	}

	// Other criteria should be empty.
	if result.TmuxClients != "" {
		t.Error("expected TmuxClients to be empty")
	}
	if result.SSHConnections != "" {
		t.Error("expected SSHConnections to be empty")
	}
	if result.ExtendedUntil != nil {
		t.Error("expected ExtendedUntil to be nil")
	}
}

func TestDetectActiveSessions_DockerNotInstalled(t *testing.T) {
	mock := newMockExecutor()

	mock.set("tmux list-clients -F #{client_name} #{session_name}",
		nil, fmt.Errorf("no sessions"))
	mock.set("who", []byte(""), nil)
	mock.set("docker ps -q", nil, fmt.Errorf("docker: command not found"))
	mock.set("cat "+ExtendTimestampPath,
		nil, fmt.Errorf("No such file or directory"))

	result, err := DetectActiveSessions(context.Background(), mock.run)
	if err != nil {
		t.Fatalf("docker not installed should not cause error: %v", err)
	}

	if result.HasActivity() {
		t.Error("expected no activity when docker is not installed")
	}
}

func TestDetectActiveSessions_DockerNoContainers(t *testing.T) {
	mock := newMockExecutor()

	mock.set("tmux list-clients -F #{client_name} #{session_name}",
		nil, fmt.Errorf("no sessions"))
	mock.set("who", []byte(""), nil)
	mock.set("docker ps -q", []byte(""), nil)
	mock.set("cat "+ExtendTimestampPath,
		nil, fmt.Errorf("No such file or directory"))

	result, err := DetectActiveSessions(context.Background(), mock.run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ClaudeProcesses != "" {
		t.Error("expected empty ClaudeProcesses with no containers")
	}
}

func TestDetectActiveSessions_MultipleContainers(t *testing.T) {
	mock := newMockExecutor()

	mock.set("tmux list-clients -F #{client_name} #{session_name}",
		nil, fmt.Errorf("no sessions"))
	mock.set("who", []byte(""), nil)

	// Two containers, claude only in second
	mock.set("docker ps -q", []byte("aaa111\nbbb222\n"), nil)
	mock.set("docker top aaa111 -o pid,comm",
		[]byte("PID COMMAND\n1 node\n"), nil)
	mock.set("docker top bbb222 -o pid,comm",
		[]byte("PID COMMAND\n1 bash\n50 claude\n"), nil)

	mock.set("cat "+ExtendTimestampPath,
		nil, fmt.Errorf("No such file or directory"))

	result, err := DetectActiveSessions(context.Background(), mock.run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result.ClaudeProcesses, "bbb222") {
		t.Errorf("expected bbb222 in ClaudeProcesses, got: %q", result.ClaudeProcesses)
	}
	// aaa111 should NOT appear (no claude process)
	if strings.Contains(result.ClaudeProcesses, "aaa111") {
		t.Errorf("aaa111 should not be in ClaudeProcesses (no claude), got: %q", result.ClaudeProcesses)
	}
}

func TestDetectActiveSessions_ContainerStoppedBetweenPsAndTop(t *testing.T) {
	mock := newMockExecutor()

	mock.set("tmux list-clients -F #{client_name} #{session_name}",
		nil, fmt.Errorf("no sessions"))
	mock.set("who", []byte(""), nil)
	mock.set("docker ps -q", []byte("gone123\n"), nil)
	mock.set("docker top gone123 -o pid,comm",
		nil, fmt.Errorf("container gone123 is not running"))
	mock.set("cat "+ExtendTimestampPath,
		nil, fmt.Errorf("No such file or directory"))

	result, err := DetectActiveSessions(context.Background(), mock.run)
	if err != nil {
		t.Fatalf("container stopping should not cause error: %v", err)
	}
	if result.ClaudeProcesses != "" {
		t.Error("expected empty ClaudeProcesses when container is gone")
	}
}

func TestDetectActiveSessions_ExtendActive(t *testing.T) {
	mock := newMockExecutor()

	mock.set("tmux list-clients -F #{client_name} #{session_name}",
		nil, fmt.Errorf("no sessions"))
	mock.set("who", []byte(""), nil)
	mock.set("docker ps -q", nil, fmt.Errorf("docker: command not found"))

	// Extend timestamp 2 hours in the future.
	future := time.Now().Add(2 * time.Hour)
	mock.set("cat "+ExtendTimestampPath,
		[]byte(future.Format(time.RFC3339)+"\n"), nil)

	result, err := DetectActiveSessions(context.Background(), mock.run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.HasActivity() {
		t.Fatal("expected HasActivity() to be true when extend is active")
	}
	if result.ExtendedUntil == nil {
		t.Fatal("expected ExtendedUntil to be non-nil")
	}

	summary := result.Summary()
	if !strings.Contains(summary, "Manual extend active until") {
		t.Errorf("summary missing extend info, got:\n%s", summary)
	}
}

func TestDetectActiveSessions_ExtendExpired(t *testing.T) {
	mock := newMockExecutor()

	mock.set("tmux list-clients -F #{client_name} #{session_name}",
		nil, fmt.Errorf("no sessions"))
	mock.set("who", []byte(""), nil)
	mock.set("docker ps -q", nil, fmt.Errorf("docker: command not found"))

	// Extend timestamp 1 hour in the past.
	past := time.Now().Add(-1 * time.Hour)
	mock.set("cat "+ExtendTimestampPath,
		[]byte(past.Format(time.RFC3339)+"\n"), nil)

	result, err := DetectActiveSessions(context.Background(), mock.run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.ExtendedUntil != nil {
		t.Error("expected ExtendedUntil to be nil for expired timestamp")
	}
	if result.HasActivity() {
		t.Error("expected no activity for expired extend")
	}
}

func TestDetectActiveSessions_ExtendInvalidTimestamp(t *testing.T) {
	mock := newMockExecutor()

	mock.set("tmux list-clients -F #{client_name} #{session_name}",
		nil, fmt.Errorf("no sessions"))
	mock.set("who", []byte(""), nil)
	mock.set("docker ps -q", nil, fmt.Errorf("docker: command not found"))
	mock.set("cat "+ExtendTimestampPath,
		[]byte("not-a-timestamp\n"), nil)

	result, err := DetectActiveSessions(context.Background(), mock.run)
	if err != nil {
		t.Fatalf("invalid timestamp should not cause error: %v", err)
	}

	if result.ExtendedUntil != nil {
		t.Error("expected ExtendedUntil to be nil for invalid timestamp")
	}
}

func TestDetectActiveSessions_TmuxNoServerRunning(t *testing.T) {
	mock := newMockExecutor()

	mock.set("tmux list-clients -F #{client_name} #{session_name}",
		nil, fmt.Errorf("no server running on /tmp/tmux-1000/default"))
	mock.set("who", []byte(""), nil)
	mock.set("docker ps -q", nil, fmt.Errorf("docker: command not found"))
	mock.set("cat "+ExtendTimestampPath,
		nil, fmt.Errorf("No such file or directory"))

	result, err := DetectActiveSessions(context.Background(), mock.run)
	if err != nil {
		t.Fatalf("tmux no server should be graceful: %v", err)
	}

	if result.TmuxClients != "" {
		t.Error("expected empty TmuxClients when no server running")
	}
}

func TestDetectActiveSessions_TmuxRealError(t *testing.T) {
	mock := newMockExecutor()

	// A real SSH error (not tmux-specific)
	mock.set("tmux list-clients -F #{client_name} #{session_name}",
		nil, fmt.Errorf("connection refused"))

	result, err := DetectActiveSessions(context.Background(), mock.run)
	if err == nil {
		t.Fatal("expected error for tmux connection failure")
	}
	if !strings.Contains(err.Error(), "checking tmux clients") {
		t.Errorf("error %q should mention tmux clients", err.Error())
	}
	if result != nil {
		t.Error("expected nil result on error")
	}
}

func TestDetectActiveSessions_WhoError(t *testing.T) {
	mock := newMockExecutor()

	mock.set("tmux list-clients -F #{client_name} #{session_name}",
		nil, fmt.Errorf("no sessions"))
	mock.set("who", nil, fmt.Errorf("connection refused"))

	result, err := DetectActiveSessions(context.Background(), mock.run)
	if err == nil {
		t.Fatal("expected error for who connection failure")
	}
	if !strings.Contains(err.Error(), "checking active connections") {
		t.Errorf("error %q should mention active connections", err.Error())
	}
	if result != nil {
		t.Error("expected nil result on error")
	}
}

// ---------------------------------------------------------------------------
// Tests — isTmuxNoSessionsError
// ---------------------------------------------------------------------------

func TestIsTmuxNoSessionsError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"no server running", fmt.Errorf("no server running on /tmp/tmux-1000/default"), true},
		{"no sessions", fmt.Errorf("no sessions"), true},
		{"wrapped no sessions", fmt.Errorf("remote command failed: no sessions"), true},
		{"connection refused", fmt.Errorf("connection refused"), false},
		{"timeout", fmt.Errorf("connection timeout"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isTmuxNoSessionsError(tt.err)
			if got != tt.expected {
				t.Errorf("isTmuxNoSessionsError(%v) = %v, want %v", tt.err, got, tt.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Tests — ActiveSessions methods
// ---------------------------------------------------------------------------

func TestActiveSessions_HasActivity(t *testing.T) {
	tests := []struct {
		name     string
		sessions ActiveSessions
		want     bool
	}{
		{"empty", ActiveSessions{}, false},
		{"tmux only", ActiveSessions{TmuxClients: "/dev/pts/0 main"}, true},
		{"ssh only", ActiveSessions{SSHConnections: "user pts/0"}, true},
		{"claude only", ActiveSessions{ClaudeProcesses: "abc: claude"}, true},
		{"extend only", ActiveSessions{ExtendedUntil: func() *time.Time { t := time.Now(); return &t }()}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.sessions.HasActivity(); got != tt.want {
				t.Errorf("HasActivity() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestActiveSessions_Summary(t *testing.T) {
	// Empty summary.
	empty := &ActiveSessions{}
	if empty.Summary() != "" {
		t.Errorf("expected empty summary, got: %q", empty.Summary())
	}

	// Full summary.
	ts := time.Date(2025, 6, 15, 14, 30, 0, 0, time.UTC)
	full := &ActiveSessions{
		TmuxClients:     "/dev/pts/0 main",
		SSHConnections:  "ubuntu pts/0        2025-01-15 10:30 (192.168.1.100)",
		ClaudeProcesses: "abc123: 42 claude",
		ExtendedUntil:   &ts,
	}

	summary := full.Summary()
	if !strings.Contains(summary, "Tmux clients:") {
		t.Error("summary missing Tmux clients section")
	}
	if !strings.Contains(summary, "Active connections:") {
		t.Error("summary missing Active connections section")
	}
	if !strings.Contains(summary, "Claude processes in containers:") {
		t.Error("summary missing Claude processes section")
	}
	if !strings.Contains(summary, "Manual extend active until 2025-06-15T14:30:00Z") {
		t.Error("summary missing extend timestamp")
	}
}

func TestDetectActiveSessions_ClaudeCaseSensitive(t *testing.T) {
	// ADR-0018 specifies case-sensitive matching for "claude".
	mock := newMockExecutor()

	mock.set("tmux list-clients -F #{client_name} #{session_name}",
		nil, fmt.Errorf("no sessions"))
	mock.set("who", []byte(""), nil)
	mock.set("docker ps -q", []byte("ccc333\n"), nil)
	// "Claude" (uppercase C) should NOT match.
	mock.set("docker top ccc333 -o pid,comm",
		[]byte("PID COMMAND\n1 Claude\n"), nil)
	mock.set("cat "+ExtendTimestampPath,
		nil, fmt.Errorf("No such file or directory"))

	result, err := DetectActiveSessions(context.Background(), mock.run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// strings.Contains is case-sensitive, so "Claude" should not match "claude".
	// But actually "Claude" does NOT contain "claude" -- correct behavior.
	if result.ClaudeProcesses != "" {
		t.Errorf("expected empty ClaudeProcesses for 'Claude' (uppercase), got: %q", result.ClaudeProcesses)
	}
}

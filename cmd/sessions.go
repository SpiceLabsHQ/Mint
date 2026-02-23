package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/spf13/cobra"

	mintaws "github.com/nicholasgasior/mint/internal/aws"
	"github.com/nicholasgasior/mint/internal/cli"
	"github.com/nicholasgasior/mint/internal/vm"
)

// sessionsDeps holds the injectable dependencies for the sessions command.
type sessionsDeps struct {
	describe  mintaws.DescribeInstancesAPI
	sendKey   mintaws.SendSSHPublicKeyAPI
	owner     string
	remoteRun RemoteCommandRunner
}

// tmuxSession represents a parsed tmux session from the VM.
type tmuxSession struct {
	Name         string `json:"name"`
	Windows      int    `json:"windows"`
	Attached     bool   `json:"attached"`
	CreatedEpoch int64  `json:"created_epoch"`
	CreatedAt    string `json:"created_at"`
}

// newSessionsCommand creates the production sessions command.
func newSessionsCommand() *cobra.Command {
	return newSessionsCommandWithDeps(nil)
}

// newSessionsCommandWithDeps creates the sessions command with explicit
// dependencies for testing.
func newSessionsCommandWithDeps(deps *sessionsDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "sessions",
		Short: "List tmux sessions on the VM",
		Long:  "List active tmux sessions on the VM. Shows session name, window count, attached status, and creation time.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps != nil {
				return runSessions(cmd, deps)
			}
			clients := awsClientsFromContext(cmd.Context())
			if clients == nil {
				return fmt.Errorf("AWS clients not configured")
			}
			return runSessions(cmd, &sessionsDeps{
				describe:  clients.ec2Client,
				sendKey:   clients.icClient,
				owner:     clients.owner,
				remoteRun: defaultRemoteRunner,
			})
		},
	}
}

// runSessions executes the sessions command logic: discover VM, run tmux
// list-sessions remotely, parse output, and display results.
func runSessions(cmd *cobra.Command, deps *sessionsDeps) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	cliCtx := cli.FromCommand(cmd)
	vmName := "default"
	jsonOutput := false
	if cliCtx != nil {
		vmName = cliCtx.VM
		jsonOutput = cliCtx.JSON
	}

	// Discover VM by owner + VM name.
	found, err := vm.FindVM(ctx, deps.describe, deps.owner, vmName)
	if err != nil {
		return fmt.Errorf("discovering VM: %w", err)
	}
	if found == nil {
		return fmt.Errorf("no VM %q found — run mint up first to create one", vmName)
	}

	// Verify VM is running.
	if found.State != string(ec2types.InstanceStateNameRunning) {
		return fmt.Errorf("VM %q (%s) is not running (state: %s) — run mint up to start it",
			vmName, found.ID, found.State)
	}

	// Execute tmux list-sessions remotely.
	tmuxCmd := []string{
		"tmux", "list-sessions", "-F",
		"#{session_name} #{session_windows} #{session_attached} #{session_created}",
	}

	output, err := deps.remoteRun(
		ctx,
		deps.sendKey,
		found.ID,
		found.AvailabilityZone,
		found.PublicIP,
		defaultSSHPort,
		defaultSSHUser,
		tmuxCmd,
	)
	if err != nil {
		if isTmuxNoSessionsError(err) {
			return writeSessionsOutput(cmd.OutOrStdout(), nil, jsonOutput)
		}
		if isSSHConnectionError(err) {
			return fmt.Errorf(
				"cannot connect to VM %q (port 41122 refused).\n"+
					"Bootstrap may be incomplete — run 'mint doctor' for details.",
				vmName,
			)
		}
		return fmt.Errorf("listing tmux sessions: %w", err)
	}

	sessions := parseTmuxSessions(string(output))
	return writeSessionsOutput(cmd.OutOrStdout(), sessions, jsonOutput)
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
		strings.Contains(msg, "no sessions") ||
		strings.Contains(msg, "error connecting to") // Ubuntu 24.04 tmux: socket not found
}

// parseTmuxSessions parses the output of tmux list-sessions -F into
// structured session data. Each line has the format:
// <name> <windows> <attached> <created_epoch>
func parseTmuxSessions(output string) []tmuxSession {
	var sessions []tmuxSession

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Split from the right to handle session names with spaces.
		// Format: name windows attached created
		// The last 3 fields are always single tokens.
		parts := strings.Fields(line)
		if len(parts) < 4 {
			continue
		}

		// The last 3 parts are windows, attached, created_epoch.
		// Everything before is the session name.
		createdStr := parts[len(parts)-1]
		attachedStr := parts[len(parts)-2]
		windowsStr := parts[len(parts)-3]
		name := strings.Join(parts[:len(parts)-3], " ")

		windows, err := strconv.Atoi(windowsStr)
		if err != nil {
			continue
		}

		createdEpoch, err := strconv.ParseInt(createdStr, 10, 64)
		if err != nil {
			continue
		}

		attached := attachedStr == "1"
		createdAt := time.Unix(createdEpoch, 0).UTC().Format(time.RFC3339)

		sessions = append(sessions, tmuxSession{
			Name:         name,
			Windows:      windows,
			Attached:     attached,
			CreatedEpoch: createdEpoch,
			CreatedAt:    createdAt,
		})
	}

	return sessions
}

// writeSessionsOutput writes sessions as a human-readable table or JSON array.
func writeSessionsOutput(w io.Writer, sessions []tmuxSession, jsonOutput bool) error {
	if jsonOutput {
		return writeSessionsJSON(w, sessions)
	}
	writeSessionsHuman(w, sessions)
	return nil
}

// writeSessionsJSON outputs sessions as a JSON array.
func writeSessionsJSON(w io.Writer, sessions []tmuxSession) error {
	if sessions == nil {
		sessions = []tmuxSession{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(sessions)
}

// writeSessionsHuman outputs sessions as a human-readable table.
func writeSessionsHuman(w io.Writer, sessions []tmuxSession) {
	if len(sessions) == 0 {
		fmt.Fprintln(w, "No active sessions")
		return
	}

	fmt.Fprintf(w, "%-20s  %-8s  %-10s  %s\n", "SESSION", "WINDOWS", "STATUS", "CREATED")
	for _, s := range sessions {
		status := "detached"
		if s.Attached {
			status = "attached"
		}
		fmt.Fprintf(w, "%-20s  %-8d  %-10s  %s\n", s.Name, s.Windows, status, s.CreatedAt)
	}
}

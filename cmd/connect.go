package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2instanceconnect"
	"github.com/spf13/cobra"

	mintaws "github.com/nicholasgasior/mint/internal/aws"
	"github.com/nicholasgasior/mint/internal/cli"
	"github.com/nicholasgasior/mint/internal/config"
	"github.com/nicholasgasior/mint/internal/sshconfig"
	"github.com/nicholasgasior/mint/internal/vm"
)

// connectDeps holds the injectable dependencies for the connect command.
type connectDeps struct {
	describe       mintaws.DescribeInstancesAPI
	sendKey        mintaws.SendSSHPublicKeyAPI
	owner          string
	runner         CommandRunner
	lookupPath     func(string) (string, error)
	hostKeyStore   *sshconfig.HostKeyStore
	hostKeyScanner HostKeyScanner
	remoteRun      RemoteCommandRunner
	stdin          io.Reader // for testing the session picker
}

// newConnectCommand creates the production connect command.
func newConnectCommand() *cobra.Command {
	return newConnectCommandWithDeps(nil)
}

// newConnectCommandWithDeps creates the connect command with explicit
// dependencies for testing.
func newConnectCommandWithDeps(deps *connectDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "connect [session]",
		Short: "Connect to a tmux session on the VM via mosh",
		Long: "Connect to a tmux session on the VM using mosh + tmux. " +
			"If a session name is provided, creates or attaches to that session. " +
			"If no session name is given, lists available sessions and presents a picker.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps != nil {
				return runConnect(cmd, deps, args)
			}
			clients := awsClientsFromContext(cmd.Context())
			if clients == nil {
				return fmt.Errorf("AWS clients not configured")
			}
			configDir := config.DefaultConfigDir()
			return runConnect(cmd, &connectDeps{
				describe:       clients.ec2Client,
				sendKey:        clients.icClient,
				owner:          clients.owner,
				lookupPath:     exec.LookPath,
				hostKeyStore:   sshconfig.NewHostKeyStore(configDir),
				hostKeyScanner: defaultHostKeyScanner,
				remoteRun:      defaultRemoteRunner,
			}, args)
		},
	}

	return cmd
}

// runConnect executes the connect command logic. Path A (session name provided)
// connects directly. Path B (no session name) lists sessions and presents a
// picker before connecting.
func runConnect(cmd *cobra.Command, deps *connectDeps, args []string) error {
	// Check that mosh is installed locally before doing any AWS work.
	lookup := deps.lookupPath
	if lookup == nil {
		lookup = exec.LookPath
	}
	if _, err := lookup("mosh"); err != nil {
		return fmt.Errorf("mosh is not installed — install it with: brew install mosh (macOS) or apt install mosh (Linux)")
	}

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	cliCtx := cli.FromCommand(cmd)
	vmName := "default"
	if cliCtx != nil {
		vmName = cliCtx.VM
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

	// Determine session name: provided as arg, or picked interactively.
	var sessionName string
	if len(args) > 0 {
		sessionName = args[0]
	} else {
		// Path B: list sessions and pick one.
		selected, err := pickSession(ctx, cmd, deps, found)
		if err != nil {
			return err
		}
		sessionName = selected
	}

	// TOFU host key verification (ADR-0019).
	var knownHostsPath string
	if deps.hostKeyStore != nil && deps.hostKeyScanner != nil {
		fingerprint, hostKeyLine, scanErr := deps.hostKeyScanner(found.PublicIP, defaultSSHPort)
		if scanErr != nil {
			return fmt.Errorf("scanning host key: %w", scanErr)
		}

		matched, existing, checkErr := deps.hostKeyStore.CheckKey(vmName, fingerprint)
		if checkErr != nil {
			return fmt.Errorf("checking host key: %w", checkErr)
		}

		if existing == "" {
			// First connection — trust on first use.
			if err := deps.hostKeyStore.RecordKey(vmName, fingerprint); err != nil {
				return fmt.Errorf("recording host key: %w", err)
			}
		} else if !matched {
			return fmt.Errorf(
				"HOST KEY CHANGED for VM %q!\n\n"+
					"  Stored fingerprint: %s\n"+
					"  Current fingerprint: %s\n\n"+
					"This could indicate a man-in-the-middle attack, or the VM was rebuilt.\n"+
					"If this is expected (VM was rebuilt), run: mint destroy && mint up",
				vmName, existing, fingerprint,
			)
		}

		// Write a temporary known_hosts file with the host's actual key
		// so OpenSSH (invoked by mosh) can verify the connection.
		tmpKH, khErr := os.CreateTemp("", "mint-known-hosts-*")
		if khErr != nil {
			return fmt.Errorf("creating temp known_hosts: %w", khErr)
		}
		knownHostsPath = tmpKH.Name()
		defer os.Remove(knownHostsPath)

		hostEntry := fmt.Sprintf("[%s]:%d %s\n", found.PublicIP, defaultSSHPort, hostKeyLine)
		if _, err := tmpKH.WriteString(hostEntry); err != nil {
			tmpKH.Close()
			return fmt.Errorf("writing temp known_hosts: %w", err)
		}
		tmpKH.Close()
	}

	// Use availability zone from FindVM (already populated via DescribeInstances).
	if found.AvailabilityZone == "" {
		return fmt.Errorf("VM %q (%s) has no availability zone — this is unexpected, try mint destroy && mint up", vmName, found.ID)
	}

	// Generate ephemeral SSH key pair.
	pubKey, privKeyPath, cleanup, err := generateEphemeralKeyPair()
	if err != nil {
		return fmt.Errorf("generating ephemeral SSH key: %w", err)
	}
	defer cleanup()

	// Push public key via Instance Connect.
	_, err = deps.sendKey.SendSSHPublicKey(ctx, &ec2instanceconnect.SendSSHPublicKeyInput{
		InstanceId:       aws.String(found.ID),
		InstanceOSUser:   aws.String(defaultSSHUser),
		SSHPublicKey:     aws.String(pubKey),
		AvailabilityZone: aws.String(found.AvailabilityZone),
	})
	if err != nil {
		return fmt.Errorf("pushing SSH key via Instance Connect: %w", err)
	}

	// Build the ssh sub-command string for mosh --ssh="...".
	sshCmd := fmt.Sprintf("ssh -p %d -i %s", defaultSSHPort, privKeyPath)
	if knownHostsPath != "" {
		sshCmd += fmt.Sprintf(" -o StrictHostKeyChecking=yes -o UserKnownHostsFile=%s", knownHostsPath)
	} else {
		sshCmd += " -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null"
	}

	// Build mosh command arguments with tmux attach.
	moshArgs := []string{
		fmt.Sprintf("--ssh=%s", sshCmd),
		fmt.Sprintf("%s@%s", defaultSSHUser, found.PublicIP),
		"--",
		"tmux", "new-session", "-A", "-s", sessionName,
	}

	runner := deps.runner
	if runner == nil {
		runner = defaultRunner
	}

	return runner("mosh", moshArgs...)
}

// pickSession lists tmux sessions on the VM and returns the selected session
// name. If only one session exists, it is auto-selected. If multiple sessions
// exist, an interactive picker is presented.
func pickSession(ctx context.Context, cmd *cobra.Command, deps *connectDeps, found *vm.VM) (string, error) {
	remoteRun := deps.remoteRun
	if remoteRun == nil {
		remoteRun = defaultRemoteRunner
	}

	// Execute tmux list-sessions remotely.
	tmuxCmd := []string{
		"tmux", "list-sessions", "-F",
		"#{session_name} #{session_windows} #{session_attached} #{session_created}",
	}

	output, err := remoteRun(
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
			return "", fmt.Errorf("no active sessions — create one with: mint project add <git-url>")
		}
		return "", fmt.Errorf("listing tmux sessions: %w", err)
	}

	sessions := parseTmuxSessions(string(output))
	if len(sessions) == 0 {
		return "", fmt.Errorf("no active sessions — create one with: mint project add <git-url>")
	}

	// Single session: auto-select.
	if len(sessions) == 1 {
		return sessions[0].Name, nil
	}

	// Multiple sessions: present picker.
	w := cmd.OutOrStdout()
	fmt.Fprintln(w, "Active sessions:")
	for i, s := range sessions {
		status := "detached"
		if s.Attached {
			status = "attached"
		}
		fmt.Fprintf(w, "  %d. %-20s (%d %s, %s)\n",
			i+1, s.Name, s.Windows, pluralWindows(s.Windows), status)
	}
	fmt.Fprintf(w, "\nSelect session [1-%d]: ", len(sessions))

	// Read selection from stdin.
	stdinReader := deps.stdin
	if stdinReader == nil {
		stdinReader = os.Stdin
	}
	scanner := bufio.NewScanner(stdinReader)
	if !scanner.Scan() {
		return "", fmt.Errorf("no input received")
	}

	choice := strings.TrimSpace(scanner.Text())
	num, err := strconv.Atoi(choice)
	if err != nil || num < 1 || num > len(sessions) {
		return "", fmt.Errorf("invalid selection %q — enter a number between 1 and %d", choice, len(sessions))
	}

	return sessions[num-1].Name, nil
}

// pluralWindows returns "window" or "windows" based on count.
func pluralWindows(n int) string {
	if n == 1 {
		return "window"
	}
	return "windows"
}

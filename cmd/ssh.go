package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2instanceconnect"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"

	mintaws "github.com/SpiceLabsHQ/Mint/internal/aws"
	"github.com/SpiceLabsHQ/Mint/internal/cli"
	"github.com/SpiceLabsHQ/Mint/internal/config"
	"github.com/SpiceLabsHQ/Mint/internal/progress"
	"github.com/SpiceLabsHQ/Mint/internal/sshconfig"
	"github.com/SpiceLabsHQ/Mint/internal/vm"
)

// sshDeps holds the injectable dependencies for the ssh command.
type sshDeps struct {
	describe       mintaws.DescribeInstancesAPI
	sendKey        mintaws.SendSSHPublicKeyAPI
	owner          string
	runner         CommandRunner
	hostKeyStore   *sshconfig.HostKeyStore
	hostKeyScanner HostKeyScanner
}

// newSSHCommand creates the production ssh command.
func newSSHCommand() *cobra.Command {
	return newSSHCommandWithDeps(nil)
}

// newSSHCommandWithDeps creates the ssh command with explicit dependencies
// for testing.
func newSSHCommandWithDeps(deps *sshDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ssh [-- extra-ssh-args]",
		Short: "SSH into the VM using ephemeral keys",
		Long: "Connect to the VM via SSH using EC2 Instance Connect ephemeral keys (ADR-0007). " +
			"Extra SSH arguments can be passed after --, for example: mint ssh -- -L 8080:localhost:8080",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps != nil {
				return runSSH(cmd, deps, args)
			}
			clients := awsClientsFromContext(cmd.Context())
			if clients == nil {
				return fmt.Errorf("AWS clients not configured")
			}
			configDir := config.DefaultConfigDir()
			return runSSH(cmd, &sshDeps{
				describe:       clients.ec2Client,
				sendKey:        clients.icClient,
				owner:          clients.owner,
				hostKeyStore:   sshconfig.NewHostKeyStore(configDir),
				hostKeyScanner: defaultHostKeyScanner,
			}, args)
		},
	}

	return cmd
}

// runSSH executes the ssh command logic: discover VM, verify running,
// generate ephemeral key, push via Instance Connect, exec ssh.
func runSSH(cmd *cobra.Command, deps *sshDeps, extraArgs []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	cliCtx := cli.FromCommand(cmd)
	vmName := "default"
	if cliCtx != nil {
		vmName = cliCtx.VM
	}

	sp := progress.NewCommandSpinner(cmd.OutOrStdout(), false)

	// Discover VM by owner + VM name.
	sp.Start("Looking up VM...")
	found, err := vm.FindVM(ctx, deps.describe, deps.owner, vmName)
	if err != nil {
		sp.Fail(err.Error())
		return fmt.Errorf("discovering VM: %w", err)
	}
	if found == nil {
		sp.Stop("")
		return fmt.Errorf("no VM %q found — run mint up first to create one", vmName)
	}

	// Verify VM is running.
	if found.State != string(ec2types.InstanceStateNameRunning) {
		sp.Stop("")
		return fmt.Errorf("VM %q (%s) is not running (state: %s) — run mint up to start it",
			vmName, found.ID, found.State)
	}

	// Check bootstrap status before attempting any SSH operation (ADR-0001).
	// The SSH daemon is not listening until bootstrap completes.
	sp.Update("Checking bootstrap status...")
	switch found.BootstrapStatus {
	case "pending":
		sp.Stop("")
		return fmt.Errorf(
			"VM %q bootstrap is not complete (status: pending).\n"+
				"Run 'mint doctor' for details or 'mint recreate' to rebuild.",
			vmName,
		)
	case "failed":
		sp.Stop("")
		return fmt.Errorf(
			"VM %q bootstrap failed.\nRun 'mint recreate' to rebuild.",
			vmName,
		)
	}

	// Use availability zone from FindVM (already populated via DescribeInstances).
	if found.AvailabilityZone == "" {
		sp.Stop("")
		return fmt.Errorf("VM %q (%s) has no availability zone — this is unexpected, try mint destroy && mint up", vmName, found.ID)
	}

	// Spinner must be fully stopped before exec — exec replaces the process and
	// any residual goroutine would leave the terminal in a dirty state.
	sp.Stop("")

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
		// so OpenSSH can verify the connection.
		tmpKH, khErr := os.CreateTemp("", "mint-known-hosts-*")
		if khErr != nil {
			return fmt.Errorf("creating temp known_hosts: %w", khErr)
		}
		knownHostsPath = tmpKH.Name()
		defer os.Remove(knownHostsPath)

		// Write the host key line in OpenSSH known_hosts format.
		hostEntry := fmt.Sprintf("[%s]:%d %s\n", found.PublicIP, defaultSSHPort, hostKeyLine)
		if _, err := tmpKH.WriteString(hostEntry); err != nil {
			tmpKH.Close()
			return fmt.Errorf("writing temp known_hosts: %w", err)
		}
		tmpKH.Close()
	}

	// Build ssh command arguments.
	sshArgs := []string{
		"-i", privKeyPath,
		"-p", fmt.Sprintf("%d", defaultSSHPort),
	}
	if knownHostsPath != "" {
		sshArgs = append(sshArgs,
			"-o", "StrictHostKeyChecking=yes",
			"-o", fmt.Sprintf("UserKnownHostsFile=%s", knownHostsPath),
		)
	} else {
		// Fallback when no TOFU store configured (backward compat).
		sshArgs = append(sshArgs,
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
		)
	}
	sshArgs = append(sshArgs, fmt.Sprintf("%s@%s", defaultSSHUser, found.PublicIP))
	sshArgs = append(sshArgs, extraArgs...)

	runner := deps.runner
	if runner == nil {
		runner = defaultRunner
	}

	return runner("ssh", sshArgs...)
}

// defaultHostKeyScanner runs ssh-keyscan to fetch a host's SSH public key
// and returns its SHA256 fingerprint and the raw key line (type + base64 data).
func defaultHostKeyScanner(host string, port int) (fingerprint string, hostKeyLine string, err error) {
	cmd := exec.Command("ssh-keyscan", "-p", fmt.Sprintf("%d", port), "-T", "5", "-t", "ed25519", host)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", "", fmt.Errorf("ssh-keyscan failed: %w", err)
	}

	output := stdout.String()
	if output == "" {
		return "", "", fmt.Errorf("ssh-keyscan returned no keys for %s:%d", host, port)
	}

	// Parse the first valid key line. Format: "host key-type base64-data"
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Split into: host, key-type, base64-data
		parts := strings.SplitN(line, " ", 3)
		if len(parts) < 3 {
			continue
		}
		keyLine := parts[1] + " " + parts[2] // "key-type base64-data"

		// Parse the public key to compute its fingerprint.
		pubKey, _, _, _, parseErr := ssh.ParseAuthorizedKey([]byte(line))
		if parseErr != nil {
			continue
		}

		fp := ssh.FingerprintSHA256(pubKey)
		return fp, keyLine, nil
	}

	return "", "", fmt.Errorf("ssh-keyscan returned no parseable keys for %s:%d", host, port)
}

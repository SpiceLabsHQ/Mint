package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"

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

// moshDeps holds the injectable dependencies for the mosh command.
type moshDeps struct {
	describe       mintaws.DescribeInstancesAPI
	sendKey        mintaws.SendSSHPublicKeyAPI
	owner          string
	runner         CommandRunner
	lookupPath     func(string) (string, error)
	hostKeyStore   *sshconfig.HostKeyStore
	hostKeyScanner HostKeyScanner
}

// newMoshCommand creates the production mosh command.
func newMoshCommand() *cobra.Command {
	return newMoshCommandWithDeps(nil)
}

// newMoshCommandWithDeps creates the mosh command with explicit dependencies
// for testing.
func newMoshCommandWithDeps(deps *moshDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mosh",
		Short: "Open a mosh session to the VM using ephemeral keys",
		Long: "Connect to the VM via mosh using EC2 Instance Connect ephemeral keys (ADR-0007). " +
			"Mosh provides a roaming, intermittent-connectivity SSH session — ideal for iPads and " +
			"unreliable networks.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps != nil {
				return runMosh(cmd, deps)
			}
			clients := awsClientsFromContext(cmd.Context())
			if clients == nil {
				return fmt.Errorf("AWS clients not configured")
			}
			configDir := config.DefaultConfigDir()
			return runMosh(cmd, &moshDeps{
				describe:       clients.ec2Client,
				sendKey:        clients.icClient,
				owner:          clients.owner,
				lookupPath:     exec.LookPath,
				hostKeyStore:   sshconfig.NewHostKeyStore(configDir),
				hostKeyScanner: defaultHostKeyScanner,
			})
		},
	}

	return cmd
}

// runMosh executes the mosh command logic: check mosh binary, discover VM,
// verify running, generate ephemeral key, push via Instance Connect, exec mosh.
func runMosh(cmd *cobra.Command, deps *moshDeps) error {
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

	// Look up availability zone from the instance (needed for SendSSHPublicKey).
	az, err := lookupInstanceAZ(ctx, deps.describe, found.ID)
	if err != nil {
		return fmt.Errorf("looking up availability zone: %w", err)
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
		AvailabilityZone: aws.String(az),
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

	// Build mosh command arguments.
	moshArgs := []string{
		fmt.Sprintf("--ssh=%s", sshCmd),
		fmt.Sprintf("%s@%s", defaultSSHUser, found.PublicIP),
	}

	runner := deps.runner
	if runner == nil {
		runner = defaultRunner
	}

	return runner("mosh", moshArgs...)
}

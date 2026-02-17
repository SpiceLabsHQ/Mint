package cmd

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2instanceconnect"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"

	mintaws "github.com/nicholasgasior/mint/internal/aws"
	"github.com/nicholasgasior/mint/internal/cli"
	"github.com/nicholasgasior/mint/internal/vm"
)

// CommandRunner is a function type that executes an external command.
// It enables testing without actually exec'ing ssh/code binaries.
type CommandRunner func(name string, args ...string) error

// defaultRunner executes the command using os/exec, connecting
// stdin/stdout/stderr to the parent process.
func defaultRunner(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// sshDeps holds the injectable dependencies for the ssh command.
type sshDeps struct {
	describe mintaws.DescribeInstancesAPI
	sendKey  mintaws.SendSSHPublicKeyAPI
	owner    string
	runner   CommandRunner
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
			if deps == nil {
				return fmt.Errorf("AWS clients not configured (not yet wired for production use)")
			}
			return runSSH(cmd, deps, args)
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

	// Build ssh command arguments.
	sshArgs := []string{
		"-i", privKeyPath,
		"-p", fmt.Sprintf("%d", defaultSSHPort),
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		fmt.Sprintf("%s@%s", defaultSSHUser, found.PublicIP),
	}
	sshArgs = append(sshArgs, extraArgs...)

	runner := deps.runner
	if runner == nil {
		runner = defaultRunner
	}

	return runner("ssh", sshArgs...)
}

// lookupInstanceAZ queries DescribeInstances for a single instance ID
// and returns its availability zone from the Placement field.
func lookupInstanceAZ(ctx context.Context, client mintaws.DescribeInstancesAPI, instanceID string) (string, error) {
	out, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		return "", fmt.Errorf("describe instance %s: %w", instanceID, err)
	}

	for _, res := range out.Reservations {
		for _, inst := range res.Instances {
			if inst.Placement != nil && inst.Placement.AvailabilityZone != nil {
				return aws.ToString(inst.Placement.AvailabilityZone), nil
			}
		}
	}

	return "", fmt.Errorf("no placement info for instance %s", instanceID)
}

// generateEphemeralKeyPair generates a temporary ed25519 SSH key pair.
// It returns the public key in OpenSSH authorized_keys format, the path
// to the temporary private key file, a cleanup function, and any error.
func generateEphemeralKeyPair() (pubKeyStr string, privKeyPath string, cleanup func(), err error) {
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", nil, fmt.Errorf("generate ed25519 key: %w", err)
	}

	// Convert public key to OpenSSH authorized_keys format.
	sshPubKey, err := ssh.NewPublicKey(pubKey)
	if err != nil {
		return "", "", nil, fmt.Errorf("convert public key: %w", err)
	}
	pubKeyStr = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPubKey)))

	// Marshal private key to PEM and write to temp file.
	privKeyBytes, err := ssh.MarshalPrivateKey(privKey, "")
	if err != nil {
		return "", "", nil, fmt.Errorf("marshal private key: %w", err)
	}

	tmpFile, err := os.CreateTemp("", "mint-ssh-key-*")
	if err != nil {
		return "", "", nil, fmt.Errorf("create temp key file: %w", err)
	}

	if err := os.Chmod(tmpFile.Name(), 0o600); err != nil {
		os.Remove(tmpFile.Name())
		return "", "", nil, fmt.Errorf("chmod temp key file: %w", err)
	}

	if err := pem.Encode(tmpFile, privKeyBytes); err != nil {
		os.Remove(tmpFile.Name())
		return "", "", nil, fmt.Errorf("write private key: %w", err)
	}
	tmpFile.Close()

	privKeyPath = tmpFile.Name()
	cleanup = func() { os.Remove(privKeyPath) }

	return pubKeyStr, privKeyPath, cleanup, nil
}

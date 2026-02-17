package cmd

import (
	"bytes"
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
	"github.com/aws/aws-sdk-go-v2/service/ec2instanceconnect"
	"golang.org/x/crypto/ssh"

	mintaws "github.com/nicholasgasior/mint/internal/aws"
	"github.com/nicholasgasior/mint/internal/sshconfig"
)

// HostKeyScanner is a function type that scans a remote host for its SSH
// host key and returns the SHA256 fingerprint. Production implementation
// uses ssh-keyscan; tests inject a mock.
type HostKeyScanner func(host string, port int) (fingerprint string, hostKeyLine string, err error)

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

// RemoteCommandRunner executes a command on a remote VM via SSH using
// Instance Connect ephemeral keys. It pushes a temporary public key,
// runs the command, and returns the captured stdout. Unlike CommandRunner
// (which execs an interactive session), this captures output for
// programmatic use by commands like sessions, extend, and project.
type RemoteCommandRunner func(
	ctx context.Context,
	sendKey mintaws.SendSSHPublicKeyAPI,
	instanceID string,
	az string,
	host string,
	port int,
	user string,
	command []string,
) ([]byte, error)

// defaultRemoteRunner is the production implementation of RemoteCommandRunner.
// It generates an ephemeral key pair, pushes the public key via Instance
// Connect, and runs the command over SSH, capturing stdout.
func defaultRemoteRunner(
	ctx context.Context,
	sendKey mintaws.SendSSHPublicKeyAPI,
	instanceID string,
	az string,
	host string,
	port int,
	user string,
	command []string,
) ([]byte, error) {
	// Generate ephemeral key pair.
	pubKey, privKeyPath, cleanup, err := generateEphemeralKeyPair()
	if err != nil {
		return nil, fmt.Errorf("generating ephemeral SSH key: %w", err)
	}
	defer cleanup()

	// Push public key via Instance Connect.
	_, err = sendKey.SendSSHPublicKey(ctx, &ec2instanceconnect.SendSSHPublicKeyInput{
		InstanceId:       aws.String(instanceID),
		InstanceOSUser:   aws.String(user),
		SSHPublicKey:     aws.String(pubKey),
		AvailabilityZone: aws.String(az),
	})
	if err != nil {
		return nil, fmt.Errorf("pushing SSH key via Instance Connect: %w", err)
	}

	// Build ssh command for non-interactive execution.
	sshArgs := []string{
		"-i", privKeyPath,
		"-p", fmt.Sprintf("%d", port),
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
		fmt.Sprintf("%s@%s", user, host),
	}
	sshArgs = append(sshArgs, command...)

	cmd := exec.CommandContext(ctx, "ssh", sshArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("remote command failed: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}

	return stdout.Bytes(), nil
}

// TOFURemoteRunner wraps a RemoteCommandRunner with TOFU host key
// verification (ADR-0019). It runs ssh-keyscan once on the first call
// and caches the result for subsequent calls in the same command
// invocation. Write commands (key add, project add, project rebuild)
// use this to verify the host before modifying state on the VM.
type TOFURemoteRunner struct {
	inner          RemoteCommandRunner
	hostKeyStore   *sshconfig.HostKeyStore
	hostKeyScanner HostKeyScanner
	vmName         string
	verified       bool
}

// NewTOFURemoteRunner creates a TOFURemoteRunner that verifies the host
// key via TOFU before delegating to the inner runner.
func NewTOFURemoteRunner(
	inner RemoteCommandRunner,
	store *sshconfig.HostKeyStore,
	scanner HostKeyScanner,
	vmName string,
) *TOFURemoteRunner {
	return &TOFURemoteRunner{
		inner:          inner,
		hostKeyStore:   store,
		hostKeyScanner: scanner,
		vmName:         vmName,
	}
}

// Run executes a remote command with TOFU verification on the first call.
// Subsequent calls reuse the cached verification result. If the host key
// has changed since it was first recorded, Run returns an error and does
// not execute the command.
func (t *TOFURemoteRunner) Run(
	ctx context.Context,
	sendKey mintaws.SendSSHPublicKeyAPI,
	instanceID string,
	az string,
	host string,
	port int,
	user string,
	command []string,
) ([]byte, error) {
	if !t.verified {
		if err := t.verifyHostKey(host, port); err != nil {
			return nil, err
		}
		t.verified = true
	}
	return t.inner(ctx, sendKey, instanceID, az, host, port, user, command)
}

// verifyHostKey implements the TOFU logic: scan the host key, check
// against the store, record on first use, reject on mismatch.
func (t *TOFURemoteRunner) verifyHostKey(host string, port int) error {
	fingerprint, _, scanErr := t.hostKeyScanner(host, port)
	if scanErr != nil {
		return fmt.Errorf("scanning host key: %w", scanErr)
	}

	matched, existing, checkErr := t.hostKeyStore.CheckKey(t.vmName, fingerprint)
	if checkErr != nil {
		return fmt.Errorf("checking host key: %w", checkErr)
	}

	if existing == "" {
		// First connection -- trust on first use.
		if err := t.hostKeyStore.RecordKey(t.vmName, fingerprint); err != nil {
			return fmt.Errorf("recording host key: %w", err)
		}
		return nil
	}

	if !matched {
		return fmt.Errorf(
			"HOST KEY CHANGED for VM %q!\n\n"+
				"  Stored fingerprint: %s\n"+
				"  Current fingerprint: %s\n\n"+
				"This could indicate a man-in-the-middle attack, or the VM was rebuilt.\n"+
				"If this is expected (VM was rebuilt), run: mint destroy && mint up",
			t.vmName, existing, fingerprint,
		)
	}

	return nil
}

// isTOFUError returns true if the error is a TOFU host key verification
// error that should be propagated directly rather than masked by
// command-specific error wrapping.
func isTOFUError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "HOST KEY CHANGED") ||
		strings.Contains(msg, "scanning host key") ||
		strings.Contains(msg, "checking host key") ||
		strings.Contains(msg, "recording host key")
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

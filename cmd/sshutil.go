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

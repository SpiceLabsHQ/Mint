package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"

	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/spf13/cobra"

	mintaws "github.com/SpiceLabsHQ/Mint/internal/aws"
	"github.com/SpiceLabsHQ/Mint/internal/cli"
	"github.com/SpiceLabsHQ/Mint/internal/config"
	"github.com/SpiceLabsHQ/Mint/internal/sshconfig"
	"github.com/SpiceLabsHQ/Mint/internal/tags"
	"github.com/SpiceLabsHQ/Mint/internal/vm"
)

// validKeyPrefixes lists the accepted SSH public key type prefixes.
var validKeyPrefixes = []string{
	"ssh-rsa",
	"ssh-ed25519",
	"ecdsa-sha2-",
	"ssh-dss",
	"sk-ssh-",
}

// keyAddDeps holds the injectable dependencies for the key add command.
type keyAddDeps struct {
	describe       mintaws.DescribeInstancesAPI
	sendKey        mintaws.SendSSHPublicKeyAPI
	owner          string
	remoteRunner   RemoteCommandRunner
	hostKeyStore   *sshconfig.HostKeyStore
	hostKeyScanner HostKeyScanner
	fingerprintFn  func(key string) (string, error)
}

// newKeyCommand creates the parent key command with subcommands.
func newKeyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "key",
		Short: "Manage SSH keys",
		Long:  "Manage SSH keys on the VM. Use subcommands to add keys.",
	}

	cmd.AddCommand(newKeyAddCommand())

	return cmd
}

// newKeyAddCommand creates the production key add command.
func newKeyAddCommand() *cobra.Command {
	return newKeyAddCommandWithDeps(nil)
}

// newKeyAddCommandWithDeps creates the key add command with explicit
// dependencies for testing.
func newKeyAddCommandWithDeps(deps *keyAddDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <public-key>",
		Short: "Add an SSH public key to the VM",
		Long: "Add an SSH public key to the VM's ~/.ssh/authorized_keys. " +
			"The argument can be a file path, a key string, or - for stdin.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps != nil {
				return runKeyAdd(cmd, deps, args[0])
			}
			clients := awsClientsFromContext(cmd.Context())
			if clients == nil {
				return fmt.Errorf("AWS clients not configured")
			}
			configDir := config.DefaultConfigDir()
			return runKeyAdd(cmd, &keyAddDeps{
				describe:       clients.ec2Client,
				sendKey:        clients.icClient,
				owner:          clients.owner,
				remoteRunner:   defaultRemoteRunner,
				hostKeyStore:   sshconfig.NewHostKeyStore(configDir),
				hostKeyScanner: defaultHostKeyScanner,
				fingerprintFn:  computeKeyFingerprint,
			}, args[0])
		},
	}

	return cmd
}

// runKeyAdd executes the key add command logic.
func runKeyAdd(cmd *cobra.Command, deps *keyAddDeps, arg string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	// Read the public key from the argument.
	pubKey, err := resolvePublicKey(cmd, arg)
	if err != nil {
		return err
	}

	// Validate the key format.
	if err := validatePublicKey(pubKey); err != nil {
		return err
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

	// Check bootstrap status before attempting any SSH/keyscan operation (ADR-0001).
	// The SSH daemon is not listening until bootstrap completes.
	switch found.BootstrapStatus {
	case tags.BootstrapPending:
		return fmt.Errorf(
			"VM %q bootstrap is not complete (status: pending).\n"+
				"Run 'mint doctor' for details or 'mint recreate' to rebuild.",
			vmName,
		)
	case tags.BootstrapFailed:
		return fmt.Errorf(
			"VM %q bootstrap failed.\nRun 'mint recreate' to rebuild.",
			vmName,
		)
	}

	// Build a TOFU-verified remote runner for write commands (ADR-0019).
	// The runner verifies the host key on the first remote call and caches
	// the result for subsequent calls in this invocation.
	remote := deps.remoteRunner
	if deps.hostKeyStore != nil && deps.hostKeyScanner != nil {
		tofu := NewTOFURemoteRunner(deps.remoteRunner, deps.hostKeyStore, deps.hostKeyScanner, vmName)
		remote = tofu.Run
	}

	// Check if key already exists on the VM. The entire compound command is
	// passed as a single string element so that SSH transmits it verbatim to
	// the remote shell. When the command slice has multiple elements SSH joins
	// them with spaces, causing the remote shell to treat only the first word
	// as the -c script and silently discard the rest of the compound command.
	//
	// The key is embedded with single-quote wrapping. validatePublicKey above
	// ensures the key contains no single quotes (only alphanumeric, +, /, =,
	// @, ., _, :, comma, hyphen, and space are permitted), so the quoting is
	// safe against injection.
	authKeysDir := fmt.Sprintf("/home/%s/.ssh", defaultSSHUser)
	authKeysPath := fmt.Sprintf("%s/authorized_keys", authKeysDir)
	quotedKey := "'" + pubKey + "'"
	grepOutput, grepErr := remote(
		ctx,
		deps.sendKey,
		found.ID,
		found.AvailabilityZone,
		found.PublicIP,
		defaultSSHPort,
		defaultSSHUser,
		[]string{fmt.Sprintf(`grep -F %s %s 2>/dev/null || true`, quotedKey, authKeysPath)},
	)
	if grepErr != nil {
		return fmt.Errorf("checking existing keys: %w", grepErr)
	}

	// Compute fingerprint for user feedback.
	fp := ""
	if deps.fingerprintFn != nil {
		fp, _ = deps.fingerprintFn(pubKey)
	}

	// If grep found the key, it already exists.
	if len(strings.TrimSpace(string(grepOutput))) > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "Key %s already exists on VM %q\n", fp, vmName)
		return nil
	}

	// Append the key to authorized_keys. The entire compound command is a
	// single string so the remote shell receives it intact. mkdir -p ensures
	// the .ssh directory exists on fresh VMs.
	_, appendErr := remote(
		ctx,
		deps.sendKey,
		found.ID,
		found.AvailabilityZone,
		found.PublicIP,
		defaultSSHPort,
		defaultSSHUser,
		[]string{fmt.Sprintf(`mkdir -p %s && printf '%%s\n' %s >> %s`, authKeysDir, quotedKey, authKeysPath)},
	)
	if appendErr != nil {
		return fmt.Errorf("adding key to authorized_keys: %w", appendErr)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Added key %s to VM %q\n", fp, vmName)
	return nil
}

// resolvePublicKey reads the public key from the given argument.
// If arg is "-", reads from stdin. If arg is a file path, reads the file.
// Otherwise, treats arg as the key content itself.
func resolvePublicKey(cmd *cobra.Command, arg string) (string, error) {
	if arg == "-" {
		data, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return "", fmt.Errorf("reading key from stdin: %w", err)
		}
		return strings.TrimSpace(string(data)), nil
	}

	// Try reading as a file path first.
	if _, err := os.Stat(arg); err == nil {
		data, readErr := os.ReadFile(arg)
		if readErr != nil {
			return "", fmt.Errorf("reading key file %s: %w", arg, readErr)
		}
		return strings.TrimSpace(string(data)), nil
	}

	// Treat as inline key content.
	return strings.TrimSpace(arg), nil
}

// sshPubKeyCharPattern matches the safe character set for SSH public keys:
// base64 body ([A-Za-z0-9+/=]), key type prefixes ([-]), comments ([@._]),
// and field separators (space). No shell metacharacters are permitted.
var sshPubKeyCharPattern = regexp.MustCompile(`^[A-Za-z0-9+/=@. _:,-]+$`)

// validatePublicKey checks that the key string has a valid SSH public key prefix
// and contains only characters safe for SSH public keys. The character check is
// defense-in-depth against shell injection even though the append command also
// avoids interpolating user input into shell strings.
func validatePublicKey(key string) error {
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("public key is empty")
	}

	validPrefix := false
	for _, prefix := range validKeyPrefixes {
		if strings.HasPrefix(key, prefix) {
			validPrefix = true
			break
		}
	}
	if !validPrefix {
		return fmt.Errorf("invalid SSH public key format: must start with one of %s",
			strings.Join(validKeyPrefixes, ", "))
	}

	if !sshPubKeyCharPattern.MatchString(key) {
		return fmt.Errorf("public key contains invalid characters: only alphanumeric, +, /, =, @, ., _, :, comma, hyphen, and spaces are allowed")
	}

	return nil
}

// computeKeyFingerprint computes the SHA256 fingerprint of an SSH public key
// using ssh-keygen. This is the production implementation; tests inject a mock.
func computeKeyFingerprint(key string) (string, error) {
	// Use ssh-keygen -lf /dev/stdin to compute the fingerprint.
	cmd := exec.Command("ssh-keygen", "-lf", "-")
	cmd.Stdin = strings.NewReader(key + "\n")
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("computing key fingerprint: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}

	// Output format: "256 SHA256:... comment (ED25519)"
	// Extract the fingerprint (second field).
	parts := strings.Fields(stdout.String())
	if len(parts) < 2 {
		return "", fmt.Errorf("unexpected ssh-keygen output: %s", stdout.String())
	}

	return parts[1], nil
}

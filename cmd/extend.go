package cmd

import (
	"context"
	"fmt"
	"strconv"
	"time"

	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/spf13/cobra"

	mintaws "github.com/SpiceLabsHQ/Mint/internal/aws"
	"github.com/SpiceLabsHQ/Mint/internal/cli"
	"github.com/SpiceLabsHQ/Mint/internal/config"
	"github.com/SpiceLabsHQ/Mint/internal/progress"
	"github.com/SpiceLabsHQ/Mint/internal/vm"
)


// validateExtendArgs is a cobra Args function that validates the optional
// [minutes] argument before AWS initialization runs in PersistentPreRunE.
// This ensures argument errors are reported immediately rather than after a
// (potentially slow or failing) credential check.
func validateExtendArgs(_ *cobra.Command, args []string) error {
	if len(args) > 1 {
		return fmt.Errorf("accepts at most 1 arg(s), received %d", len(args))
	}
	if len(args) == 1 {
		n, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid minutes %q: must be a number", args[0])
		}
		if n < 15 {
			return fmt.Errorf("minutes must be >= 15 (got %d)", n)
		}
	}
	return nil
}

// extendDeps holds the injectable dependencies for the extend command.
type extendDeps struct {
	describe    mintaws.DescribeInstancesAPI
	sendKey     mintaws.SendSSHPublicKeyAPI
	owner       string
	remote      RemoteCommandRunner
	idleTimeout int // default minutes from config
}

// newExtendCommand creates the production extend command.
func newExtendCommand() *cobra.Command {
	return newExtendCommandWithDeps(nil)
}

// newExtendCommandWithDeps creates the extend command with explicit
// dependencies for testing.
func newExtendCommandWithDeps(deps *extendDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "extend [minutes]",
		Short: "Extend the VM idle auto-stop timer",
		Long: "Reset the idle auto-stop timer on the VM. " +
			"Defaults to the configured idle_timeout_minutes (from config). " +
			"Pass a number of minutes as an argument to override the default. " +
			"Minimum value is 15 minutes.",
		Args: validateExtendArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps != nil {
				return runExtend(cmd, deps, args)
			}
			clients := awsClientsFromContext(cmd.Context())
			if clients == nil {
				return fmt.Errorf("AWS clients not configured")
			}
			idleTimeout := 60
			if clients.mintConfig != nil {
				idleTimeout = clients.mintConfig.IdleTimeoutMinutes
			} else {
				cfg, err := config.Load(config.DefaultConfigDir())
				if err == nil && cfg != nil {
					idleTimeout = cfg.IdleTimeoutMinutes
				}
			}
			return runExtend(cmd, &extendDeps{
				describe:    clients.ec2Client,
				sendKey:     clients.icClient,
				owner:       clients.owner,
				remote:      defaultRemoteRunner,
				idleTimeout: idleTimeout,
			}, args)
		},
	}
}

// runExtend executes the extend command logic: parse minutes, discover VM,
// run remote command to write the extended-until timestamp.
func runExtend(cmd *cobra.Command, deps *extendDeps, args []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	// Determine minutes: positional arg or config default.
	minutes := deps.idleTimeout
	if len(args) > 0 {
		n, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid minutes %q: must be a number", args[0])
		}
		minutes = n
	}

	// Validate minimum (matches config validation: >= 15).
	if minutes < 15 {
		return fmt.Errorf("minutes must be >= 15 (got %d)", minutes)
	}

	cliCtx := cli.FromCommand(cmd)
	vmName := "default"
	verbose := false
	if cliCtx != nil {
		vmName = cliCtx.VM
		verbose = cliCtx.Verbose
	}

	sp := progress.NewCommandSpinner(cmd.OutOrStdout(), verbose)

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

	// Build the remote command to write the future timestamp.
	seconds := minutes * 60
	remoteCmd := []string{
		"bash", "-c",
		fmt.Sprintf("echo $(($(date +%%s) + %d)) | sudo tee /var/lib/mint/idle-extended-until", seconds),
	}

	// Execute remote command via SSH.
	sp.Update("Extending session...")
	_, err = deps.remote(
		ctx,
		deps.sendKey,
		found.ID,
		found.AvailabilityZone,
		found.PublicIP,
		defaultSSHPort,
		defaultSSHUser,
		remoteCmd,
	)
	if err != nil {
		sp.Fail(err.Error())
		if isSSHConnectionError(err) {
			return fmt.Errorf(
				"cannot connect to VM %q (port 41122 refused).\n"+
					"Bootstrap may be incomplete — run 'mint doctor' for details.",
				vmName,
			)
		}
		return fmt.Errorf("extending idle timer: %w", err)
	}

	sp.Stop("")

	// Compute the approximate expiry time for the success message.
	expiry := time.Now().Add(time.Duration(minutes) * time.Minute)

	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "Extended idle timer by %d minutes (until %s)\n",
		minutes, expiry.Format("15:04 local time"))

	return nil
}

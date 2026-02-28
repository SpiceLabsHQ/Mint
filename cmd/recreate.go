package cmd

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/spf13/cobra"

	mintaws "github.com/SpiceLabsHQ/Mint/internal/aws"
	"github.com/SpiceLabsHQ/Mint/internal/bootstrap"
	"github.com/SpiceLabsHQ/Mint/internal/cli"
	"github.com/SpiceLabsHQ/Mint/internal/config"
	"github.com/SpiceLabsHQ/Mint/internal/progress"
	"github.com/SpiceLabsHQ/Mint/internal/provision"
	"github.com/SpiceLabsHQ/Mint/internal/session"
	"github.com/SpiceLabsHQ/Mint/internal/sshconfig"
	"github.com/SpiceLabsHQ/Mint/internal/tags"
	"github.com/SpiceLabsHQ/Mint/internal/vm"
)

// recreateDeps holds the injectable dependencies for the recreate command.
type recreateDeps struct {
	describe            mintaws.DescribeInstancesAPI
	sendKey             mintaws.SendSSHPublicKeyAPI
	remoteRun           RemoteCommandRunner
	owner               string
	ownerARN            string
	stop                mintaws.StopInstancesAPI
	terminate           mintaws.TerminateInstancesAPI
	detachVolume        mintaws.DetachVolumeAPI
	waitVolumeAvailable mintaws.WaitVolumeAvailableAPI
	describeVolumes     mintaws.DescribeVolumesAPI
	run                 mintaws.RunInstancesAPI
	attachVolume        mintaws.AttachVolumeAPI
	createTags          mintaws.CreateTagsAPI
	deleteTags          provision.DeleteTagsAPI
	describeSubnets     mintaws.DescribeSubnetsAPI
	describeSGs         mintaws.DescribeSecurityGroupsAPI
	describeImages      mintaws.DescribeImagesAPI
	waitRunning         mintaws.WaitInstanceRunningAPI
	describeFS          mintaws.DescribeFileSystemsAPI
	describeAddrs       mintaws.DescribeAddressesAPI
	associateAddr       mintaws.AssociateAddressAPI
	disassociateAddr    mintaws.DisassociateAddressAPI
	bootstrapScript      []byte
	bootstrapURL         string // GitHub raw URL for bootstrap.sh delivery
	userBootstrapScript  []byte // Optional user-bootstrap.sh content read from config dir
	mintConfig           *config.Config
	pollBootstrap       provision.BootstrapPollFunc
	resolveAMI          provision.AMIResolver
	verifyBootstrap     provision.BootstrapVerifier
	removeHostKey       func(vmName string) error
}

// WithWaitVolumeAvailable sets the waiter used to poll until the EBS volume
// reaches the available state. Call this to override the default (no-op) waiter.
func (d *recreateDeps) WithWaitVolumeAvailable(w mintaws.WaitVolumeAvailableAPI) *recreateDeps {
	d.waitVolumeAvailable = w
	return d
}

// newRecreateCommand creates the production recreate command.
func newRecreateCommand() *cobra.Command {
	return newRecreateCommandWithDeps(nil)
}

// newRecreateCommandWithDeps creates the recreate command with explicit
// dependencies for testing. When deps is nil, the command wires real AWS clients.
func newRecreateCommandWithDeps(deps *recreateDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "recreate",
		Short: "Destroy and re-provision the VM with the same configuration",
		Long: "Destroy the current VM and create a fresh one with the same instance type, " +
			"storage, and project configuration. Active sessions are detected and the " +
			"operation is blocked unless --force is used.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps != nil {
				return runRecreate(cmd, deps)
			}
			clients := awsClientsFromContext(cmd.Context())
			if clients == nil {
				return fmt.Errorf("AWS clients not configured")
			}
			cliCtx := cli.FromCommand(cmd)
			verbose := cliCtx != nil && cliCtx.Verbose
			sp := progress.NewCommandSpinner(cmd.OutOrStdout(), false)
			var pollerWriter io.Writer
			if verbose {
				pollerWriter = &spinnerWriter{sp: sp}
			} else {
				pollerWriter = sp.Writer
			}
			poller := provision.NewBootstrapPoller(
				clients.ec2Client, // DescribeInstancesAPI
				clients.ec2Client, // StopInstancesAPI
				clients.ec2Client, // TerminateInstancesAPI
				clients.ec2Client, // CreateTagsAPI
				pollerWriter,
				cmd.InOrStdin(),
			)
			configDir := config.DefaultConfigDir()
			hostKeyStore := sshconfig.NewHostKeyStore(configDir)
			// Read user-bootstrap.sh from the config directory if it exists.
			var userBootstrapScript []byte
			userBootstrapPath := filepath.Join(configDir, "user-bootstrap.sh")
			if data, err := os.ReadFile(userBootstrapPath); err == nil {
				userBootstrapScript = data
			}
			return runRecreate(cmd, &recreateDeps{
				describe:             clients.ec2Client,
				sendKey:              clients.icClient,
				remoteRun:            defaultRemoteRunner,
				owner:                clients.owner,
				ownerARN:             clients.ownerARN,
				stop:                 clients.ec2Client,
				terminate:            clients.ec2Client,
				detachVolume:         clients.ec2Client,
				waitVolumeAvailable:  ec2.NewVolumeAvailableWaiter(clients.ec2Client),
				describeVolumes:      clients.ec2Client,
				run:                  clients.ec2Client,
				attachVolume:         clients.ec2Client,
				createTags:           clients.ec2Client,
				deleteTags:           clients.ec2Client,
				describeSubnets:      clients.ec2Client,
				describeSGs:          clients.ec2Client,
				describeImages:       clients.ec2Client,
				waitRunning:          ec2.NewInstanceRunningWaiter(clients.ec2Client),
				describeFS:           clients.efsClient,
				describeAddrs:        clients.ec2Client,
				associateAddr:        clients.ec2Client,
				disassociateAddr:     clients.ec2Client,
				bootstrapScript:      GetBootstrapScript(),
				bootstrapURL:         bootstrap.ScriptURL(version),
				userBootstrapScript:  userBootstrapScript,
				verifyBootstrap:      bootstrap.Verify,
				mintConfig:           clients.mintConfig,
				removeHostKey:        hostKeyStore.RemoveKey,
				pollBootstrap:        poller.Poll,
			})
		},
	}

	cmd.Flags().Bool("force", false, "Bypass active session guard")

	return cmd
}

// runRecreate executes the recreate command logic: discover VM, check for
// active sessions, confirm with user, then signal readiness for the lifecycle
// sequence (implemented in a separate unit).
func runRecreate(cmd *cobra.Command, deps *recreateDeps) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	cliCtx := cli.FromCommand(cmd)
	vmName := "default"
	yes := false
	if cliCtx != nil {
		vmName = cliCtx.VM
		yes = cliCtx.Yes
	}

	force, _ := cmd.Flags().GetBool("force")
	w := cmd.OutOrStdout()
	sp := progress.NewCommandSpinner(w, false)

	// Discover VM.
	sp.Start(fmt.Sprintf("Discovering VM %q for owner %q...", vmName, deps.owner))

	found, err := vm.FindVM(ctx, deps.describe, deps.owner, vmName)
	if err != nil {
		sp.Fail(err.Error())
		return fmt.Errorf("discovering VM: %w", err)
	}
	if found == nil {
		return fmt.Errorf("no VM %q found — run mint up first to create one", vmName)
	}

	// Verify VM is running (session detection requires SSH access).
	state := ec2types.InstanceStateName(found.State)
	if state != ec2types.InstanceStateNameRunning {
		return fmt.Errorf("VM %q is %s — must be running to recreate (need SSH access for session detection)", vmName, found.State)
	}

	// Active session detection: check for tmux clients and SSH/mosh sessions.
	sp.Update(fmt.Sprintf("Checking for active sessions on VM %q...", vmName))

	activeSessions, err := detectActiveSessions(ctx, deps, found)
	if err != nil {
		// Non-fatal: if we can't detect sessions, warn but continue with
		// confirmation. This avoids blocking recreate when SSH is flaky.
		sp.Update(fmt.Sprintf("Warning: could not detect active sessions: %v", err))
	}

	if activeSessions != "" && !force {
		return fmt.Errorf("active sessions detected on VM %q:\n\n%s\n\nUse --force to proceed anyway", vmName, activeSessions)
	}

	if activeSessions != "" && force {
		fmt.Fprintf(w, "Warning: proceeding despite active sessions on VM %q:\n%s\n\n", vmName, activeSessions)
	}

	// Show what will happen.
	fmt.Fprintf(w, "This will destroy and re-provision VM %q (%s).\n", vmName, found.ID)
	fmt.Fprintf(w, "  - Instance %s will be terminated\n", found.ID)
	fmt.Fprintf(w, "  - A new VM will be provisioned with the same configuration\n")
	fmt.Fprintf(w, "  - Project EBS volumes will be preserved if possible\n")

	// Confirmation: require user to type VM name unless --yes is set.
	if !yes {
		fmt.Fprintf(w, "\nType the VM name %q to confirm: ", vmName)
		scanner := bufio.NewScanner(cmd.InOrStdin())
		if scanner.Scan() {
			input := strings.TrimSpace(scanner.Text())
			if input != vmName {
				return fmt.Errorf("confirmation %q does not match VM name %q — recreate aborted", input, vmName)
			}
		} else {
			return fmt.Errorf("no confirmation input received — recreate aborted")
		}
	}

	// Guards passed — execute the 9-step recreate lifecycle.
	return executeRecreateLifecycle(ctx, deps, found, vmName, sp, w)
}

// executeRecreateLifecycle runs the 9-step recreate sequence:
//  1. Query project EBS volume
//  2. Tag project EBS with pending-attach
//  3. Stop instance
//  4. Detach project EBS
//  5. Terminate instance
//  6. Launch new instance in same AZ
//  7. Attach project EBS + remove pending-attach tag
//  8. Reassociate Elastic IP
//  9. Poll for bootstrap complete
func executeRecreateLifecycle(
	ctx context.Context,
	deps *recreateDeps,
	found *vm.VM,
	vmName string,
	sp *progress.Spinner,
	w io.Writer,
) error {
	volumeID, volumeAZ, err := stepQueryProjectVolume(ctx, deps, vmName, sp, w)
	if err != nil {
		return fmt.Errorf("querying project volume: %w", err)
	}

	if err := stepTagPendingAttach(ctx, deps, volumeID, sp); err != nil {
		return fmt.Errorf("tagging project volume with pending-attach: %w", err)
	}

	if err := stepStopInstance(ctx, deps, found.ID, sp); err != nil {
		return fmt.Errorf("stopping instance %s: %w", found.ID, err)
	}

	if err := stepDetachVolume(ctx, deps, volumeID, found.ID, sp); err != nil {
		return fmt.Errorf("detaching project volume %s: %w", volumeID, err)
	}

	if err := stepTerminateInstance(ctx, deps, found.ID, sp); err != nil {
		return fmt.Errorf("terminating instance %s: %w", found.ID, err)
	}

	newInstanceID, err := stepLaunchInstance(ctx, deps, found, vmName, volumeAZ, sp)
	if err != nil {
		return fmt.Errorf("launching new instance: %w", err)
	}

	if deps.waitRunning != nil {
		sp.Update(fmt.Sprintf("  Waiting for instance %s to be running...", newInstanceID))
		if err := deps.waitRunning.Wait(ctx, &ec2.DescribeInstancesInput{
			InstanceIds: []string{newInstanceID},
		}, 5*time.Minute); err != nil {
			return fmt.Errorf("waiting for instance %s to be running: %w", newInstanceID, err)
		}
	}

	if deps.waitVolumeAvailable != nil {
		sp.Update(fmt.Sprintf("  Waiting for volume %s to become available...", volumeID))
		if err := deps.waitVolumeAvailable.Wait(ctx, &ec2.DescribeVolumesInput{
			VolumeIds: []string{volumeID},
		}, 5*time.Minute); err != nil {
			return fmt.Errorf("waiting for volume to become available: %w", err)
		}
	}

	if err := stepAttachVolume(ctx, deps, volumeID, newInstanceID, sp, w); err != nil {
		return fmt.Errorf("attaching project volume %s to %s: %w", volumeID, newInstanceID, err)
	}

	if err := stepReassociateEIP(ctx, deps, vmName, newInstanceID, sp, w); err != nil {
		return fmt.Errorf("reassociating Elastic IP: %w", err)
	}

	if err := stepBootstrapPoll(ctx, deps, vmName, newInstanceID, sp); err != nil {
		return fmt.Errorf("bootstrap polling: %w", err)
	}

	// Clear cached TOFU host key so the next connection triggers fresh
	// key recording instead of a scary change-detection warning (ADR-0019).
	if deps.removeHostKey != nil {
		if keyErr := deps.removeHostKey(vmName); keyErr != nil {
			return fmt.Errorf("clearing cached host key for %s: %w", vmName, keyErr)
		}
	}

	// Print the final success message to the command output unconditionally.
	// sp.Stop clears the spinner line in interactive mode before we print.
	sp.Stop("")
	fmt.Fprintf(w, "Recreate complete. New instance: %s\n", newInstanceID)
	if deps.pollBootstrap != nil {
		fmt.Fprintln(w, "\nBootstrap complete. VM is ready.")
	}
	return nil
}

// stepQueryProjectVolume discovers the project EBS volume for the VM (Step 1/9).
func stepQueryProjectVolume(
	ctx context.Context,
	deps *recreateDeps,
	vmName string,
	sp *progress.Spinner,
	w io.Writer,
) (volumeID, volumeAZ string, err error) {
	sp.Update("Step 1/9: Querying project EBS volume...")

	volumeID, volumeAZ, err = findProjectVolume(ctx, deps, vmName)
	if err != nil {
		return "", "", err
	}

	sp.Update(fmt.Sprintf("  Found project volume %s in %s", volumeID, volumeAZ))

	return volumeID, volumeAZ, nil
}

// stepTagPendingAttach tags the project volume with pending-attach as a safety
// net for crash recovery (Step 2/9).
func stepTagPendingAttach(
	ctx context.Context,
	deps *recreateDeps,
	volumeID string,
	sp *progress.Spinner,
) error {
	sp.Update("Step 2/9: Tagging project volume with pending-attach...")

	_, err := deps.createTags.CreateTags(ctx, &ec2.CreateTagsInput{
		Resources: []string{volumeID},
		Tags: []ec2types.Tag{
			{Key: aws.String(tags.TagPendingAttach), Value: aws.String("true")},
		},
	})
	return err
}

// stepStopInstance stops the EC2 instance (Step 3/9).
func stepStopInstance(
	ctx context.Context,
	deps *recreateDeps,
	instanceID string,
	sp *progress.Spinner,
) error {
	sp.Update(fmt.Sprintf("Step 3/9: Stopping instance %s...", instanceID))

	_, err := deps.stop.StopInstances(ctx, &ec2.StopInstancesInput{
		InstanceIds: []string{instanceID},
	})
	return err
}

// stepDetachVolume detaches the project EBS volume from the instance (Step 4/9).
func stepDetachVolume(
	ctx context.Context,
	deps *recreateDeps,
	volumeID, instanceID string,
	sp *progress.Spinner,
) error {
	sp.Update(fmt.Sprintf("Step 4/9: Detaching project volume %s...", volumeID))

	_, err := deps.detachVolume.DetachVolume(ctx, &ec2.DetachVolumeInput{
		VolumeId:   aws.String(volumeID),
		InstanceId: aws.String(instanceID),
		Force:      aws.Bool(true),
	})
	return err
}

// stepTerminateInstance terminates the EC2 instance (Step 5/9).
func stepTerminateInstance(
	ctx context.Context,
	deps *recreateDeps,
	instanceID string,
	sp *progress.Spinner,
) error {
	sp.Update(fmt.Sprintf("Step 5/9: Terminating instance %s...", instanceID))

	_, err := deps.terminate.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{instanceID},
	})
	return err
}

// stepLaunchInstance launches a new EC2 instance in the same AZ as the project
// volume (Step 6/9).
func stepLaunchInstance(
	ctx context.Context,
	deps *recreateDeps,
	original *vm.VM,
	vmName, volumeAZ string,
	sp *progress.Spinner,
) (string, error) {
	sp.Update(fmt.Sprintf("Step 6/9: Launching new instance in %s...", volumeAZ))

	newInstanceID, err := launchRecreateInstance(ctx, deps, original, vmName, volumeAZ)
	if err != nil {
		return "", err
	}

	sp.Update(fmt.Sprintf("  Launched new instance %s", newInstanceID))

	return newInstanceID, nil
}

// stepAttachVolume attaches the project EBS volume to the new instance and
// removes the pending-attach safety tag (Step 7/9).
func stepAttachVolume(
	ctx context.Context,
	deps *recreateDeps,
	volumeID, newInstanceID string,
	sp *progress.Spinner,
	w io.Writer,
) error {
	sp.Update(fmt.Sprintf("Step 7/9: Attaching project volume %s to %s...", volumeID, newInstanceID))

	_, err := deps.attachVolume.AttachVolume(ctx, &ec2.AttachVolumeInput{
		VolumeId:   aws.String(volumeID),
		InstanceId: aws.String(newInstanceID),
		Device:     aws.String("/dev/xvdf"),
	})
	if err != nil {
		return err
	}

	// Remove the pending-attach tag via DeleteTags (fully removes the key).
	if deps.deleteTags != nil {
		_, delErr := deps.deleteTags.DeleteTags(ctx, &ec2.DeleteTagsInput{
			Resources: []string{volumeID},
			Tags: []ec2types.Tag{
				{Key: aws.String(tags.TagPendingAttach)},
			},
		})
		if delErr != nil {
			// Non-fatal: the volume is attached, but the tag cleanup failed.
			// Log the warning but don't fail the recreate.
			fmt.Fprintf(w, "Warning: could not remove pending-attach tag from %s: %v\n", volumeID, delErr)
		}
	}

	return nil
}

// stepReassociateEIP reassociates the Elastic IP with the new instance (Step 8/9).
func stepReassociateEIP(
	ctx context.Context,
	deps *recreateDeps,
	vmName, newInstanceID string,
	sp *progress.Spinner,
	w io.Writer,
) error {
	sp.Update("Step 8/9: Reassociating Elastic IP...")

	return reassociateElasticIP(ctx, deps, vmName, newInstanceID, sp, w)
}

// stepBootstrapPoll waits for the bootstrap process to complete on the new
// instance (Step 9/9).
func stepBootstrapPoll(
	ctx context.Context,
	deps *recreateDeps,
	vmName, newInstanceID string,
	sp *progress.Spinner,
) error {
	sp.Update("Step 9/9: Waiting for bootstrap to complete...")

	if deps.pollBootstrap != nil {
		return deps.pollBootstrap(ctx, deps.owner, vmName, newInstanceID)
	}

	return nil
}

// findProjectVolume discovers the project EBS volume for the given owner and VM.
func findProjectVolume(ctx context.Context, deps *recreateDeps, vmName string) (volumeID, az string, err error) {
	filters := append(
		tags.FilterByOwnerAndVM(deps.owner, vmName),
		ec2types.Filter{
			Name:   aws.String("tag:" + tags.TagComponent),
			Values: []string{tags.ComponentProjectVolume},
		},
	)

	out, err := deps.describeVolumes.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{
		Filters: filters,
	})
	if err != nil {
		return "", "", fmt.Errorf("describe volumes: %w", err)
	}

	if len(out.Volumes) == 0 {
		return "", "", fmt.Errorf("no project volume found for owner %q, vm %q", deps.owner, vmName)
	}

	vol := out.Volumes[0]
	return aws.ToString(vol.VolumeId), aws.ToString(vol.AvailabilityZone), nil
}

// reassociateElasticIP discovers the existing EIP by tags and associates it
// with the new instance. If no EIP is found, it logs a warning but does not
// fail (the VM still has an auto-assigned public IP). If association fails,
// it returns an error.
//
// If the EIP has a stale AssociationId from the terminated instance's ENI,
// DisassociateAddress is called explicitly before AssociateAddress. This
// avoids the InvalidNetworkInterfaceID.NotFound race condition that occurs
// when AWS has not yet auto-cleaned the ENI reference (which can take >60s).
func reassociateElasticIP(
	ctx context.Context,
	deps *recreateDeps,
	vmName, newInstanceID string,
	sp *progress.Spinner,
	w io.Writer,
) error {
	if deps.describeAddrs == nil {
		sp.Update("  Warning: no Elastic IP client configured, skipping EIP reassociation")
		return nil
	}

	filters := append(
		tags.FilterByOwnerAndVM(deps.owner, vmName),
		ec2types.Filter{
			Name:   aws.String("tag:" + tags.TagComponent),
			Values: []string{tags.ComponentElasticIP},
		},
	)

	out, err := deps.describeAddrs.DescribeAddresses(ctx, &ec2.DescribeAddressesInput{
		Filters: filters,
	})
	if err != nil {
		return fmt.Errorf("discovering Elastic IP: %w", err)
	}

	if len(out.Addresses) == 0 {
		sp.Update(fmt.Sprintf("  Warning: no Elastic IP found for VM %q — using auto-assigned public IP", vmName))
		return nil
	}

	addr := out.Addresses[0]
	allocID := aws.ToString(addr.AllocationId)

	sp.Update(fmt.Sprintf("  Found Elastic IP %s (%s), reassociating with %s",
		aws.ToString(addr.PublicIp), allocID, newInstanceID))

	if deps.associateAddr == nil {
		return fmt.Errorf("no AssociateAddress client configured")
	}

	// If the EIP still carries a stale AssociationId from the terminated
	// instance's ENI, explicitly disassociate it first. AWS's auto-cleanup
	// can take longer than 60s, causing AssociateAddress to fail with
	// InvalidNetworkInterfaceID.NotFound.
	if aws.ToString(addr.AssociationId) != "" {
		sp.Update(fmt.Sprintf("  Disassociating stale EIP association %s...", aws.ToString(addr.AssociationId)))
		if deps.disassociateAddr == nil {
			return fmt.Errorf("no DisassociateAddress client configured")
		}
		_, disassocErr := deps.disassociateAddr.DisassociateAddress(ctx, &ec2.DisassociateAddressInput{
			AssociationId: addr.AssociationId,
		})
		if disassocErr != nil {
			return fmt.Errorf("disassociating EIP: %w", disassocErr)
		}
	}

	_, err = deps.associateAddr.AssociateAddress(ctx, &ec2.AssociateAddressInput{
		AllocationId: aws.String(allocID),
		InstanceId:   aws.String(newInstanceID),
	})
	if err != nil {
		return fmt.Errorf("associating EIP %s with instance %s: %w", allocID, newInstanceID, err)
	}

	sp.Update("  Elastic IP reassociated successfully")

	return nil
}

// launchRecreateInstance launches a new EC2 instance in the specified AZ,
// reusing the same configuration as the original instance.
func launchRecreateInstance(
	ctx context.Context,
	deps *recreateDeps,
	original *vm.VM,
	vmName, targetAZ string,
) (string, error) {
	// Resolve AMI.
	resolveAMI := deps.resolveAMI
	if resolveAMI == nil {
		resolveAMI = mintaws.ResolveAMI
	}
	amiID, err := resolveAMI(ctx, deps.describeImages)
	if err != nil {
		return "", fmt.Errorf("resolving AMI: %w", err)
	}

	// Find user's security group.
	userSGID, err := findRecreateSG(ctx, deps, deps.owner, tags.ComponentSecurityGroup)
	if err != nil {
		return "", fmt.Errorf("finding user security group: %w", err)
	}

	// Find admin EFS security group.
	adminSGID, err := findRecreateAdminSG(ctx, deps)
	if err != nil {
		return "", fmt.Errorf("finding admin security group: %w", err)
	}

	// Find a subnet in the target AZ.
	subnetID, err := findSubnetInAZ(ctx, deps, targetAZ)
	if err != nil {
		return "", fmt.Errorf("finding subnet in %s: %w", targetAZ, err)
	}

	// Prepare bootstrap script.
	bootstrapScript := deps.bootstrapScript
	if deps.verifyBootstrap != nil {
		if verifyErr := deps.verifyBootstrap(bootstrapScript); verifyErr != nil {
			return "", fmt.Errorf("bootstrap verification failed: %w", verifyErr)
		}
	}

	// Determine instance type and volume config from original or config.
	instanceType := ec2types.InstanceType(original.InstanceType)
	idleTimeout := 60
	volumeSize := int32(50)

	if deps.mintConfig != nil {
		if deps.mintConfig.InstanceType != "" {
			instanceType = ec2types.InstanceType(deps.mintConfig.InstanceType)
		}
		if deps.mintConfig.IdleTimeoutMinutes > 0 {
			idleTimeout = deps.mintConfig.IdleTimeoutMinutes
		}
		if deps.mintConfig.VolumeSizeGB > 0 {
			volumeSize = int32(deps.mintConfig.VolumeSizeGB)
		}
	}

	// Discover admin EFS filesystem.
	efsID := ""
	if deps.describeFS != nil {
		var efsErr error
		efsID, efsErr = discoverEFS(ctx, deps.describeFS)
		if efsErr != nil {
			return "", fmt.Errorf("discovering EFS: %w", efsErr)
		}
	}

	// Encode the user bootstrap script if present.
	userBootstrapB64 := ""
	if len(deps.userBootstrapScript) > 0 {
		userBootstrapB64 = base64.StdEncoding.EncodeToString(deps.userBootstrapScript)
	}

	// Render the bootstrap stub with runtime values.
	stub, renderErr := bootstrap.RenderStub(
		bootstrap.ScriptSHA256,
		deps.bootstrapURL,
		efsID,
		"/dev/xvdf",
		vmName,
		strconv.Itoa(idleTimeout),
		userBootstrapB64,
	)
	if renderErr != nil {
		return "", fmt.Errorf("rendering bootstrap stub: %w", renderErr)
	}

	const maxUserDataBytes = 16384
	if len(stub) > maxUserDataBytes {
		return "", fmt.Errorf("user-bootstrap.sh too large: rendered user-data is %d bytes, max is %d (%d bytes over limit)",
			len(stub), maxUserDataBytes, len(stub)-maxUserDataBytes)
	}

	userData := base64.StdEncoding.EncodeToString(stub)

	// Build instance tags.
	instanceTags := tags.NewTagBuilder(deps.owner, deps.ownerARN, vmName).
		WithComponent(tags.ComponentInstance).
		WithBootstrap(tags.BootstrapPending).
		Build()

	instanceTags = append(instanceTags,
		ec2types.Tag{Key: aws.String(tags.TagRootVolumeGB), Value: aws.String("200")},
		ec2types.Tag{Key: aws.String(tags.TagProjectVolumeGB), Value: aws.String(strconv.Itoa(int(volumeSize)))},
	)

	input := &ec2.RunInstancesInput{
		ImageId:      aws.String(amiID),
		InstanceType: instanceType,
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
		SubnetId:     aws.String(subnetID),
		SecurityGroupIds: []string{
			userSGID,
			adminSGID,
		},
		UserData: aws.String(userData),
		IamInstanceProfile: &ec2types.IamInstanceProfileSpecification{
			Name: aws.String("mint-instance-profile"),
		},
		TagSpecifications: []ec2types.TagSpecification{
			{
				ResourceType: ec2types.ResourceTypeInstance,
				Tags:         instanceTags,
			},
		},
	}

	out, err := deps.run.RunInstances(ctx, input)
	if err != nil {
		return "", fmt.Errorf("run instances: %w", err)
	}

	if len(out.Instances) == 0 {
		return "", fmt.Errorf("run instances returned no instances")
	}

	return aws.ToString(out.Instances[0].InstanceId), nil
}

// findRecreateSG discovers a security group by owner and component tags.
func findRecreateSG(ctx context.Context, deps *recreateDeps, owner, component string) (string, error) {
	out, err := deps.describeSGs.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("tag:" + tags.TagMint), Values: []string{"true"}},
			{Name: aws.String("tag:" + tags.TagOwner), Values: []string{owner}},
			{Name: aws.String("tag:" + tags.TagComponent), Values: []string{component}},
		},
	})
	if err != nil {
		return "", fmt.Errorf("describe security groups: %w", err)
	}
	if len(out.SecurityGroups) == 0 {
		return "", fmt.Errorf("no security group found with tags mint:owner=%s, mint:component=%s", owner, component)
	}
	return aws.ToString(out.SecurityGroups[0].GroupId), nil
}

// findRecreateAdminSG discovers the admin EFS security group by tags.
func findRecreateAdminSG(ctx context.Context, deps *recreateDeps) (string, error) {
	out, err := deps.describeSGs.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("tag:" + tags.TagMint), Values: []string{"true"}},
			{Name: aws.String("tag:" + tags.TagComponent), Values: []string{"admin"}},
		},
	})
	if err != nil {
		return "", fmt.Errorf("describe admin security groups: %w", err)
	}
	if len(out.SecurityGroups) == 0 {
		return "", fmt.Errorf("no admin security group found")
	}
	return aws.ToString(out.SecurityGroups[0].GroupId), nil
}

// findSubnetInAZ finds a default subnet in the specified AZ.
func findSubnetInAZ(ctx context.Context, deps *recreateDeps, az string) (string, error) {
	out, err := deps.describeSubnets.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("default-for-az"), Values: []string{"true"}},
			{Name: aws.String("availability-zone"), Values: []string{az}},
		},
	})
	if err != nil {
		return "", fmt.Errorf("describe subnets: %w", err)
	}
	if len(out.Subnets) == 0 {
		return "", fmt.Errorf("no default subnet found in %s", az)
	}
	return aws.ToString(out.Subnets[0].SubnetId), nil
}

// detectActiveSessions SSHs into the VM and checks all four ADR-0018 idle
// detection criteria: tmux clients, SSH/mosh connections, claude processes
// in containers, and manual extend timestamps. Returns a human-readable
// summary of active sessions, or empty string if no active sessions found.
func detectActiveSessions(ctx context.Context, deps *recreateDeps, found *vm.VM) (string, error) {
	// Create a RemoteExecutor closure that adapts the recreateDeps' remoteRun
	// to the simpler session.RemoteExecutor interface.
	executor := func(ctx context.Context, command []string) ([]byte, error) {
		return deps.remoteRun(
			ctx,
			deps.sendKey,
			found.ID,
			found.AvailabilityZone,
			found.PublicIP,
			defaultSSHPort,
			defaultSSHUser,
			command,
		)
	}

	result, err := session.DetectActiveSessions(ctx, executor)
	if err != nil {
		return "", err
	}

	return result.Summary(), nil
}

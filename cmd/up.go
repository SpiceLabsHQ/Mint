package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/efs"
	efstypes "github.com/aws/aws-sdk-go-v2/service/efs/types"

	mintaws "github.com/SpiceLabsHQ/Mint/internal/aws"
	"github.com/SpiceLabsHQ/Mint/internal/bootstrap"
	"github.com/SpiceLabsHQ/Mint/internal/cli"
	"github.com/SpiceLabsHQ/Mint/internal/config"
	"github.com/SpiceLabsHQ/Mint/internal/progress"
	"github.com/SpiceLabsHQ/Mint/internal/provision"
	"github.com/SpiceLabsHQ/Mint/internal/sshconfig"
	"github.com/SpiceLabsHQ/Mint/internal/tags"
	"github.com/SpiceLabsHQ/Mint/internal/vm"
	"github.com/spf13/cobra"
)

// spinnerWriter is an io.Writer that routes writes through the spinner's
// Update method. This prevents garbled output when --verbose is active:
// the spinner goroutine and the bootstrap poller would otherwise both write
// to the same fd concurrently.
type spinnerWriter struct{ sp *progress.Spinner }

func (sw *spinnerWriter) Write(p []byte) (int, error) {
	msg := strings.TrimRight(string(p), "\n")
	if msg != "" {
		sw.sp.Update(msg)
	}
	return len(p), nil
}

// upDeps holds the injectable dependencies for the up command.
type upDeps struct {
	provisioner          *provision.Provisioner
	owner                string
	ownerARN             string
	bootstrapScript      []byte
	bootstrapURL         string // GitHub raw URL for bootstrap.sh delivery
	userBootstrapScript  []byte // Optional user-bootstrap.sh content read from config dir
	instanceType         string
	volumeSize           int32
	volumeIOPS           int32
	sshConfigApproved    bool
	sshConfigPath        string
	describe             mintaws.DescribeInstancesAPI
	describeFileSystems  mintaws.DescribeFileSystemsAPI
}

// newUpCommand creates the production up command.
func newUpCommand() *cobra.Command {
	return newUpCommandWithDeps(nil)
}

// newUpCommandWithDeps creates the up command with explicit dependencies for testing.
func newUpCommandWithDeps(deps *upDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Provision or start the VM",
		Long: "Provision a new VM or start a stopped one. Creates EC2 instance, " +
			"project EBS volume, and Elastic IP. If a VM already exists and is " +
			"stopped, it will be started.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps != nil {
				return runUp(cmd, deps)
			}
			clients := awsClientsFromContext(cmd.Context())
			if clients == nil {
				return fmt.Errorf("AWS clients not configured")
			}
			cliCtx := cli.FromCommand(cmd)
			verbose := cliCtx != nil && cliCtx.Verbose
			sp := progress.NewCommandSpinner(cmd.OutOrStdout(), verbose)
			// When --verbose is active, route poller output through the spinner's
			// mutex-protected Update method to prevent concurrent writes to the
			// same fd from the spinner goroutine and the poller.
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
			sshApproved := false
			volumeIOPS := int32(0)
			if clients.mintConfig != nil {
				sshApproved = clients.mintConfig.SSHConfigApproved
				volumeIOPS = int32(clients.mintConfig.VolumeIOPS)
			}
			// --volume-iops flag overrides config value when provided (> 0).
			if flagIOPS, _ := cmd.Flags().GetInt32("volume-iops"); flagIOPS > 0 {
				volumeIOPS = flagIOPS
			}
			// Read user-bootstrap.sh from the config directory if it exists.
			configDir := config.DefaultConfigDir()
			var userBootstrapScript []byte
			userBootstrapPath := filepath.Join(configDir, "user-bootstrap.sh")
			if data, err := os.ReadFile(userBootstrapPath); err == nil {
				userBootstrapScript = data
			}
			return runUp(cmd, &upDeps{
				provisioner: provision.NewProvisioner(
					clients.ec2Client, // DescribeInstancesAPI
					clients.ec2Client, // StartInstancesAPI
					clients.ec2Client, // RunInstancesAPI
					clients.ec2Client, // DescribeSecurityGroupsAPI
					clients.ec2Client, // DescribeSubnetsAPI
					clients.ec2Client, // CreateVolumeAPI
					clients.ec2Client, // AttachVolumeAPI
					clients.ec2Client, // AllocateAddressAPI
					clients.ec2Client, // AssociateAddressAPI
					clients.ec2Client, // DescribeAddressesAPI
					clients.ec2Client, // CreateTagsAPI
					clients.ec2Client, // DescribeImagesAPI
				).WithWaitRunning(awsec2.NewInstanceRunningWaiter(clients.ec2Client)).
				WithWaitVolumeAvailable(awsec2.NewVolumeAvailableWaiter(clients.ec2Client)).
				WithBootstrapPoller(poller),
				owner:                clients.owner,
				ownerARN:             clients.ownerARN,
				bootstrapScript:      GetBootstrapScript(),
				bootstrapURL:         bootstrap.ScriptURL(version),
				userBootstrapScript:  userBootstrapScript,
				instanceType:         clients.mintConfig.InstanceType,
				volumeSize:           int32(clients.mintConfig.VolumeSizeGB),
				volumeIOPS:           volumeIOPS,
				sshConfigApproved:    sshApproved,
				describe:             clients.ec2Client,
				describeFileSystems:  clients.efsClient,
			})
		},
	}

	// --volume-iops overrides the config value. 0 means "use config value".
	cmd.Flags().Int32("volume-iops", 0, "IOPS for the project EBS volume (gp3, range 3000-16000; 0 uses config value)")

	return cmd
}

// runUp executes the up command logic.
func runUp(cmd *cobra.Command, deps *upDeps) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	cliCtx := cli.FromCommand(cmd)
	vmName := "default"
	verbose := false
	jsonOutput := false
	if cliCtx != nil {
		vmName = cliCtx.VM
		verbose = cliCtx.Verbose
		jsonOutput = cliCtx.JSON
	}

	// Pre-flight: warn when provisioning would result in 3+ running VMs (SPEC).
	// Warning is informational only — never blocks the operation.
	// Skip in JSON mode to avoid corrupting machine-readable output.
	if deps.describe != nil && !jsonOutput {
		existingVMs, err := vm.ListVMs(ctx, deps.describe, deps.owner)
		if err == nil {
			if runningCount := countRunningVMs(existingVMs); runningCount >= 2 {
				fmt.Fprintf(cmd.OutOrStdout(),
					"⚠  You have %d running VMs. Consider stopping unused VMs to avoid unnecessary costs.\n",
					runningCount)
			}
		}
	}

	sp := progress.NewCommandSpinner(cmd.OutOrStdout(), verbose)
	sp.Start(fmt.Sprintf("Provisioning VM %q for owner %q...", vmName, deps.owner))

	// Discover admin EFS filesystem (same pattern as mint init).
	efsID, err := discoverEFS(ctx, deps.describeFileSystems)
	if err != nil {
		sp.Fail(err.Error())
		return fmt.Errorf("discovering EFS: %w", err)
	}

	cfg := provision.ProvisionConfig{
		InstanceType:        deps.instanceType,
		VolumeSize:          deps.volumeSize,
		VolumeIOPS:          deps.volumeIOPS,
		BootstrapScript:     deps.bootstrapScript,
		BootstrapURL:        deps.bootstrapURL,
		EFSID:               efsID,
		UserBootstrapScript: deps.userBootstrapScript,
	}

	sp.Update(fmt.Sprintf("Provisioning VM %q...", vmName))

	result, err := deps.provisioner.Run(ctx, deps.owner, deps.ownerARN, vmName, cfg)
	if err != nil {
		sp.Fail(err.Error())
		return err
	}

	// Stop the spinner (clears line in interactive mode) before printing results.
	sp.Stop("")

	// Auto-generate SSH config entry if approved (ADR-0015).
	if deps.sshConfigApproved && result.PublicIP != "" {
		writeSSHConfigAfterUp(ctx, cmd, deps, vmName, result)
	}

	return printUpResult(cmd, cliCtx, result, jsonOutput, verbose)
}

func printUpResult(cmd *cobra.Command, cliCtx *cli.CLIContext, result *provision.ProvisionResult, jsonOutput, verbose bool) error {
	if jsonOutput {
		return printUpJSON(cmd, result)
	}
	return printUpHuman(cmd, result, verbose)
}

func printUpJSON(cmd *cobra.Command, result *provision.ProvisionResult) error {
	data := map[string]any{
		"instance_id":      result.InstanceID,
		"public_ip":        result.PublicIP,
		"volume_id":        result.VolumeID,
		"allocation_id":    result.AllocationID,
		"restarted":        result.Restarted,
		"already_running":  result.AlreadyRunning,
		"bootstrap_status": result.BootstrapStatus,
	}

	if result.BootstrapError != nil {
		data["bootstrap_error"] = result.BootstrapError.Error()
	}

	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(data)
}

func printUpHuman(cmd *cobra.Command, result *provision.ProvisionResult, verbose bool) error {
	w := cmd.OutOrStdout()

	if result.Restarted {
		fmt.Fprintf(w, "VM %s restarted.\n", result.InstanceID)
		if result.PublicIP != "" {
			fmt.Fprintf(w, "IP            %s\n", result.PublicIP)
		}
		if result.BootstrapError != nil {
			fmt.Fprintf(w, "\nBootstrap error: %v\n", result.BootstrapError)
			return result.BootstrapError
		}
		return nil
	}

	if result.AlreadyRunning {
		// VM was already running when mint up was called.
		fmt.Fprintf(w, "VM %s is already running.\n", result.InstanceID)
		if result.PublicIP != "" {
			fmt.Fprintf(w, "IP            %s\n", result.PublicIP)
		}
		if result.BootstrapError != nil {
			fmt.Fprintf(w, "\nBootstrap error: %v\n", result.BootstrapError)
		} else if result.BootstrapStatus == tags.BootstrapComplete {
			fmt.Fprintln(w, "\nBootstrap complete. VM is ready.")
		} else {
			// pending, unknown, or empty — don't claim success
			fmt.Fprintln(w, "\nBootstrap in progress — run 'mint status' to check.")
		}
		return nil
	}

	// Fresh provision.
	fmt.Fprintf(w, "Instance      %s\n", result.InstanceID)
	if result.PublicIP != "" {
		fmt.Fprintf(w, "IP            %s\n", result.PublicIP)
	}
	if result.VolumeID != "" {
		fmt.Fprintf(w, "Volume        %s\n", result.VolumeID)
	}
	if result.AllocationID != "" {
		fmt.Fprintf(w, "EIP           %s\n", result.AllocationID)
	}

	if result.BootstrapError != nil {
		fmt.Fprintf(w, "\nBootstrap error: %v\n", result.BootstrapError)
		return result.BootstrapError
	}
	fmt.Fprintln(w, "\nBootstrap complete. VM is ready.")
	return nil
}

// writeSSHConfigAfterUp generates and writes the SSH config block for the VM.
// Failures are non-fatal: a warning is printed but the command still succeeds.
func writeSSHConfigAfterUp(ctx context.Context, cmd *cobra.Command, deps *upDeps, vmName string, result *provision.ProvisionResult) {
	w := cmd.OutOrStdout()

	// Look up the VM to get AvailabilityZone (not in ProvisionResult).
	az := ""
	if deps.describe != nil {
		found, err := vm.FindVM(ctx, deps.describe, deps.owner, vmName)
		if err != nil {
			fmt.Fprintf(w, "Warning: could not look up VM for ssh config: %v\n", err)
			return
		}
		if found != nil {
			az = found.AvailabilityZone
		}
	}

	configPath := deps.sshConfigPath
	if configPath == "" {
		configPath = defaultSSHConfigPath()
	}

	block := sshconfig.GenerateBlock(vmName, result.PublicIP, defaultSSHUser, defaultSSHPort, result.InstanceID, az)
	if err := sshconfig.WriteManagedBlock(configPath, vmName, block); err != nil {
		fmt.Fprintf(w, "Warning: could not update ssh config: %v\n", err)
	}
}

// discoverEFS finds the admin EFS filesystem by tags (mint=true, mint:component=admin).
// This mirrors the discovery logic in internal/provision/init.go but lives in cmd/
// because it is command-level wiring, not provisioning logic.
func discoverEFS(ctx context.Context, client mintaws.DescribeFileSystemsAPI) (string, error) {
	out, err := client.DescribeFileSystems(ctx, &efs.DescribeFileSystemsInput{})
	if err != nil {
		return "", fmt.Errorf("describe EFS: %w", err)
	}

	for _, fs := range out.FileSystems {
		tagMap := efsTagsToMap(fs.Tags)
		if tagMap[tags.TagMint] == "true" && tagMap[tags.TagComponent] == "admin" {
			return aws.ToString(fs.FileSystemId), nil
		}
	}

	return "", fmt.Errorf("no admin EFS found — run 'mint init' first")
}

// efsTagsToMap converts EFS tags to a map for convenient lookup.
func efsTagsToMap(efsTags []efstypes.Tag) map[string]string {
	m := make(map[string]string, len(efsTags))
	for _, tag := range efsTags {
		m[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}
	return m
}

// upWithProvisioner runs up with a pre-built Provisioner (for testing).
func upWithProvisioner(ctx context.Context, cmd *cobra.Command, cliCtx *cli.CLIContext, deps *upDeps, vmName string) error {
	cfg := provision.ProvisionConfig{
		InstanceType:        deps.instanceType,
		VolumeSize:          deps.volumeSize,
		VolumeIOPS:          deps.volumeIOPS,
		BootstrapScript:     deps.bootstrapScript,
		BootstrapURL:        deps.bootstrapURL,
		UserBootstrapScript: deps.userBootstrapScript,
	}

	verbose := false
	jsonOutput := false
	if cliCtx != nil {
		verbose = cliCtx.Verbose
		jsonOutput = cliCtx.JSON
	}

	result, err := deps.provisioner.Run(ctx, deps.owner, deps.ownerARN, vmName, cfg)
	if err != nil {
		return err
	}

	return printUpResult(cmd, cliCtx, result, jsonOutput, verbose)
}


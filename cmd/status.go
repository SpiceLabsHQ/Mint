package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/spf13/cobra"

	mintaws "github.com/SpiceLabsHQ/Mint/internal/aws"
	"github.com/SpiceLabsHQ/Mint/internal/cli"
	"github.com/SpiceLabsHQ/Mint/internal/progress"
	"github.com/SpiceLabsHQ/Mint/internal/tags"
	"github.com/SpiceLabsHQ/Mint/internal/vm"
)

// statusDeps holds the injectable dependencies for the status command.
type statusDeps struct {
	describe       mintaws.DescribeInstancesAPI
	sendKey        mintaws.SendSSHPublicKeyAPI
	owner          string
	remoteRun      RemoteCommandRunner
	versionChecker VersionCheckerFunc
}

// newStatusCommand creates the production status command.
func newStatusCommand() *cobra.Command {
	return newStatusCommandWithDeps(nil)
}

// newStatusCommandWithDeps creates the status command with explicit dependencies
// for testing.
func newStatusCommandWithDeps(deps *statusDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show VM details",
		Long:  "Show detailed status of a single VM including state, IP, instance type, and tags.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps != nil {
				if deps.versionChecker == nil {
					deps.versionChecker = defaultVersionChecker()
				}
				return runStatus(cmd, deps)
			}
			clients := awsClientsFromContext(cmd.Context())
			if clients == nil {
				return fmt.Errorf("AWS clients not configured")
			}
			return runStatus(cmd, &statusDeps{
				describe:       clients.ec2Client,
				sendKey:        clients.icClient,
				owner:          clients.owner,
				remoteRun:      defaultRemoteRunner,
				versionChecker: defaultVersionChecker(),
			})
		},
	}
}

// statusJSON is the JSON representation of a VM for --json output.
type statusJSON struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	State           string            `json:"state"`
	PublicIP        string            `json:"public_ip,omitempty"`
	InstanceType    string            `json:"instance_type"`
	RootVolumeGB    int               `json:"root_volume_gb,omitempty"`
	ProjectVolumeGB int               `json:"project_volume_gb,omitempty"`
	DiskUsagePct    *int              `json:"disk_usage_pct,omitempty"`
	LaunchTime      time.Time         `json:"launch_time"`
	BootstrapStatus string            `json:"bootstrap_status"`
	Tags            map[string]string `json:"tags,omitempty"`
	MintVersion     string            `json:"mint_version"`
	UpdateAvailable bool              `json:"update_available"`
	LatestVersion   *string           `json:"latest_version"`
}

// runStatus executes the status command logic.
func runStatus(cmd *cobra.Command, deps *statusDeps) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	cliCtx := cli.FromCommand(cmd)
	vmName := "default"
	jsonOutput := false
	if cliCtx != nil {
		vmName = cliCtx.VM
		jsonOutput = cliCtx.JSON
	}

	w := cmd.OutOrStdout()

	// Show a spinner during the AWS VM lookup. Suppress in JSON mode so
	// spinner lines do not corrupt machine-readable output.
	sp := progress.NewCommandSpinner(w, !jsonOutput)
	sp.Start("Checking VM status...")

	found, err := vm.FindVM(ctx, deps.describe, deps.owner, vmName)
	if err != nil {
		sp.Fail(err.Error())
		msg := fmt.Sprintf("finding VM: %v", err)
		if jsonOutput {
			fmt.Fprintf(w, "{\"error\":%q}\n", msg)
			return silentExitError{}
		}
		return fmt.Errorf("%s", msg)
	}

	if found == nil {
		sp.Fail(fmt.Sprintf("VM %q not found", vmName))
		msg := fmt.Sprintf("VM %q not found for owner %q", vmName, deps.owner)
		if jsonOutput {
			fmt.Fprintf(w, "{\"error\":%q}\n", msg)
			return silentExitError{}
		}
		return fmt.Errorf("%s", msg)
	}

	// Stop the spinner before printing any output to prevent interleaving.
	sp.Stop("")

	// Fetch disk usage when VM is running and SSH deps are available.
	var diskUsagePct *int
	if found.State == string(ec2types.InstanceStateNameRunning) && deps.remoteRun != nil && deps.sendKey != nil {
		diskUsagePct = fetchDiskUsage(ctx, deps, found)
	}

	if jsonOutput {
		return writeStatusJSON(w, found, diskUsagePct, deps.versionChecker)
	}

	writeStatusHuman(w, found, diskUsagePct)
	appendVersionNotice(w)
	return nil
}

// fetchDiskUsage retrieves the root volume disk usage percentage via SSH.
// Returns nil if the SSH command fails (graceful degradation).
func fetchDiskUsage(ctx context.Context, deps *statusDeps, v *vm.VM) *int {
	dfCmd := []string{"df", "--output=pcent", "/"}
	output, err := deps.remoteRun(
		ctx,
		deps.sendKey,
		v.ID,
		v.AvailabilityZone,
		v.PublicIP,
		defaultSSHPort,
		defaultSSHUser,
		dfCmd,
	)
	if err != nil {
		return nil
	}

	pct, err := parseDiskUsagePct(string(output))
	if err != nil {
		return nil
	}
	return &pct
}

// parseDiskUsagePct extracts the percentage value from df --output=pcent output.
// Expected format:
//
//	Use%
//	 42%
func parseDiskUsagePct(output string) (int, error) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) < 2 {
		return 0, fmt.Errorf("unexpected df output: %q", output)
	}
	// The percentage is on the last line, e.g. " 42%"
	pctStr := strings.TrimSpace(lines[len(lines)-1])
	pctStr = strings.TrimSuffix(pctStr, "%")
	pct, err := strconv.Atoi(pctStr)
	if err != nil {
		return 0, fmt.Errorf("parsing disk usage percentage: %w", err)
	}
	return pct, nil
}

// writeStatusJSON outputs a single VM as a JSON object.
func writeStatusJSON(w io.Writer, v *vm.VM, diskUsagePct *int, checker VersionCheckerFunc) error {
	updateAvailable := false
	var latestVersion *string
	if checker != nil {
		updateAvailable, latestVersion = checker()
	}

	obj := statusJSON{
		ID:              v.ID,
		Name:            v.Name,
		State:           v.State,
		PublicIP:        v.PublicIP,
		InstanceType:    v.InstanceType,
		RootVolumeGB:    v.RootVolumeGB,
		ProjectVolumeGB: v.ProjectVolumeGB,
		DiskUsagePct:    diskUsagePct,
		LaunchTime:      v.LaunchTime,
		BootstrapStatus: v.BootstrapStatus,
		Tags:            v.Tags,
		MintVersion:     version,
		UpdateAvailable: updateAvailable,
		LatestVersion:   latestVersion,
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(obj)
}

// writeStatusHuman outputs a single VM in human-readable format.
func writeStatusHuman(w io.Writer, v *vm.VM, diskUsagePct *int) {
	bootstrap := v.BootstrapStatus
	if bootstrap == tags.BootstrapFailed {
		bootstrap = "FAILED"
	}

	ip := v.PublicIP
	if ip == "" {
		ip = "-"
	}

	fmt.Fprintf(w, "VM:        %s\n", v.Name)
	fmt.Fprintf(w, "ID:        %s\n", v.ID)
	fmt.Fprintf(w, "State:     %s\n", v.State)
	fmt.Fprintf(w, "IP:        %s\n", ip)
	fmt.Fprintf(w, "Type:      %s\n", v.InstanceType)
	if v.RootVolumeGB > 0 {
		fmt.Fprintf(w, "Root Vol:  %d GB\n", v.RootVolumeGB)
	}
	if v.ProjectVolumeGB > 0 {
		fmt.Fprintf(w, "Proj Vol:  %d GB\n", v.ProjectVolumeGB)
	}
	if diskUsagePct != nil {
		if *diskUsagePct >= 80 {
			fmt.Fprintf(w, "Disk:      %d%% [WARN]\n", *diskUsagePct)
		} else {
			fmt.Fprintf(w, "Disk:      %d%%\n", *diskUsagePct)
		}
	} else if v.State == string(ec2types.InstanceStateNameRunning) {
		fmt.Fprintf(w, "Disk:      unknown\n")
	}
	fmt.Fprintf(w, "Launched:  %s\n", v.LaunchTime.Format(time.RFC3339))
	fmt.Fprintf(w, "Bootstrap: %s\n", bootstrap)

	if len(v.Tags) > 0 {
		fmt.Fprintln(w, "\nTags:")
		for k, val := range v.Tags {
			fmt.Fprintf(w, "  %s = %s\n", k, val)
		}
	}

	shortCommit := commit
	if len(shortCommit) > 7 {
		shortCommit = shortCommit[:7]
	}
	fmt.Fprintf(w, "\nmint %s (%s)\n", version, shortCommit)
}

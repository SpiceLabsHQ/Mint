package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	mintaws "github.com/nicholasgasior/mint/internal/aws"
	"github.com/nicholasgasior/mint/internal/cli"
	"github.com/nicholasgasior/mint/internal/config"
	"github.com/nicholasgasior/mint/internal/tags"
	versioncheck "github.com/nicholasgasior/mint/internal/version"
	"github.com/nicholasgasior/mint/internal/vm"
)

// VersionCheckerFunc is a function that checks for an available update.
// It returns whether an update is available and the latest version string
// (nil if the check failed or the version is unknown).
type VersionCheckerFunc func() (updateAvailable bool, latestVersion *string)

// listDeps holds the injectable dependencies for the list command.
type listDeps struct {
	describe       mintaws.DescribeInstancesAPI
	owner          string
	idleTimeout    time.Duration
	versionChecker VersionCheckerFunc
}

// newListCommand creates the production list command.
func newListCommand() *cobra.Command {
	return newListCommandWithDeps(nil)
}

// defaultVersionChecker returns a VersionCheckerFunc that calls the real
// version check with a short timeout, failing open on any error.
func defaultVersionChecker() VersionCheckerFunc {
	return func() (bool, *string) {
		cacheDir := config.DefaultConfigDir()
		info, err := versioncheck.Check(version, cacheDir)
		if err != nil || info == nil {
			return false, nil
		}
		if info.LatestVersion == "" {
			return false, nil
		}
		latest := info.LatestVersion
		return info.UpdateAvailable, &latest
	}
}

// newListCommandWithDeps creates the list command with explicit dependencies
// for testing.
func newListCommandWithDeps(deps *listDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all VMs",
		Long:  "List all VMs belonging to the current owner with status, IP, and uptime.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps != nil {
				if deps.versionChecker == nil {
					deps.versionChecker = defaultVersionChecker()
				}
				return runList(cmd, deps)
			}
			clients := awsClientsFromContext(cmd.Context())
			if clients == nil {
				return fmt.Errorf("AWS clients not configured")
			}
			return runList(cmd, &listDeps{
				describe:       clients.ec2Client,
				owner:          clients.owner,
				idleTimeout:    clients.idleTimeout(),
				versionChecker: defaultVersionChecker(),
			})
		},
	}
}

// vmJSON is the JSON representation of a VM for --json output.
type vmJSON struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	State           string            `json:"state"`
	PublicIP        string            `json:"public_ip,omitempty"`
	InstanceType    string            `json:"instance_type"`
	LaunchTime      time.Time         `json:"launch_time"`
	Uptime          string            `json:"uptime"`
	BootstrapStatus string            `json:"bootstrap_status"`
	Tags            map[string]string `json:"tags,omitempty"`
}

// listJSON is the top-level JSON envelope for the list command output.
type listJSON struct {
	VMs             []vmJSON `json:"vms"`
	RunningVMCount  int      `json:"running_vm_count"`
	UpdateAvailable bool     `json:"update_available"`
	LatestVersion   *string  `json:"latest_version"`
}

// runList executes the list command logic.
func runList(cmd *cobra.Command, deps *listDeps) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	cliCtx := cli.FromCommand(cmd)
	jsonOutput := false
	if cliCtx != nil {
		jsonOutput = cliCtx.JSON
	}

	vms, err := vm.ListVMs(ctx, deps.describe, deps.owner)
	if err != nil {
		return fmt.Errorf("listing VMs: %w", err)
	}

	w := cmd.OutOrStdout()

	if jsonOutput {
		return writeListJSON(w, vms, deps.versionChecker)
	}

	writeListTable(w, vms, deps.idleTimeout)

	// Append version check notice (human output only).
	appendVersionNotice(w)

	return nil
}

// countRunningVMs returns the number of VMs in the "running" state.
func countRunningVMs(vms []*vm.VM) int {
	count := 0
	for _, v := range vms {
		if v.State == "running" {
			count++
		}
	}
	return count
}

// writeListJSON outputs VMs as a JSON object with version check fields.
func writeListJSON(w io.Writer, vms []*vm.VM, checker VersionCheckerFunc) error {
	items := make([]vmJSON, 0, len(vms))
	for _, v := range vms {
		items = append(items, vmJSON{
			ID:              v.ID,
			Name:            v.Name,
			State:           v.State,
			PublicIP:        v.PublicIP,
			InstanceType:    v.InstanceType,
			LaunchTime:      v.LaunchTime,
			Uptime:          formatUptime(v.LaunchTime),
			BootstrapStatus: v.BootstrapStatus,
			Tags:            v.Tags,
		})
	}

	updateAvailable := false
	var latestVersion *string
	if checker != nil {
		updateAvailable, latestVersion = checker()
	}

	out := listJSON{
		VMs:             items,
		RunningVMCount:  countRunningVMs(vms),
		UpdateAvailable: updateAvailable,
		LatestVersion:   latestVersion,
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// writeListTable outputs VMs in a human-readable table.
func writeListTable(w io.Writer, vms []*vm.VM, idleTimeout time.Duration) {
	if len(vms) == 0 {
		fmt.Fprintln(w, "No VMs found.")
		return
	}

	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSTATE\tIP\tTYPE\tUPTIME\tBOOTSTRAP")

	for _, v := range vms {
		bootstrap := v.BootstrapStatus
		if bootstrap == tags.BootstrapFailed {
			bootstrap = "FAILED"
		}

		ip := v.PublicIP
		if ip == "" {
			ip = "-"
		}

		uptime := formatUptime(v.LaunchTime)

		// Idle timer warning: only for running VMs.
		warning := ""
		if v.State == "running" && idleTimeout > 0 && time.Since(v.LaunchTime) > idleTimeout {
			warning = " (idle)"
		}

		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s%s\t%s\n",
			v.Name, v.State, ip, v.InstanceType, uptime, warning, bootstrap)
	}

	tw.Flush()

	// Warn when 3+ VMs are running (SPEC: informational only, no hard limit).
	if n := countRunningVMs(vms); n >= 3 {
		fmt.Fprintf(w, "\n⚠  You have %d running VMs. Consider stopping unused VMs to avoid unnecessary costs.\n", n)
	}
}

// formatUptime returns a human-readable uptime string.
func formatUptime(launchTime time.Time) string {
	if launchTime.IsZero() {
		return "-"
	}
	d := time.Since(launchTime)
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60

	if hours > 0 {
		return fmt.Sprintf("%dh%dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

// appendVersionNotice checks for updates and prints a notice if one is available.
func appendVersionNotice(w io.Writer) {
	cacheDir := config.DefaultConfigDir()
	info, err := versioncheck.Check(version, cacheDir)
	if err != nil || info == nil {
		return
	}
	if !info.UpdateAvailable {
		return
	}

	fmt.Fprintf(w, "\n")
	line := strings.Repeat("-", 50)
	fmt.Fprintln(w, line)
	fmt.Fprintf(w, "A new version of mint is available: %s (current: %s)\n", info.LatestVersion, version)
	fmt.Fprintln(w, line)
}

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	mintaws "github.com/nicholasgasior/mint/internal/aws"
	"github.com/nicholasgasior/mint/internal/cli"
	"github.com/nicholasgasior/mint/internal/tags"
	"github.com/nicholasgasior/mint/internal/vm"
)

// statusDeps holds the injectable dependencies for the status command.
type statusDeps struct {
	describe mintaws.DescribeInstancesAPI
	owner    string
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
				return runStatus(cmd, deps)
			}
			clients := awsClientsFromContext(cmd.Context())
			if clients == nil {
				return fmt.Errorf("AWS clients not configured")
			}
			return runStatus(cmd, &statusDeps{
				describe: clients.ec2Client,
				owner:    clients.owner,
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
	LaunchTime      time.Time         `json:"launch_time"`
	BootstrapStatus string            `json:"bootstrap_status"`
	Tags            map[string]string `json:"tags,omitempty"`
	MintVersion     string            `json:"mint_version"`
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

	found, err := vm.FindVM(ctx, deps.describe, deps.owner, vmName)
	if err != nil {
		return fmt.Errorf("finding VM: %w", err)
	}

	if found == nil {
		return fmt.Errorf("VM %q not found for owner %q", vmName, deps.owner)
	}

	w := cmd.OutOrStdout()

	if jsonOutput {
		return writeStatusJSON(w, found)
	}

	writeStatusHuman(w, found)
	appendVersionNotice(w)
	return nil
}

// writeStatusJSON outputs a single VM as a JSON object.
func writeStatusJSON(w io.Writer, v *vm.VM) error {
	obj := statusJSON{
		ID:              v.ID,
		Name:            v.Name,
		State:           v.State,
		PublicIP:        v.PublicIP,
		InstanceType:    v.InstanceType,
		RootVolumeGB:    v.RootVolumeGB,
		ProjectVolumeGB: v.ProjectVolumeGB,
		LaunchTime:      v.LaunchTime,
		BootstrapStatus: v.BootstrapStatus,
		Tags:            v.Tags,
		MintVersion:     version,
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(obj)
}

// writeStatusHuman outputs a single VM in human-readable format.
func writeStatusHuman(w io.Writer, v *vm.VM) {
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

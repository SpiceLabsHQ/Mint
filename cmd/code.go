package cmd

import (
	"context"
	"fmt"
	"strings"

	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/spf13/cobra"

	mintaws "github.com/SpiceLabsHQ/Mint/internal/aws"
	"github.com/SpiceLabsHQ/Mint/internal/cli"
	"github.com/SpiceLabsHQ/Mint/internal/sshconfig"
	"github.com/SpiceLabsHQ/Mint/internal/vm"
)

// codeDeps holds the injectable dependencies for the code command.
type codeDeps struct {
	describe          mintaws.DescribeInstancesAPI
	sendKey           mintaws.SendSSHPublicKeyAPI
	runRemoteCommand  RemoteCommandRunner
	owner             string
	profile           string // AWS profile for ProxyCommand aws CLI
	region            string // AWS region for ProxyCommand aws CLI
	runner            CommandRunner
	sshConfigPath     string
	sshConfigApproved bool
}

// newCodeCommand creates the production code command.
func newCodeCommand() *cobra.Command {
	return newCodeCommandWithDeps(nil)
}

// newCodeCommandWithDeps creates the code command with explicit dependencies
// for testing.
func newCodeCommandWithDeps(deps *codeDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "code [project]",
		Short: "Open VS Code connected to the VM",
		Long: "Open VS Code with Remote-SSH connected to the VM. " +
			"Ensures the SSH config entry exists before launching.\n\n" +
			"If a project name is given, opens /mint/projects/<name> in VS Code.\n" +
			"With no arguments, discovers projects on the VM: auto-opens if exactly one exists, " +
			"or lists available projects with example commands.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps != nil {
				return runCode(cmd, args, deps)
			}
			clients := awsClientsFromContext(cmd.Context())
			if clients == nil {
				return fmt.Errorf("AWS clients not configured")
			}
			sshApproved := false
			if clients.mintConfig != nil {
				sshApproved = clients.mintConfig.SSHConfigApproved
			}
			// Determine effective profile: --profile flag > config aws_profile.
			cliCtx := cli.FromCommand(cmd)
			profile := ""
			if cliCtx != nil {
				profile = cliCtx.Profile
			}
			if profile == "" && clients.mintConfig != nil {
				profile = clients.mintConfig.AWSProfile
			}
			return runCode(cmd, args, &codeDeps{
				describe:          clients.ec2Client,
				sendKey:           clients.icClient,
				runRemoteCommand:  defaultRemoteRunner,
				owner:             clients.owner,
				profile:           profile,
				region:            clients.region,
				sshConfigApproved: sshApproved,
			})
		},
	}

	cmd.Flags().String("path", "/home/ubuntu", "Remote directory to open in VS Code")

	return cmd
}

// runCode executes the code command logic: discover VM, verify running,
// ensure SSH config, exec VS Code with --remote.
func runCode(cmd *cobra.Command, args []string, deps *codeDeps) error {
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

	// ADR-0015: Check permission before writing to ~/.ssh/config.
	if !deps.sshConfigApproved {
		return fmt.Errorf(
			"mint needs permission to update ~/.ssh/config — " +
				"run mint config set ssh_config_approved true",
		)
	}

	// When no positional arg and --path not explicitly set, discover projects
	// on the VM and provide guidance.
	pathChanged := cmd.Flags().Changed("path")
	if len(args) == 0 && !pathChanged {
		return codeDiscoverProjects(cmd, ctx, deps, vmName, found)
	}

	// Resolve remote path from positional arg or --path flag.
	remotePath, err := resolveCodePath(cmd, args)
	if err != nil {
		return err
	}

	return launchVSCode(cmd, deps, vmName, found, remotePath)
}

// codeDiscoverProjects handles the no-argument code path: SSHes to the VM,
// lists projects under /mint/projects/, and branches on the count:
//   - 0 projects: prints guidance to run mint project add
//   - 1 project: auto-opens VS Code at that project
//   - 2+ projects: lists them with mint code <name> examples
func codeDiscoverProjects(cmd *cobra.Command, ctx context.Context, deps *codeDeps, vmName string, found *vm.VM) error {
	// SSH to the VM to list projects.
	lsCmd := []string{"ls", "-1", "/mint/projects/"}
	lsOutput, err := deps.runRemoteCommand(ctx, deps.sendKey, found.ID, found.AvailabilityZone,
		found.PublicIP, defaultSSHPort, defaultSSHUser, lsCmd)
	if err != nil {
		return fmt.Errorf("listing projects: %w", err)
	}

	// Parse project names, filtering lost+found and blank lines.
	projects := parseProjectNames(string(lsOutput))

	w := cmd.OutOrStdout()

	switch len(projects) {
	case 0:
		fmt.Fprintln(w, "No projects yet — run mint project add <git-url>")
		return nil

	case 1:
		// Auto-open the sole project in VS Code.
		remotePath := fmt.Sprintf("/mint/projects/%s", projects[0])
		return launchVSCode(cmd, deps, vmName, found, remotePath)

	default:
		// List projects with example commands.
		fmt.Fprintln(w, "Multiple projects found — specify which to open:")
		fmt.Fprintln(w)
		for _, p := range projects {
			fmt.Fprintf(w, "  mint code %s\n", p)
		}
		return nil
	}
}

// parseProjectNames extracts project directory names from ls -1 output,
// filtering blank lines and the lost+found directory.
func parseProjectNames(lsOutput string) []string {
	var projects []string
	for _, line := range strings.Split(lsOutput, "\n") {
		name := strings.TrimSpace(line)
		if name != "" && name != "lost+found" {
			projects = append(projects, name)
		}
	}
	return projects
}

// launchVSCode writes the SSH config and execs VS Code with --remote.
func launchVSCode(cmd *cobra.Command, deps *codeDeps, vmName string, found *vm.VM, remotePath string) error {
	// Ensure SSH config entry exists.
	sshConfigPath := deps.sshConfigPath
	if sshConfigPath == "" {
		sshConfigPath = defaultSSHConfigPath()
	}

	block := sshconfig.GenerateBlock(vmName, found.PublicIP, defaultSSHUser, defaultSSHPort, found.ID, found.AvailabilityZone, deps.profile, deps.region)
	if err := sshconfig.WriteManagedBlock(sshConfigPath, vmName, block); err != nil {
		return fmt.Errorf("write ssh config: %w", err)
	}

	// Build VS Code command: code --remote ssh-remote+mint-<vmName> <path>
	remoteName := fmt.Sprintf("ssh-remote+mint-%s", vmName)

	runner := deps.runner
	if runner == nil {
		runner = defaultRunner
	}

	return runner("code", "--remote", remoteName, remotePath)
}

// resolveCodePath determines the remote directory to open in VS Code.
//
// Priority:
//  1. Positional arg (project name) -> /mint/projects/<name>
//  2. --path flag (explicit override)
//  3. Default: /home/ubuntu
//
// It is an error to provide both a positional arg and --path.
func resolveCodePath(cmd *cobra.Command, args []string) (string, error) {
	pathChanged := cmd.Flags().Changed("path")
	pathFlag, _ := cmd.Flags().GetString("path")

	if len(args) > 0 {
		project := args[0]

		// Reject --path when a positional project name is given.
		if pathChanged {
			return "", fmt.Errorf(
				"cannot use both project argument %q and --path flag — "+
					"use one or the other", project)
		}

		// Validate the project name to prevent shell injection.
		if err := validateProjectName(project); err != nil {
			return "", err
		}

		return fmt.Sprintf("/mint/projects/%s", project), nil
	}

	// No positional arg: use --path flag or its default.
	if pathFlag == "" {
		return "/home/ubuntu", nil
	}
	return pathFlag, nil
}

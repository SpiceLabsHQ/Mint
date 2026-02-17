package cmd

import (
	"context"
	"fmt"
	"path"
	"strings"

	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/spf13/cobra"

	mintaws "github.com/nicholasgasior/mint/internal/aws"
	"github.com/nicholasgasior/mint/internal/cli"
	"github.com/nicholasgasior/mint/internal/vm"
)

// projectAddDeps holds the injectable dependencies for the project add command.
type projectAddDeps struct {
	describe mintaws.DescribeInstancesAPI
	sendKey  mintaws.SendSSHPublicKeyAPI
	owner    string
	remote   RemoteCommandRunner
}

// newProjectCommand creates the parent "project" command with subcommands attached.
func newProjectCommand() *cobra.Command {
	return newProjectCommandWithDeps(nil)
}

// newProjectCommandWithDeps creates the project command tree with explicit
// dependencies for testing.
func newProjectCommandWithDeps(deps *projectAddDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage projects on the VM",
		Long:  "Clone repositories, build devcontainers, and manage projects on the VM.",
	}

	cmd.AddCommand(newProjectAddCommandWithDeps(deps))

	return cmd
}

// newProjectAddCommand creates the production project add subcommand.
func newProjectAddCommand() *cobra.Command {
	return newProjectAddCommandWithDeps(nil)
}

// newProjectAddCommandWithDeps creates the project add subcommand with explicit
// dependencies for testing.
func newProjectAddCommandWithDeps(deps *projectAddDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <git-url>",
		Short: "Clone a repo, build its devcontainer, and create a tmux session",
		Long: "Clone a git repository to /mint/projects/<name> on the VM, " +
			"run devcontainer up to build the development container, " +
			"and create a tmux session for the project.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps != nil {
				return runProjectAdd(cmd, deps, args[0])
			}
			clients := awsClientsFromContext(cmd.Context())
			if clients == nil {
				return fmt.Errorf("AWS clients not configured")
			}
			return runProjectAdd(cmd, &projectAddDeps{
				describe: clients.ec2Client,
				sendKey:  clients.icClient,
				owner:    clients.owner,
				remote:   defaultRemoteRunner,
			}, args[0])
		},
	}

	cmd.Flags().String("name", "", "Override the project name (default: derived from git URL)")
	cmd.Flags().String("branch", "", "Branch to clone")

	return cmd
}

// runProjectAdd executes the project add logic: discover VM, clone repo,
// build devcontainer, create tmux session.
func runProjectAdd(cmd *cobra.Command, deps *projectAddDeps, gitURL string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	cliCtx := cli.FromCommand(cmd)
	vmName := "default"
	if cliCtx != nil {
		vmName = cliCtx.VM
	}

	// Derive project name from URL or --name flag.
	projectName, err := extractProjectName(gitURL)
	if err != nil {
		return fmt.Errorf("invalid git URL %q: %w", gitURL, err)
	}

	nameOverride, _ := cmd.Flags().GetString("name")
	if nameOverride != "" {
		projectName = nameOverride
	}

	branch, _ := cmd.Flags().GetString("branch")

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

	w := cmd.OutOrStdout()
	projectPath := fmt.Sprintf("/mint/projects/%s", projectName)

	// Step 1: Clone repository.
	fmt.Fprintf(w, "Cloning %s...\n", gitURL)
	cloneCmd := buildCloneCommand(gitURL, projectPath, branch)
	_, err = deps.remote(ctx, deps.sendKey, found.ID, found.AvailabilityZone,
		found.PublicIP, defaultSSHPort, defaultSSHUser, cloneCmd)
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			return fmt.Errorf("project directory already exists at %s on the VM", projectPath)
		}
		return fmt.Errorf("cloning repository: %w", err)
	}

	// Step 2: Build devcontainer.
	fmt.Fprintf(w, "Building devcontainer...\n")
	buildCmd := []string{"devcontainer", "up", "--workspace-folder", projectPath}
	_, err = deps.remote(ctx, deps.sendKey, found.ID, found.AvailabilityZone,
		found.PublicIP, defaultSSHPort, defaultSSHUser, buildCmd)
	if err != nil {
		return fmt.Errorf("building devcontainer: %w", err)
	}

	// Step 3: Create tmux session.
	fmt.Fprintf(w, "Creating tmux session...\n")
	tmuxCmd := []string{"tmux", "new-session", "-d", "-s", projectName}
	_, err = deps.remote(ctx, deps.sendKey, found.ID, found.AvailabilityZone,
		found.PublicIP, defaultSSHPort, defaultSSHUser, tmuxCmd)
	if err != nil {
		return fmt.Errorf("creating tmux session: %w", err)
	}

	fmt.Fprintf(w, "\nProject %q ready at %s\n", projectName, projectPath)
	return nil
}

// buildCloneCommand constructs the git clone command arguments.
func buildCloneCommand(gitURL, projectPath, branch string) []string {
	cmd := []string{"git", "clone"}
	if branch != "" {
		cmd = append(cmd, "--branch", branch)
	}
	cmd = append(cmd, gitURL, projectPath)
	return cmd
}

// extractProjectName derives a project name from a git URL.
// Handles both HTTPS and SSH (git@) URL formats.
//
// Examples:
//
//	https://github.com/org/repo.git → repo
//	git@github.com:org/repo.git    → repo
func extractProjectName(gitURL string) (string, error) {
	if gitURL == "" {
		return "", fmt.Errorf("empty git URL")
	}

	// Normalize SSH URLs: git@host:path → path
	urlPath := gitURL
	if strings.Contains(gitURL, "://") {
		// HTTPS URL: extract path component.
		parts := strings.SplitN(gitURL, "://", 2)
		if len(parts) != 2 {
			return "", fmt.Errorf("malformed URL")
		}
		// Remove host portion: host/path → path
		hostAndPath := parts[1]
		slashIdx := strings.Index(hostAndPath, "/")
		if slashIdx < 0 {
			return "", fmt.Errorf("no path in URL %q", gitURL)
		}
		urlPath = hostAndPath[slashIdx+1:]
	} else if strings.Contains(gitURL, ":") {
		// SSH URL: git@host:org/repo.git
		parts := strings.SplitN(gitURL, ":", 2)
		if len(parts) != 2 || parts[1] == "" {
			return "", fmt.Errorf("malformed SSH URL %q", gitURL)
		}
		urlPath = parts[1]
	} else {
		return "", fmt.Errorf("unrecognized git URL format %q", gitURL)
	}

	// Take the last path segment and strip .git suffix.
	name := path.Base(urlPath)
	name = strings.TrimSuffix(name, ".git")

	if name == "" || name == "." || name == "/" {
		return "", fmt.Errorf("could not extract project name from %q", gitURL)
	}

	return name, nil
}

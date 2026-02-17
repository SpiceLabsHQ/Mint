package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// projectListDeps holds the injectable dependencies for the project list command.
type projectListDeps struct {
	describe mintaws.DescribeInstancesAPI
	sendKey  mintaws.SendSSHPublicKeyAPI
	owner    string
	remote   RemoteCommandRunner
}

// projectInfo represents a project on the VM with its container status.
type projectInfo struct {
	Name            string `json:"name"`
	ContainerStatus string `json:"container_status"`
	Image           string `json:"image"`
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
	cmd.AddCommand(newProjectListCommand())

	return cmd
}

// newProjectCommandWithListDeps creates the project command tree with explicit
// list dependencies for testing. The add subcommand uses production deps (nil).
func newProjectCommandWithListDeps(listDeps *projectListDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage projects on the VM",
		Long:  "Clone repositories, build devcontainers, and manage projects on the VM.",
	}

	cmd.AddCommand(newProjectAddCommand())
	cmd.AddCommand(newProjectListCommandWithDeps(listDeps))

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

// newProjectListCommand creates the production project list subcommand.
func newProjectListCommand() *cobra.Command {
	return newProjectListCommandWithDeps(nil)
}

// newProjectListCommandWithDeps creates the project list subcommand with explicit
// dependencies for testing.
func newProjectListCommandWithDeps(deps *projectListDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List projects on the VM",
		Long:  "List project directories under /mint/projects/ and their devcontainer status.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps != nil {
				return runProjectList(cmd, deps)
			}
			clients := awsClientsFromContext(cmd.Context())
			if clients == nil {
				return fmt.Errorf("AWS clients not configured")
			}
			return runProjectList(cmd, &projectListDeps{
				describe: clients.ec2Client,
				sendKey:  clients.icClient,
				owner:    clients.owner,
				remote:   defaultRemoteRunner,
			})
		},
	}
}

// runProjectList executes the project list logic: discover VM, list project
// directories and running containers, match them, and display results.
func runProjectList(cmd *cobra.Command, deps *projectListDeps) error {
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

	// List project directories.
	lsCmd := []string{"ls", "-1", "/mint/projects/"}
	lsOutput, err := deps.remote(ctx, deps.sendKey, found.ID, found.AvailabilityZone,
		found.PublicIP, defaultSSHPort, defaultSSHUser, lsCmd)
	if err != nil {
		return fmt.Errorf("listing projects: %w", err)
	}

	// List running containers with devcontainer label.
	dockerCmd := []string{
		"docker", "ps", "-a",
		"--format", "{{.Names}}\t{{.Status}}\t{{.Image}}\t{{.Label \"devcontainer.local_folder\"}}",
		"--filter", "label=devcontainer.local_folder",
	}
	dockerOutput, err := deps.remote(ctx, deps.sendKey, found.ID, found.AvailabilityZone,
		found.PublicIP, defaultSSHPort, defaultSSHUser, dockerCmd)
	if err != nil {
		// Docker errors are non-fatal; just show projects without container info.
		dockerOutput = nil
	}

	projects := parseProjectsAndContainers(string(lsOutput), string(dockerOutput))

	w := cmd.OutOrStdout()
	if jsonOutput {
		return writeProjectListJSON(w, projects)
	}
	writeProjectListHuman(w, projects)
	return nil
}

// parseProjectsAndContainers parses the output of ls and docker ps to build
// a list of projects with their container status.
func parseProjectsAndContainers(lsOutput, dockerOutput string) []projectInfo {
	// Parse project directory names.
	var projectNames []string
	for _, line := range strings.Split(lsOutput, "\n") {
		name := strings.TrimSpace(line)
		if name != "" {
			projectNames = append(projectNames, name)
		}
	}

	if len(projectNames) == 0 {
		return nil
	}

	// Parse docker output into a map of project path -> container info.
	type containerInfo struct {
		status string
		image  string
	}
	containers := make(map[string]containerInfo)

	for _, line := range strings.Split(dockerOutput, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Format: name\tstatus\timage\tlocal_folder
		parts := strings.Split(line, "\t")
		if len(parts) < 4 {
			continue
		}
		folder := strings.TrimSpace(parts[3])
		status := normalizeContainerStatus(parts[1])
		image := strings.TrimSpace(parts[2])
		containers[folder] = containerInfo{status: status, image: image}
	}

	// Match projects to containers.
	var projects []projectInfo
	for _, name := range projectNames {
		projectPath := "/mint/projects/" + name
		p := projectInfo{
			Name:            name,
			ContainerStatus: "none",
		}
		if c, ok := containers[projectPath]; ok {
			p.ContainerStatus = c.status
			p.Image = c.image
		}
		projects = append(projects, p)
	}

	return projects
}

// normalizeContainerStatus converts a docker status string to a simplified
// status label: "running", "exited", "created", "paused", or the raw status.
func normalizeContainerStatus(rawStatus string) string {
	lower := strings.ToLower(rawStatus)
	switch {
	case strings.HasPrefix(lower, "up"):
		return "running"
	case strings.HasPrefix(lower, "exited"):
		return "exited"
	case strings.HasPrefix(lower, "created"):
		return "created"
	case strings.HasPrefix(lower, "paused"):
		return "paused"
	default:
		return lower
	}
}

// writeProjectListJSON outputs projects as a JSON array.
func writeProjectListJSON(w io.Writer, projects []projectInfo) error {
	if projects == nil {
		projects = []projectInfo{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(projects)
}

// writeProjectListHuman outputs projects as a human-readable table.
func writeProjectListHuman(w io.Writer, projects []projectInfo) {
	if len(projects) == 0 {
		fmt.Fprintln(w, "No projects found")
		return
	}

	fmt.Fprintf(w, "%-20s  %-10s  %s\n", "PROJECT", "STATUS", "IMAGE")
	for _, p := range projects {
		image := p.Image
		if image == "" {
			image = "\u2014"
		}
		fmt.Fprintf(w, "%-20s  %-10s  %s\n", p.Name, p.ContainerStatus, image)
	}
}

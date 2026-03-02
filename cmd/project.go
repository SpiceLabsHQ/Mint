package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strings"

	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/spf13/cobra"

	mintaws "github.com/SpiceLabsHQ/Mint/internal/aws"
	"github.com/SpiceLabsHQ/Mint/internal/cli"
	"github.com/SpiceLabsHQ/Mint/internal/config"
	"github.com/SpiceLabsHQ/Mint/internal/sshconfig"
	"github.com/SpiceLabsHQ/Mint/internal/vm"
)

// projectAddDeps holds the injectable dependencies for the project add command.
type projectAddDeps struct {
	describe        mintaws.DescribeInstancesAPI
	sendKey         mintaws.SendSSHPublicKeyAPI
	owner           string
	remote          RemoteCommandRunner
	streamingRunner StreamingRemoteRunner
	hostKeyStore    *sshconfig.HostKeyStore
	hostKeyScanner  HostKeyScanner
}

// projectListDeps holds the injectable dependencies for the project list command.
type projectListDeps struct {
	describe mintaws.DescribeInstancesAPI
	sendKey  mintaws.SendSSHPublicKeyAPI
	owner    string
	remote   RemoteCommandRunner
}

// projectRebuildDeps holds the injectable dependencies for the project rebuild command.
type projectRebuildDeps struct {
	describe        mintaws.DescribeInstancesAPI
	sendKey         mintaws.SendSSHPublicKeyAPI
	owner           string
	remote          RemoteCommandRunner
	streamingRunner StreamingRemoteRunner
	stdin           io.Reader
	hostKeyStore    *sshconfig.HostKeyStore
	hostKeyScanner  HostKeyScanner
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
	cmd.AddCommand(newProjectRebuildCommand())

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

// newProjectCommandWithRebuildDeps creates the project command tree with explicit
// rebuild dependencies for testing.
func newProjectCommandWithRebuildDeps(rebuildDeps *projectRebuildDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage projects on the VM",
		Long:  "Clone repositories, build devcontainers, and manage projects on the VM.",
	}

	cmd.AddCommand(newProjectAddCommand())
	cmd.AddCommand(newProjectListCommand())
	cmd.AddCommand(newProjectRebuildCommandWithDeps(rebuildDeps))

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
			configDir := config.DefaultConfigDir()
			return runProjectAdd(cmd, &projectAddDeps{
				describe:        clients.ec2Client,
				sendKey:         clients.icClient,
				owner:           clients.owner,
				remote:          defaultRemoteRunner,
				streamingRunner: defaultStreamingRemoteRunner,
				hostKeyStore:    sshconfig.NewHostKeyStore(configDir),
				hostKeyScanner:  defaultHostKeyScanner,
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

	// Expand GitHub shorthand "owner/repo" → full HTTPS URL.
	gitURL = expandGitHubShorthand(gitURL)

	// Derive project name from URL or --name flag.
	projectName, err := extractProjectName(gitURL)
	if err != nil {
		return fmt.Errorf("invalid git URL %q: %w", gitURL, err)
	}

	nameOverride, _ := cmd.Flags().GetString("name")
	if nameOverride != "" {
		projectName = nameOverride
	}

	if err := validateProjectName(projectName); err != nil {
		return err
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

	// Build a TOFU-verified remote runner for write commands (ADR-0019).
	remote := deps.remote
	if deps.hostKeyStore != nil && deps.hostKeyScanner != nil {
		tofu := NewTOFURemoteRunner(deps.remote, deps.hostKeyStore, deps.hostKeyScanner, vmName)
		remote = tofu.Run
	}

	w := cmd.OutOrStdout()
	projectPath := fmt.Sprintf("/mint/projects/%s", projectName)

	// State detection: check what already exists on the VM to enable
	// resume-from-failure. The first remote command also triggers TOFU
	// host key verification (ADR-0019).
	dirExists := false
	containerID := ""
	tmuxExists := false

	// Check 1: Does the project directory exist?
	dirCheckCmd := []string{"test", "-d", projectPath}
	_, err = remote(ctx, deps.sendKey, found.ID, found.AvailabilityZone,
		found.PublicIP, defaultSSHPort, defaultSSHUser, dirCheckCmd)
	if err != nil {
		if isTOFUError(err) {
			return err
		}
		// Directory does not exist — fresh start.
	} else {
		dirExists = true

		// Check 2: Is a container running for this project?
		containerCheckCmd := []string{
			"docker", "ps", "-q",
			"--filter", fmt.Sprintf("label=devcontainer.local_folder=%s", projectPath),
		}
		containerOutput, containerErr := remote(ctx, deps.sendKey, found.ID, found.AvailabilityZone,
			found.PublicIP, defaultSSHPort, defaultSSHUser, containerCheckCmd)
		if containerErr == nil {
			containerID = strings.TrimSpace(string(containerOutput))
		}

		if containerID != "" {
			// Check 3: Does a tmux session already exist?
			tmuxCheckCmd := []string{"tmux", "has-session", "-t", projectName}
			_, tmuxErr := remote(ctx, deps.sendKey, found.ID, found.AvailabilityZone,
				found.PublicIP, defaultSSHPort, defaultSSHUser, tmuxCheckCmd)
			tmuxExists = tmuxErr == nil
		}
	}

	// If everything exists, nothing to do.
	if dirExists && containerID != "" && tmuxExists {
		fmt.Fprintf(w, "Project %q is already fully set up.\n", projectName)
		return nil
	}

	// Resolve the streaming runner — use deps.streamingRunner for long-running
	// commands (git clone, devcontainer up) so users see progress on stderr.
	// TOFU was verified by the state check above, so StrictHostKeyChecking=no
	// is safe for subsequent calls in the same invocation.
	streaming := deps.streamingRunner
	if streaming == nil {
		streaming = defaultStreamingRemoteRunner
	}

	// Clone step: skip if directory already exists.
	if !dirExists {
		fmt.Fprintf(w, "Cloning %s...\n", gitURL)
		cloneCmd := buildCloneCommand(gitURL, projectPath, branch)
		_, err = streaming(ctx, deps.sendKey, found.ID, found.AvailabilityZone,
			found.PublicIP, defaultSSHPort, defaultSSHUser, cloneCmd, os.Stderr)
		if err != nil {
			return fmt.Errorf("cloning repository: %w", err)
		}
	} else if containerID == "" {
		fmt.Fprintf(w, "Found existing clone for %q, resuming from devcontainer build.\n", projectName)
	}

	// Build step: skip if container is already running.
	if containerID == "" {
		fmt.Fprintf(w, "Building devcontainer...\n")
		buildCmd := []string{"devcontainer", "up", "--workspace-folder", projectPath}
		_, err = streaming(ctx, deps.sendKey, found.ID, found.AvailabilityZone,
			found.PublicIP, defaultSSHPort, defaultSSHUser, buildCmd, os.Stderr)
		if err != nil {
			return fmt.Errorf("building devcontainer: %w", err)
		}

		// Discover the container ID after build.
		dockerPsCmd := []string{
			"docker", "ps", "-q",
			"--filter", fmt.Sprintf("label=devcontainer.local_folder=%s", projectPath),
		}
		containerOutput, containerErr := remote(ctx, deps.sendKey, found.ID, found.AvailabilityZone,
			found.PublicIP, defaultSSHPort, defaultSSHUser, dockerPsCmd)
		if containerErr == nil {
			containerID = strings.TrimSpace(string(containerOutput))
		}
	} else {
		fmt.Fprintf(w, "Found running container for %q, resuming from session creation.\n", projectName)
	}

	// Tmux step: create session.
	fmt.Fprintf(w, "Creating tmux session...\n")
	var tmuxCmd []string
	if containerID == "" {
		fmt.Fprintf(w, "Warning: Container not found after build. Creating tmux session without docker exec.\n")
		tmuxCmd = []string{"tmux", "new-session", "-d", "-s", projectName}
	} else {
		tmuxCmd = []string{"tmux", "new-session", "-d", "-s", projectName,
			"docker", "exec", "-it", containerID, "/bin/bash"}
	}
	_, err = remote(ctx, deps.sendKey, found.ID, found.AvailabilityZone,
		found.PublicIP, defaultSSHPort, defaultSSHUser, tmuxCmd)
	if err != nil {
		return fmt.Errorf("creating tmux session: %w", err)
	}

	fmt.Fprintf(w, "\nProject %q ready at %s\n", projectName, projectPath)
	return nil
}

// buildCloneCommand constructs the git clone command arguments.
//
// Three env vars ensure the clone is fully anonymous — no credential helpers,
// no interactive prompts — regardless of what the VM's system or user git
// config contains:
//
//   - GIT_TERMINAL_PROMPT=0: prevents git from opening /dev/tty for prompts,
//     which fail with "No such device or address" over non-interactive SSH
//     sessions (BatchMode=yes).
//   - GIT_CONFIG_NOSYSTEM=1: skips /etc/gitconfig, where system-level credential
//     helpers (e.g. git-credential-libsecret, git-credential-manager) are
//     registered. Without this, those helpers run first and fail headlessly,
//     causing git to fall back to a terminal prompt even for public repos.
//   - GIT_CONFIG_GLOBAL=/dev/null: points the global config at an empty file,
//     bypassing any credential helpers in ~/.gitconfig.
//
// Together these guarantee an unauthenticated HTTPS clone for public repos
// and an SSH-key clone for git@ URLs, with no dependence on any credential
// helper that may be installed on the VM.
func buildCloneCommand(gitURL, projectPath, branch string) []string {
	cmd := []string{
		"env",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"git",
		"clone",
	}
	if branch != "" {
		cmd = append(cmd, "--branch", branch)
	}
	cmd = append(cmd, gitURL, projectPath)
	return cmd
}

// expandGitHubShorthand converts "owner/repo" shorthand to a full GitHub HTTPS
// URL. Inputs that already look like a URL (contain "://" or "@") are returned
// unchanged.
func expandGitHubShorthand(gitURL string) string {
	if strings.Contains(gitURL, "://") || strings.Contains(gitURL, "@") {
		return gitURL
	}
	// owner/repo or owner/repo.git — no slashes beyond one separator allowed.
	parts := strings.Split(gitURL, "/")
	if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		return "https://github.com/" + gitURL
	}
	return gitURL
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
	var urlPath string
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

// projectNamePattern enforces safe project names: alphanumeric start, then
// alphanumeric plus dots, hyphens, and underscores. This prevents shell
// injection when names are interpolated into remote commands.
var projectNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// validateProjectName checks that a project name is safe for use in shell
// commands and file paths. Returns an error if the name contains shell
// metacharacters or does not start with an alphanumeric character.
func validateProjectName(name string) error {
	if name == "" {
		return fmt.Errorf("invalid project name: must not be empty")
	}
	if !projectNamePattern.MatchString(name) {
		return fmt.Errorf("invalid project name %q: must start with alphanumeric and contain only alphanumeric, dots, hyphens, or underscores", name)
	}
	return nil
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
		if name != "" && name != "lost+found" {
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
		fmt.Fprintln(w, "No projects yet — run mint project add <git-url> to clone one.")
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

// newProjectRebuildCommand creates the production project rebuild subcommand.
func newProjectRebuildCommand() *cobra.Command {
	return newProjectRebuildCommandWithDeps(nil)
}

// newProjectRebuildCommandWithDeps creates the project rebuild subcommand with
// explicit dependencies for testing.
func newProjectRebuildCommandWithDeps(deps *projectRebuildDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "rebuild <project-name>",
		Short: "Tear down and rebuild a project's devcontainer",
		Long: "Stop and remove the existing devcontainer for a project, " +
			"then rebuild it with devcontainer up. Requires confirmation " +
			"unless --yes is set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps != nil {
				return runProjectRebuild(cmd, deps, args[0])
			}
			clients := awsClientsFromContext(cmd.Context())
			if clients == nil {
				return fmt.Errorf("AWS clients not configured")
			}
			configDir := config.DefaultConfigDir()
			return runProjectRebuild(cmd, &projectRebuildDeps{
				describe:        clients.ec2Client,
				sendKey:         clients.icClient,
				owner:           clients.owner,
				remote:          defaultRemoteRunner,
				streamingRunner: defaultStreamingRemoteRunner,
				stdin:           cmd.InOrStdin(),
				hostKeyStore:    sshconfig.NewHostKeyStore(configDir),
				hostKeyScanner:  defaultHostKeyScanner,
			}, args[0])
		},
	}
}

// runProjectRebuild executes the project rebuild logic: discover VM, verify
// project exists, confirm, stop container, remove container, rebuild.
func runProjectRebuild(cmd *cobra.Command, deps *projectRebuildDeps, projectName string) error {
	if err := validateProjectName(projectName); err != nil {
		return err
	}

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

	// Build a TOFU-verified remote runner for write commands (ADR-0019).
	remote := deps.remote
	if deps.hostKeyStore != nil && deps.hostKeyScanner != nil {
		tofu := NewTOFURemoteRunner(deps.remote, deps.hostKeyStore, deps.hostKeyScanner, vmName)
		remote = tofu.Run
	}

	w := cmd.OutOrStdout()
	projectPath := fmt.Sprintf("/mint/projects/%s", projectName)

	// Step 1: Verify project exists.
	fmt.Fprintf(w, "Verifying project %q exists...\n", projectName)
	testCmd := []string{"test", "-d", projectPath}
	_, err = remote(ctx, deps.sendKey, found.ID, found.AvailabilityZone,
		found.PublicIP, defaultSSHPort, defaultSSHUser, testCmd)
	if err != nil {
		// Propagate TOFU host key errors directly instead of masking them
		// as "project not found".
		if isTOFUError(err) {
			return err
		}
		return fmt.Errorf("project %q not found — run mint project list to see available projects", projectName)
	}

	// Step 2: Confirmation prompt (unless --yes).
	if !yes {
		fmt.Fprintf(w, "This will destroy and rebuild the devcontainer for %q.\n", projectName)
		fmt.Fprintf(w, "Type the project name to confirm: ")

		stdin := deps.stdin
		if stdin == nil {
			stdin = cmd.InOrStdin()
		}
		scanner := bufio.NewScanner(stdin)
		if scanner.Scan() {
			input := strings.TrimSpace(scanner.Text())
			if input != projectName {
				return fmt.Errorf("confirmation %q does not match project name %q — rebuild aborted", input, projectName)
			}
		} else {
			return fmt.Errorf("no confirmation input received — rebuild aborted")
		}
	}

	// Step 3: Stop container (graceful if none found).
	fmt.Fprintf(w, "Stopping container...\n")
	stopCmd := []string{
		"sh", "-c",
		fmt.Sprintf("docker stop $(docker ps -q --filter label=devcontainer.local_folder=%s) 2>/dev/null || true", projectPath),
	}
	_, err = remote(ctx, deps.sendKey, found.ID, found.AvailabilityZone,
		found.PublicIP, defaultSSHPort, defaultSSHUser, stopCmd)
	if err != nil {
		return fmt.Errorf("stopping container: %w", err)
	}

	// Step 4: Remove container (graceful if none found).
	fmt.Fprintf(w, "Removing container...\n")
	rmCmd := []string{
		"sh", "-c",
		fmt.Sprintf("docker rm $(docker ps -aq --filter label=devcontainer.local_folder=%s) 2>/dev/null || true", projectPath),
	}
	_, err = remote(ctx, deps.sendKey, found.ID, found.AvailabilityZone,
		found.PublicIP, defaultSSHPort, defaultSSHUser, rmCmd)
	if err != nil {
		return fmt.Errorf("removing container: %w", err)
	}

	// Step 5: Rebuild devcontainer (streaming stderr for progress).
	// TOFU was verified by the test -d check above, so streaming calls
	// with StrictHostKeyChecking=no are safe.
	streaming := deps.streamingRunner
	if streaming == nil {
		streaming = defaultStreamingRemoteRunner
	}
	fmt.Fprintf(w, "Rebuilding devcontainer...\n")
	buildCmd := []string{"devcontainer", "up", "--workspace-folder", projectPath}
	_, err = streaming(ctx, deps.sendKey, found.ID, found.AvailabilityZone,
		found.PublicIP, defaultSSHPort, defaultSSHUser, buildCmd, os.Stderr)
	if err != nil {
		return fmt.Errorf("rebuilding devcontainer: %w", err)
	}

	// Step 6: Discover new container ID for docker exec (ADR-0003).
	fmt.Fprintf(w, "Reconnecting tmux session...\n")
	dockerPsCmd := []string{
		"docker", "ps", "-q",
		"--filter", fmt.Sprintf("label=devcontainer.local_folder=%s", projectPath),
	}
	containerOutput, err := remote(ctx, deps.sendKey, found.ID, found.AvailabilityZone,
		found.PublicIP, defaultSSHPort, defaultSSHUser, dockerPsCmd)
	if err != nil {
		containerOutput = nil
	}
	containerID := strings.TrimSpace(string(containerOutput))

	// Step 7: Kill existing tmux session (graceful — ignore errors).
	killCmd := []string{"tmux", "kill-session", "-t", projectName}
	_, _ = remote(ctx, deps.sendKey, found.ID, found.AvailabilityZone,
		found.PublicIP, defaultSSHPort, defaultSSHUser, killCmd)

	// Step 8: Create new tmux session with docker exec into container.
	var tmuxCmd []string
	if containerID == "" {
		fmt.Fprintf(w, "Warning: Container not found after build. Creating tmux session without docker exec.\n")
		tmuxCmd = []string{"tmux", "new-session", "-d", "-s", projectName}
	} else {
		tmuxCmd = []string{"tmux", "new-session", "-d", "-s", projectName,
			"docker", "exec", "-it", containerID, "/bin/bash"}
	}
	_, err = remote(ctx, deps.sendKey, found.ID, found.AvailabilityZone,
		found.PublicIP, defaultSSHPort, defaultSSHUser, tmuxCmd)
	if err != nil {
		return fmt.Errorf("creating tmux session: %w", err)
	}

	fmt.Fprintf(w, "Rebuilt devcontainer for %q\n", projectName)
	return nil
}

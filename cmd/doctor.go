package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/spf13/cobra"

	mintaws "github.com/SpiceLabsHQ/Mint/internal/aws"
	"github.com/SpiceLabsHQ/Mint/internal/cli"
	"github.com/SpiceLabsHQ/Mint/internal/config"
	"github.com/SpiceLabsHQ/Mint/internal/identity"
	"github.com/SpiceLabsHQ/Mint/internal/sshconfig"
	"github.com/SpiceLabsHQ/Mint/internal/tags"
	"github.com/SpiceLabsHQ/Mint/internal/vm"
)

// identityResolverAPI abstracts identity resolution for the doctor command.
// In production the owner is already resolved by PersistentPreRunE, so the
// production implementation just returns the cached owner. In tests, the
// real identity.Resolver is injected to exercise credential checking.
type identityResolverAPI interface {
	Resolve(ctx context.Context) (*identity.Owner, error)
}

// doctorDeps holds the injectable dependencies for the doctor command.
type doctorDeps struct {
	identityResolver  identityResolverAPI
	describeAddresses mintaws.DescribeAddressesAPI
	describe          mintaws.DescribeInstancesAPI
	sendKey           mintaws.SendSSHPublicKeyAPI
	remoteRun         RemoteCommandRunner
	configDir         string
	sshConfigPath     string
	owner             string
}

// cachedOwnerResolver is a production implementation of identityResolverAPI
// that returns a pre-resolved owner without making another STS call.
type cachedOwnerResolver struct {
	name string
	arn  string
}

func (c *cachedOwnerResolver) Resolve(_ context.Context) (*identity.Owner, error) {
	return &identity.Owner{Name: c.name, ARN: c.arn}, nil
}

// errorIdentityResolver is an identityResolverAPI implementation that always
// returns a fixed error. Used by doctor when AWS credentials are unavailable so
// that checkCredentials can report the failure as a check result rather than
// crashing the command.
type errorIdentityResolver struct {
	err error
}

func (e *errorIdentityResolver) Resolve(_ context.Context) (*identity.Owner, error) {
	return nil, e.err
}

// newDoctorCommand creates the production doctor command.
func newDoctorCommand() *cobra.Command {
	return newDoctorCommandWithDeps(nil)
}

// newDoctorCommandWithDeps creates the doctor command with explicit dependencies
// for testing. When deps is nil, the command wires real AWS clients.
func newDoctorCommandWithDeps(deps *doctorDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check environment and VM health",
		Long: "Run environment health checks including AWS credentials, " +
			"mint configuration, SSH config, EIP quota, and VM-specific checks " +
			"(health tag, disk usage, component versions). Use --fix to " +
			"reinstall failed components.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps != nil {
				return runDoctor(cmd, deps)
			}

			configDir := config.DefaultConfigDir()

			// doctor initializes its own AWS clients (commandNeedsAWS returns false
			// for doctor) so that a credential failure is surfaced as a check result
			// rather than a fatal startup error. When credentials are unavailable,
			// non-AWS checks (config, SSH config) still run.
			clients, awsErr := initAWSClients(cmd.Context())
			if awsErr != nil {
				return runDoctor(cmd, &doctorDeps{
					identityResolver: &errorIdentityResolver{err: awsErr},
					configDir:        configDir,
					sshConfigPath:    defaultSSHConfigPath(),
				})
			}
			return runDoctor(cmd, &doctorDeps{
				identityResolver: &cachedOwnerResolver{
					name: clients.owner,
					arn:  clients.ownerARN,
				},
				describeAddresses: clients.ec2Client,
				describe:          clients.ec2Client,
				sendKey:           clients.icClient,
				remoteRun:         defaultRemoteRunner,
				configDir:         configDir,
				sshConfigPath:     defaultSSHConfigPath(),
				owner:             clients.owner,
			})
		},
	}

	cmd.Flags().Bool("fix", false, "Re-install components that failed version checks")

	return cmd
}

// checkResult represents the outcome of a single doctor check.
type checkResult struct {
	name    string
	status  string // "PASS", "FAIL", "WARN"
	message string
}

// checkResultJSON is the JSON representation of a single doctor check.
type checkResultJSON struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// regionFormatPattern matches valid AWS region formats like us-east-1.
var regionFormatPattern = regexp.MustCompile(`^[a-z]{2}-[a-z]+-\d+$`)

// runDoctor executes all environment health checks and reports results.
func runDoctor(cmd *cobra.Command, deps *doctorDeps) error {
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

	fixMode, _ := cmd.Flags().GetBool("fix")

	w := cmd.OutOrStdout()
	var results []checkResult

	// 1. AWS credential check
	results = append(results, checkCredentials(ctx, deps))

	// 2. Config checks (region, volume_size_gb, idle_timeout_minutes)
	results = append(results, checkConfig(deps)...)

	// 3. SSH config check
	results = append(results, checkSSHConfig(deps))

	// 4. EIP quota headroom
	results = append(results, checkEIPQuota(ctx, deps))

	// 5. VM-specific checks (only when describe is available)
	if deps.describe != nil {
		vmResults := runVMChecks(ctx, deps, vmName, fixMode)
		results = append(results, vmResults...)
	}

	if jsonOutput {
		return printResultsJSON(w, results)
	}

	// Print results and determine exit status.
	hasFail := printResults(w, results)
	if hasFail {
		return fmt.Errorf("one or more checks failed")
	}
	return nil
}

// runVMChecks discovers VMs and runs health checks on each.
// When vmName is not "default" (i.e., --vm was specified), only that VM is
// checked. Otherwise, all running VMs owned by the user are checked.
func runVMChecks(ctx context.Context, deps *doctorDeps, vmName string, fixMode bool) []checkResult {
	var vms []*vm.VM
	var err error

	if vmName != "default" {
		// --vm flag specified: check only that VM.
		found, findErr := vm.FindVM(ctx, deps.describe, deps.owner, vmName)
		if findErr != nil {
			return []checkResult{{
				name:    fmt.Sprintf("vm/%s", vmName),
				status:  "WARN",
				message: fmt.Sprintf("could not discover VM: %v", findErr),
			}}
		}
		if found != nil {
			vms = []*vm.VM{found}
		}
	} else {
		// No --vm: check all running VMs.
		vms, err = vm.ListVMs(ctx, deps.describe, deps.owner)
		if err != nil {
			return []checkResult{{
				name:    "vm-discovery",
				status:  "WARN",
				message: fmt.Sprintf("could not list VMs: %v", err),
			}}
		}
	}

	if len(vms) == 0 {
		return nil // no VMs to check
	}

	var results []checkResult
	for _, v := range vms {
		results = append(results, checkVM(ctx, deps, v, fixMode)...)
	}
	return results
}

// checkVM runs all health checks for a single VM.
func checkVM(ctx context.Context, deps *doctorDeps, v *vm.VM, fixMode bool) []checkResult {
	prefix := fmt.Sprintf("vm/%s", v.Name)
	var results []checkResult

	// Skip non-running VMs.
	if v.State != string(ec2types.InstanceStateNameRunning) {
		results = append(results, checkResult{
			name:    prefix,
			status:  "WARN",
			message: fmt.Sprintf("VM is %s — skipping checks", v.State),
		})
		return results
	}

	// 1. Health tag check.
	results = append(results, checkHealthTag(v, prefix))

	// Skip SSH-based checks if we don't have the SSH deps.
	if deps.remoteRun == nil || deps.sendKey == nil {
		return results
	}

	// 2. Disk usage check.
	results = append(results, checkDiskUsage(ctx, deps, v, prefix))

	// 3. Component version checks.
	components := checkComponents(ctx, deps, v, prefix)
	results = append(results, components...)

	// 4. Fix mode: reinstall failed components.
	if fixMode {
		results = append(results, fixFailedComponents(ctx, deps, v, prefix, components)...)
	}

	return results
}

// checkHealthTag reads the mint:health tag and reports its status.
func checkHealthTag(v *vm.VM, prefix string) checkResult {
	health, ok := v.Tags[tags.TagHealth]
	if !ok {
		return checkResult{
			name:    prefix + "/health",
			status:  "WARN",
			message: "mint:health tag missing",
		}
	}

	switch health {
	case "healthy":
		return checkResult{
			name:    prefix + "/health",
			status:  "PASS",
			message: "healthy",
		}
	case "drift-detected":
		return checkResult{
			name:    prefix + "/health",
			status:  "WARN",
			message: "drift-detected",
		}
	default:
		return checkResult{
			name:    prefix + "/health",
			status:  "WARN",
			message: fmt.Sprintf("unknown health status: %s", health),
		}
	}
}

// checkDiskUsage retrieves disk usage via SSH and reports the result.
func checkDiskUsage(ctx context.Context, deps *doctorDeps, v *vm.VM, prefix string) checkResult {
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
		if isSSHConnectionError(err) {
			return checkResult{
				name:   prefix + "/disk",
				status: "WARN",
				message: fmt.Sprintf("cannot connect to VM (port 41122 refused) — "+
					"bootstrap may be incomplete, run 'mint doctor' for details"),
			}
		}
		return checkResult{
			name:    prefix + "/disk",
			status:  "WARN",
			message: fmt.Sprintf("could not check disk usage: %v", err),
		}
	}

	pct, err := parseDiskUsagePct(string(output))
	if err != nil {
		return checkResult{
			name:    prefix + "/disk",
			status:  "WARN",
			message: fmt.Sprintf("could not parse disk usage: %v", err),
		}
	}

	if pct >= 90 {
		return checkResult{
			name:    prefix + "/disk",
			status:  "FAIL",
			message: fmt.Sprintf("%d%% used — critically low disk space", pct),
		}
	}
	if pct >= 80 {
		return checkResult{
			name:    prefix + "/disk",
			status:  "WARN",
			message: fmt.Sprintf("%d%% used — disk space running low", pct),
		}
	}
	return checkResult{
		name:    prefix + "/disk",
		status:  "PASS",
		message: fmt.Sprintf("%d%% used", pct),
	}
}

// componentCheck defines a component to check and how to fix it.
type componentCheck struct {
	name       string
	command    []string
	fixCommand []string
}

// doctorComponents returns the list of components to check.
func doctorComponents() []componentCheck {
	return []componentCheck{
		{
			name:       "docker",
			command:    []string{"docker", "--version"},
			fixCommand: []string{"sudo", "dnf", "install", "-y", "docker"},
		},
		{
			name:       "devcontainer",
			command:    []string{"devcontainer", "--version"},
			fixCommand: []string{"sudo", "npm", "install", "-g", "@devcontainers/cli"},
		},
		{
			name:       "tmux",
			command:    []string{"tmux", "-V"},
			fixCommand: []string{"sudo", "dnf", "install", "-y", "tmux"},
		},
		{
			name:       "mosh-server",
			command:    []string{"mosh-server", "--version"},
			fixCommand: []string{"sudo", "dnf", "install", "-y", "mosh"},
		},
	}
}

// checkComponents runs version checks for all expected VM components.
func checkComponents(ctx context.Context, deps *doctorDeps, v *vm.VM, prefix string) []checkResult {
	var results []checkResult

	for _, comp := range doctorComponents() {
		output, err := deps.remoteRun(
			ctx,
			deps.sendKey,
			v.ID,
			v.AvailabilityZone,
			v.PublicIP,
			defaultSSHPort,
			defaultSSHUser,
			comp.command,
		)
		if err != nil {
			if isSSHConnectionError(err) {
				results = append(results, checkResult{
					name:   prefix + "/" + comp.name,
					status: "FAIL",
					message: fmt.Sprintf("cannot connect to VM (port 41122 refused) — "+
						"bootstrap may be incomplete, run 'mint doctor' for details"),
				})
			} else {
				results = append(results, checkResult{
					name:    prefix + "/" + comp.name,
					status:  "FAIL",
					message: fmt.Sprintf("not found or error: %v", err),
				})
			}
			continue
		}

		ver := strings.TrimSpace(string(output))
		if ver == "" {
			results = append(results, checkResult{
				name:    prefix + "/" + comp.name,
				status:  "FAIL",
				message: "no version output",
			})
			continue
		}

		results = append(results, checkResult{
			name:    prefix + "/" + comp.name,
			status:  "PASS",
			message: ver,
		})
	}

	return results
}

// fixFailedComponents attempts to reinstall components that failed checks.
func fixFailedComponents(ctx context.Context, deps *doctorDeps, v *vm.VM, prefix string, componentResults []checkResult) []checkResult {
	var results []checkResult

	components := doctorComponents()
	for _, comp := range components {
		checkName := prefix + "/" + comp.name

		// Find the check result for this component.
		failed := false
		for _, r := range componentResults {
			if r.name == checkName && r.status == "FAIL" {
				failed = true
				break
			}
		}
		if !failed {
			continue
		}

		// Attempt reinstall.
		_, err := deps.remoteRun(
			ctx,
			deps.sendKey,
			v.ID,
			v.AvailabilityZone,
			v.PublicIP,
			defaultSSHPort,
			defaultSSHUser,
			comp.fixCommand,
		)
		if err != nil {
			results = append(results, checkResult{
				name:    prefix + "/" + comp.name + "/fix",
				status:  "FAIL",
				message: fmt.Sprintf("reinstall failed: %v", err),
			})
			continue
		}

		results = append(results, checkResult{
			name:    prefix + "/" + comp.name + "/fix",
			status:  "PASS",
			message: "reinstalled successfully",
		})
	}

	return results
}

// checkCredentials verifies that AWS credentials are valid by calling
// the identity resolver.
func checkCredentials(ctx context.Context, deps *doctorDeps) checkResult {
	owner, err := deps.identityResolver.Resolve(ctx)
	if err != nil {
		msg := fmt.Sprintf("could not resolve identity: %v", err)
		if isCredentialError(err) {
			msg = `not configured — run "aws configure", set AWS_PROFILE, or use --profile`
		}
		return checkResult{
			name:    "AWS credentials",
			status:  "FAIL",
			message: msg,
		}
	}
	return checkResult{
		name:    "AWS credentials",
		status:  "PASS",
		message: fmt.Sprintf("authenticated as %s", owner.Name),
	}
}

// checkConfig validates the mint configuration values.
func checkConfig(deps *doctorDeps) []checkResult {
	var results []checkResult

	cfg, err := config.Load(deps.configDir)
	if err != nil {
		results = append(results, checkResult{
			name:    "config",
			status:  "FAIL",
			message: fmt.Sprintf("could not load config: %v", err),
		})
		return results
	}

	// Region check
	if cfg.Region == "" {
		results = append(results, checkResult{
			name:    "region",
			status:  "FAIL",
			message: "region is not set — run mint config set region <region>",
		})
	} else if !regionFormatPattern.MatchString(cfg.Region) {
		results = append(results, checkResult{
			name:    "region",
			status:  "FAIL",
			message: fmt.Sprintf("region %q does not match AWS region format (e.g., us-west-2)", cfg.Region),
		})
	} else {
		results = append(results, checkResult{
			name:    "region",
			status:  "PASS",
			message: cfg.Region,
		})
	}

	// volume_size_gb check
	if cfg.VolumeSizeGB < 50 {
		results = append(results, checkResult{
			name:    "volume_size_gb",
			status:  "FAIL",
			message: fmt.Sprintf("must be >= 50 (got %d)", cfg.VolumeSizeGB),
		})
	} else {
		results = append(results, checkResult{
			name:    "volume_size_gb",
			status:  "PASS",
			message: fmt.Sprintf("%d GB", cfg.VolumeSizeGB),
		})
	}

	// idle_timeout_minutes check
	if cfg.IdleTimeoutMinutes < 15 {
		results = append(results, checkResult{
			name:    "idle_timeout_minutes",
			status:  "FAIL",
			message: fmt.Sprintf("must be >= 15 (got %d)", cfg.IdleTimeoutMinutes),
		})
	} else {
		results = append(results, checkResult{
			name:    "idle_timeout_minutes",
			status:  "PASS",
			message: fmt.Sprintf("%d minutes", cfg.IdleTimeoutMinutes),
		})
	}

	return results
}

// checkSSHConfig verifies that the SSH managed block exists for the default VM.
func checkSSHConfig(deps *doctorDeps) checkResult {
	sshPath := deps.sshConfigPath
	if sshPath == "" {
		sshPath = defaultSSHConfigPath()
	}

	data, err := os.ReadFile(sshPath)
	if err != nil {
		return checkResult{
			name:    "SSH config",
			status:  "WARN",
			message: "SSH config file not found — run mint up to configure SSH automatically",
		}
	}

	_, found := sshconfig.ReadManagedBlock(string(data), "default")
	if !found {
		return checkResult{
			name:    "SSH config",
			status:  "WARN",
			message: "no mint managed block found — run mint up to configure SSH automatically",
		}
	}

	return checkResult{
		name:    "SSH config",
		status:  "PASS",
		message: "managed block present for default VM",
	}
}

// checkEIPQuota checks the number of allocated Elastic IPs against the default
// limit of 5. Warns if >= 4 are allocated. Returns SKIP when AWS clients are
// unavailable (e.g., no credentials).
func checkEIPQuota(ctx context.Context, deps *doctorDeps) checkResult {
	if deps.describeAddresses == nil {
		return checkResult{
			name:    "EIP quota",
			status:  "SKIP",
			message: "skipped — AWS credentials unavailable",
		}
	}
	out, err := deps.describeAddresses.DescribeAddresses(ctx, &ec2.DescribeAddressesInput{})
	if err != nil {
		return checkResult{
			name:    "EIP quota",
			status:  "WARN",
			message: fmt.Sprintf("could not check EIP quota: %v", err),
		}
	}

	count := len(out.Addresses)
	const defaultLimit = 5
	const warnThreshold = 4

	if count >= warnThreshold {
		return checkResult{
			name:    "EIP quota",
			status:  "WARN",
			message: fmt.Sprintf("%d of %d EIPs allocated — nearing limit", count, defaultLimit),
		}
	}

	return checkResult{
		name:    "EIP quota",
		status:  "PASS",
		message: fmt.Sprintf("%d of %d EIPs allocated", count, defaultLimit),
	}
}

// printResults writes the check results to the writer and returns true if
// any check failed.
func printResults(w io.Writer, results []checkResult) bool {
	hasFail := false
	for _, r := range results {
		fmt.Fprintf(w, "[%s] %s: %s\n", r.status, r.name, r.message)
		if r.status == "FAIL" {
			hasFail = true
		}
	}
	return hasFail
}

// printResultsJSON writes check results as a JSON array.
func printResultsJSON(w io.Writer, results []checkResult) error {
	jsonResults := make([]checkResultJSON, len(results))
	for i, r := range results {
		jsonResults[i] = checkResultJSON{
			Name:   r.name,
			Status: r.status,
			Detail: r.message,
		}
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(jsonResults); err != nil {
		return fmt.Errorf("encoding JSON: %w", err)
	}

	// Check for failures after JSON output. Use silentExitError so that
	// main.go exits 1 (signalling failure to scripts) without printing
	// an additional plaintext message to stderr — the JSON already encodes
	// the failure status (Bug #66).
	for _, r := range results {
		if r.status == "FAIL" {
			return silentExitError{}
		}
	}
	return nil
}

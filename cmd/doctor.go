package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/spf13/cobra"

	mintaws "github.com/nicholasgasior/mint/internal/aws"
	"github.com/nicholasgasior/mint/internal/config"
	"github.com/nicholasgasior/mint/internal/identity"
	"github.com/nicholasgasior/mint/internal/sshconfig"
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
	configDir         string
	sshConfigPath     string
	owner             string
}

// cachedOwnerResolver is the production implementation of identityResolverAPI.
// Since PersistentPreRunE already resolves the identity, this just returns
// the cached value without making another STS call.
type cachedOwnerResolver struct {
	name string
	arn  string
}

func (c *cachedOwnerResolver) Resolve(_ context.Context) (*identity.Owner, error) {
	return &identity.Owner{Name: c.name, ARN: c.arn}, nil
}

// newDoctorCommand creates the production doctor command.
func newDoctorCommand() *cobra.Command {
	return newDoctorCommandWithDeps(nil)
}

// newDoctorCommandWithDeps creates the doctor command with explicit dependencies
// for testing. When deps is nil, the command wires real AWS clients.
func newDoctorCommandWithDeps(deps *doctorDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check local environment health",
		Long: "Run local environment health checks to verify that AWS credentials, " +
			"mint configuration, SSH config, and EIP quota are properly set up.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps != nil {
				return runDoctor(cmd, deps)
			}
			clients := awsClientsFromContext(cmd.Context())
			if clients == nil {
				return fmt.Errorf("AWS clients not configured")
			}
			configDir := config.DefaultConfigDir()
			return runDoctor(cmd, &doctorDeps{
				identityResolver: &cachedOwnerResolver{
					name: clients.owner,
					arn:  clients.ownerARN,
				},
				describeAddresses: clients.ec2Client,
				configDir:         configDir,
				sshConfigPath:     defaultSSHConfigPath(),
				owner:             clients.owner,
			})
		},
	}
}

// checkResult represents the outcome of a single doctor check.
type checkResult struct {
	name    string
	status  string // "PASS", "FAIL", "WARN"
	message string
}

// regionFormatPattern matches valid AWS region formats like us-east-1.
var regionFormatPattern = regexp.MustCompile(`^[a-z]{2}-[a-z]+-\d+$`)

// runDoctor executes all local environment health checks and reports results.
func runDoctor(cmd *cobra.Command, deps *doctorDeps) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

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

	// Print results and determine exit status.
	hasFail := printResults(w, results)
	if hasFail {
		return fmt.Errorf("one or more checks failed")
	}
	return nil
}

// checkCredentials verifies that AWS credentials are valid by calling
// the identity resolver.
func checkCredentials(ctx context.Context, deps *doctorDeps) checkResult {
	owner, err := deps.identityResolver.Resolve(ctx)
	if err != nil {
		return checkResult{
			name:    "AWS credentials",
			status:  "FAIL",
			message: fmt.Sprintf("could not resolve identity: %v", err),
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
			message: "region is not set — run: mint config set region <region>",
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
			message: "SSH config file not found — run: mint ssh-config",
		}
	}

	_, found := sshconfig.ReadManagedBlock(string(data), "default")
	if !found {
		return checkResult{
			name:    "SSH config",
			status:  "WARN",
			message: "no mint managed block found — run: mint ssh-config",
		}
	}

	return checkResult{
		name:    "SSH config",
		status:  "PASS",
		message: "managed block present for default VM",
	}
}

// checkEIPQuota checks the number of allocated Elastic IPs against the default
// limit of 5. Warns if >= 4 are allocated.
func checkEIPQuota(ctx context.Context, deps *doctorDeps) checkResult {
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

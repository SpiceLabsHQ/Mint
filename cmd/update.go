package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/SpiceLabsHQ/Mint/internal/selfupdate"
)

// SelfUpdater abstracts the self-update operations for dependency injection.
type SelfUpdater interface {
	CheckLatest(ctx context.Context) (*selfupdate.Release, error)
	Download(ctx context.Context, release *selfupdate.Release, destDir string) (string, error)
	DownloadChecksums(ctx context.Context, release *selfupdate.Release) (string, error)
	VerifyChecksum(archivePath, checksumFileContent string) error
	Extract(archivePath, destDir string) (string, error)
	Apply(newBinaryPath, currentBinaryPath string) error
}

// updateDeps holds the injectable dependencies for the update command.
type updateDeps struct {
	updater    SelfUpdater
	binaryPath string // path to the currently running binary
}

// newUpdateCommand creates the production update command.
func newUpdateCommand() *cobra.Command {
	return newUpdateCommandWithDeps(nil)
}

// newUpdateCommandWithDeps creates the update command with explicit dependencies
// for testing. When deps is nil, the command wires real dependencies.
func newUpdateCommandWithDeps(deps *updateDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Update mint to the latest version",
		Long: "Download the latest release from GitHub, verify its SHA256 " +
			"checksum, and replace the current binary.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps != nil {
				return runUpdate(cmd, deps)
			}
			execPath, err := os.Executable()
			if err != nil {
				return fmt.Errorf("determine binary path: %w", err)
			}
			return runUpdate(cmd, &updateDeps{
				updater: &selfupdate.Updater{
					Client:         &http.Client{Timeout: 30 * time.Second},
					CurrentVersion: version,
				},
				binaryPath: execPath,
			})
		},
	}
}

// runUpdate executes the update command logic: check for update, download
// archive, download checksums, verify archive checksum, extract binary,
// and apply.
func runUpdate(cmd *cobra.Command, deps *updateDeps) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	w := cmd.OutOrStdout()

	// Check for latest release.
	fmt.Fprintln(w, "Checking for updates...")
	release, err := deps.updater.CheckLatest(ctx)
	if err != nil {
		var rateLimitErr *selfupdate.RateLimitError
		if errors.As(err, &rateLimitErr) {
			fmt.Fprintf(w, "Could not check for updates: GitHub API rate limit exceeded. Try again later.\n")
			return nil
		}
		var noReleasesErr *selfupdate.NoReleasesError
		if errors.As(err, &noReleasesErr) {
			fmt.Fprintf(w, "No releases found — mint may be running a pre-release version.\n")
			return nil
		}
		return fmt.Errorf("check for updates: %w", err)
	}
	if release == nil {
		fmt.Fprintf(w, "Already up to date (%s).\n", version)
		return nil
	}

	fmt.Fprintf(w, "New version available: %s (current: %s)\n", release.TagName, version)

	// Download the archive.
	fmt.Fprintln(w, "Downloading archive...")
	tmpDir, err := os.MkdirTemp("", "mint-update-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	archivePath, err := deps.updater.Download(ctx, release, tmpDir)
	if err != nil {
		return fmt.Errorf("download archive: %w", err)
	}

	// Download checksums.
	fmt.Fprintln(w, "Downloading checksums...")
	checksums, err := deps.updater.DownloadChecksums(ctx, release)
	if err != nil {
		return fmt.Errorf("download checksums: %w", err)
	}

	// Verify archive checksum BEFORE extraction.
	fmt.Fprintln(w, "Verifying checksum...")
	if err := deps.updater.VerifyChecksum(archivePath, checksums); err != nil {
		return fmt.Errorf("checksum verification failed: %w", err)
	}

	// Extract the binary from the verified archive.
	fmt.Fprintln(w, "Extracting binary...")
	binaryPath, err := deps.updater.Extract(archivePath, tmpDir)
	if err != nil {
		return fmt.Errorf("extract binary: %w", err)
	}

	// Apply the update.
	fmt.Fprintln(w, "Applying update...")
	if err := deps.updater.Apply(binaryPath, deps.binaryPath); err != nil {
		return fmt.Errorf("apply update: %w", err)
	}

	fmt.Fprintf(w, "Updated mint to %s.\n", release.TagName)
	return nil
}

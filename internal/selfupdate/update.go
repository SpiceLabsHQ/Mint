// Package selfupdate provides self-update functionality for the mint binary.
// It downloads the latest release from GitHub Releases, verifies SHA256
// checksums, and performs atomic binary replacement.
package selfupdate

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultAPIEndpoint is the GitHub API endpoint for the latest release.
	DefaultAPIEndpoint = "https://api.github.com/repos/SpiceLabsHQ/Mint/releases/latest"

	// maxBinarySize is the maximum allowed size for the extracted binary
	// (256 MB). Prevents unbounded extraction from malicious archives.
	maxBinarySize = 256 * 1024 * 1024
)

// Updater performs self-update operations against GitHub Releases.
type Updater struct {
	// Client is the HTTP client used for all requests.
	// If nil, a default client with 30s timeout is used.
	Client *http.Client

	// CurrentVersion is the running binary's version (e.g., "v1.0.0").
	CurrentVersion string

	// APIEndpoint overrides the GitHub API URL. Used for testing.
	// If empty, DefaultAPIEndpoint is used.
	APIEndpoint string
}

// Release holds the information about a GitHub release that has an
// update available.
type Release struct {
	TagName string
	Assets  []githubAsset
}

// githubRelease is the GitHub API response for a release.
type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

// githubAsset is a single asset attached to a GitHub release.
type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func (u *Updater) client() *http.Client {
	if u.Client != nil {
		return u.Client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (u *Updater) apiEndpoint() string {
	if u.APIEndpoint != "" {
		return u.APIEndpoint
	}
	return DefaultAPIEndpoint
}

// CheckLatest queries the GitHub Releases API for the latest release.
// Returns nil if the current version is already up to date.
// Returns an error on network or API failures.
func (u *Updater) CheckLatest(ctx context.Context) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.apiEndpoint(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := u.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch latest release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}

	if release.TagName == "" {
		return nil, fmt.Errorf("empty tag_name in GitHub release")
	}

	// Check if already on latest.
	if !isUpdateAvailable(u.CurrentVersion, release.TagName) {
		return nil, nil
	}

	return &Release{
		TagName: release.TagName,
		Assets:  release.Assets,
	}, nil
}

// Download fetches the archive asset for the current OS/arch from the release
// and saves the raw archive to destDir. Returns the path to the downloaded
// archive file. The archive must be verified with VerifyChecksum before
// extraction.
func (u *Updater) Download(ctx context.Context, release *Release, destDir string) (string, error) {
	assetName := assetNameForPlatform(release.TagName)

	var downloadURL string
	for _, a := range release.Assets {
		if a.Name == assetName {
			downloadURL = a.BrowserDownloadURL
			break
		}
	}
	if downloadURL == "" {
		return "", fmt.Errorf("no asset %q found in release %s", assetName, release.TagName)
	}

	// Security: enforce HTTPS on download URL.
	if !strings.HasPrefix(downloadURL, "https://") {
		return "", fmt.Errorf("refusing to download from non-HTTPS URL: %s", downloadURL)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return "", fmt.Errorf("create download request: %w", err)
	}

	resp, err := u.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("download asset: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download returned status %d", resp.StatusCode)
	}

	// Save the raw archive to disk for checksum verification before extraction.
	archivePath := filepath.Join(destDir, assetName)
	out, err := os.OpenFile(archivePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", fmt.Errorf("create archive file: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return "", fmt.Errorf("write archive: %w", err)
	}

	return archivePath, nil
}

// Extract extracts the "mint" binary from a tar.gz archive at archivePath
// into destDir. Returns the path to the extracted binary.
func (u *Updater) Extract(archivePath, destDir string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()

	binaryPath := filepath.Join(destDir, "mint")
	if err := extractBinaryFromTarGz(f, binaryPath); err != nil {
		return "", fmt.Errorf("extract binary: %w", err)
	}

	return binaryPath, nil
}

// DownloadChecksums fetches the checksums.txt asset from the release.
func (u *Updater) DownloadChecksums(ctx context.Context, release *Release) (string, error) {
	var downloadURL string
	for _, a := range release.Assets {
		if a.Name == "checksums.txt" {
			downloadURL = a.BrowserDownloadURL
			break
		}
	}
	if downloadURL == "" {
		return "", fmt.Errorf("no checksums.txt asset found in release %s", release.TagName)
	}

	// Security: enforce HTTPS on download URL.
	if !strings.HasPrefix(downloadURL, "https://") {
		return "", fmt.Errorf("refusing to download checksums from non-HTTPS URL: %s", downloadURL)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return "", fmt.Errorf("create checksums request: %w", err)
	}

	resp, err := u.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("download checksums: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("checksums download returned status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read checksums: %w", err)
	}

	return string(data), nil
}

// VerifyChecksum verifies the SHA256 checksum of the archive file at
// archivePath against the checksums.txt content. GoReleaser checksums
// hash the .tar.gz archive, not the extracted binary. The checksum file
// uses the format:
//
//	<sha256hash>  <filename>
//
// It looks for a line matching the current GOOS/GOARCH asset name.
func (u *Updater) VerifyChecksum(archivePath, checksumFileContent string) error {
	// Compute hash of the archive file.
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive for checksum: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hash archive: %w", err)
	}
	actualHash := fmt.Sprintf("%x", h.Sum(nil))

	// Find the expected hash in the checksum file.
	// Lines are: "<hash>  <filename>"
	// We match any line whose filename contains our OS/arch pattern.
	osArch := fmt.Sprintf("_%s_%s.", runtime.GOOS, runtime.GOARCH)

	var expectedHash string
	for _, line := range strings.Split(checksumFileContent, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}
		if strings.Contains(parts[1], osArch) {
			expectedHash = parts[0]
			break
		}
	}

	if expectedHash == "" {
		return fmt.Errorf("no checksum entry found for %s/%s in checksums.txt", runtime.GOOS, runtime.GOARCH)
	}

	if actualHash != expectedHash {
		return fmt.Errorf("checksum mismatch: expected %s, got %s — download may be corrupted", expectedHash, actualHash)
	}

	return nil
}

// Apply performs atomic binary replacement. The new binary is first copied
// to a temporary file in the same directory as the current binary (to
// guarantee same-filesystem rename), then atomically renamed into place.
// On failure, the original binary is left untouched.
func (u *Updater) Apply(newBinaryPath, currentBinaryPath string) error {
	// Verify source exists.
	if _, err := os.Stat(newBinaryPath); err != nil {
		return fmt.Errorf("new binary not found: %w", err)
	}

	// Copy the new binary to a temp file in the SAME directory as the
	// target to ensure os.Rename operates within a single filesystem.
	targetDir := filepath.Dir(currentBinaryPath)
	tmpFile, err := os.CreateTemp(targetDir, ".mint-update-*")
	if err != nil {
		return fmt.Errorf("create temp file in target dir: %w", err)
	}
	tmpPath := tmpFile.Name()

	// Clean up the temp file on any failure path.
	defer func() {
		// If tmpPath still exists at this point, something went wrong.
		_ = os.Remove(tmpPath)
	}()

	src, err := os.Open(newBinaryPath)
	if err != nil {
		tmpFile.Close()
		return fmt.Errorf("open new binary: %w", err)
	}

	if _, err := io.Copy(tmpFile, src); err != nil {
		src.Close()
		tmpFile.Close()
		return fmt.Errorf("copy new binary to target dir: %w", err)
	}
	src.Close()
	tmpFile.Close()

	// Ensure the temp file is executable.
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return fmt.Errorf("chmod new binary: %w", err)
	}

	// Atomic rename: backup old, rename new into place.
	backupPath := currentBinaryPath + ".bak"
	if err := os.Rename(currentBinaryPath, backupPath); err != nil {
		return fmt.Errorf("backup current binary: %w", err)
	}

	if err := os.Rename(tmpPath, currentBinaryPath); err != nil {
		// Attempt to restore the backup.
		_ = os.Rename(backupPath, currentBinaryPath)
		return fmt.Errorf("replace binary: %w", err)
	}

	// Clean up the backup.
	_ = os.Remove(backupPath)

	return nil
}

// assetNameForPlatform returns the expected asset filename for the current
// OS and architecture. GoReleaser convention:
//
//	mint_<version>_<os>_<arch>.tar.gz (linux/darwin)
//	mint_<version>_<os>_<arch>.zip    (windows)
//
// GoReleaser's name_template uses {{ .Version }}, which strips the leading
// "v" from a tag (e.g. tag "v1.2.0" → version "1.2.0"). tagName here is
// the raw GitHub tag (e.g. "v1.2.0"), so we strip the prefix before
// interpolating to produce a name that matches the actual release asset.
func assetNameForPlatform(tagName string) string {
	ext := ".tar.gz"
	if runtime.GOOS == "windows" {
		ext = ".zip"
	}
	version := strings.TrimPrefix(tagName, "v")
	return fmt.Sprintf("mint_%s_%s_%s%s", version, runtime.GOOS, runtime.GOARCH, ext)
}

// extractBinaryFromTarGz reads a tar.gz stream and extracts the file named
// "mint" (the binary) to the given output path. Extraction is bounded to
// maxBinarySize (256 MB) to prevent resource exhaustion from malicious
// archives.
func extractBinaryFromTarGz(r io.Reader, outputPath string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("open gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}

		// Look for the mint binary (could be "mint" or "./mint").
		name := filepath.Base(hdr.Name)
		if name == "mint" && hdr.Typeflag == tar.TypeReg {
			out, err := os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
			if err != nil {
				return fmt.Errorf("create output file: %w", err)
			}
			defer out.Close()

			// Bounded copy: prevent extraction of files larger than maxBinarySize.
			limited := io.LimitReader(tr, maxBinarySize+1)
			n, err := io.Copy(out, limited)
			if err != nil {
				return fmt.Errorf("write binary: %w", err)
			}
			if n > maxBinarySize {
				return fmt.Errorf("extracted binary exceeds maximum allowed size of %d bytes", maxBinarySize)
			}
			return nil
		}
	}

	return fmt.Errorf("mint binary not found in archive")
}

// isUpdateAvailable compares two semantic versions and returns true if
// latest is newer than current.
func isUpdateAvailable(current, latest string) bool {
	curMajor, curMinor, curPatch, ok := parseSemver(current)
	if !ok {
		return false
	}
	latMajor, latMinor, latPatch, ok := parseSemver(latest)
	if !ok {
		return false
	}

	if latMajor != curMajor {
		return latMajor > curMajor
	}
	if latMinor != curMinor {
		return latMinor > curMinor
	}
	return latPatch > curPatch
}

// parseSemver parses "v1.2.3" or "1.2.3" into components.
func parseSemver(v string) (major, minor, patch int, ok bool) {
	v = strings.TrimPrefix(v, "v")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil || major < 0 {
		return 0, 0, 0, false
	}
	minor, err = strconv.Atoi(parts[1])
	if err != nil || minor < 0 {
		return 0, 0, 0, false
	}
	patch, err = strconv.Atoi(parts[2])
	if err != nil || patch < 0 {
		return 0, 0, 0, false
	}

	return major, minor, patch, true
}

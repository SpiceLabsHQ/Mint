// Package version provides update checking against GitHub Releases.
//
// Check queries the GitHub Releases API for the latest version of Mint and
// caches the result for 24 hours. All errors (network, parse, cache) are
// swallowed — the check fails open so it never blocks CLI operations.
package version

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// defaultEndpoint is the GitHub API endpoint for the latest release.
	defaultEndpoint = "https://api.github.com/repos/nicholasgasior/mint/releases/latest"

	// cacheFileName is the name of the version cache file.
	cacheFileName = "version-cache.json"

	// cacheTTL is how long a cached version check remains valid.
	cacheTTL = 24 * time.Hour
)

// VersionInfo holds the result of a version check.
type VersionInfo struct {
	LatestVersion  string
	UpdateAvailable bool
	CheckedAt      time.Time
}

// cacheFile is the on-disk representation of a cached version check.
type cacheFile struct {
	LatestVersion string    `json:"latest_version"`
	CheckedAt     time.Time `json:"checked_at"`
}

// githubRelease is the minimal GitHub API response we need.
type githubRelease struct {
	TagName string `json:"tag_name"`
}

// Check queries GitHub Releases for the latest version and returns update
// information. Results are cached at cacheDir/version-cache.json for 24 hours.
// Any error returns (nil, nil) — the check fails open.
func Check(currentVersion, cacheDir string) (*VersionInfo, error) {
	return CheckWithEndpoint(currentVersion, cacheDir, defaultEndpoint)
}

// CheckWithEndpoint is like Check but allows overriding the API endpoint
// for testing.
func CheckWithEndpoint(currentVersion, cacheDir, endpoint string) (*VersionInfo, error) {
	// Try to read from cache first.
	if cached, ok := readCache(cacheDir); ok {
		return &VersionInfo{
			LatestVersion:   cached.LatestVersion,
			UpdateAvailable: isUpdateAvailable(currentVersion, cached.LatestVersion),
			CheckedAt:       cached.CheckedAt,
		}, nil
	}

	// Cache miss or expired — fetch from API.
	latest, err := fetchLatestVersion(endpoint)
	if err != nil {
		// Fail open: swallow all errors.
		return nil, nil
	}

	now := time.Now()

	// Write cache (best-effort, ignore errors).
	writeCache(cacheDir, &cacheFile{
		LatestVersion: latest,
		CheckedAt:     now,
	})

	return &VersionInfo{
		LatestVersion:   latest,
		UpdateAvailable: isUpdateAvailable(currentVersion, latest),
		CheckedAt:       now,
	}, nil
}

// readCache reads and validates the version cache file. Returns the cache
// data and true if the cache is valid and not expired.
func readCache(cacheDir string) (*cacheFile, bool) {
	data, err := os.ReadFile(filepath.Join(cacheDir, cacheFileName))
	if err != nil {
		return nil, false
	}

	var cache cacheFile
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, false
	}

	if time.Since(cache.CheckedAt) > cacheTTL {
		return nil, false
	}

	return &cache, true
}

// writeCache writes the version cache file. Errors are silently ignored.
func writeCache(cacheDir string, cache *cacheFile) {
	_ = os.MkdirAll(cacheDir, 0o700)
	data, err := json.Marshal(cache)
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(cacheDir, cacheFileName), data, 0o644)
}

// fetchLatestVersion queries the GitHub Releases API and returns the tag_name
// of the latest release.
func fetchLatestVersion(endpoint string) (string, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(endpoint)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}

	if release.TagName == "" {
		return "", fmt.Errorf("empty tag_name in GitHub release")
	}

	return release.TagName, nil
}

// isUpdateAvailable compares two semantic versions and returns true if
// latest is newer than current. Returns false for any parse error (e.g.,
// "dev" version, malformed strings).
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

// parseSemver parses a version string like "v1.2.3" or "1.2.3" into its
// components. Returns false if the string cannot be parsed.
func parseSemver(v string) (major, minor, patch int, ok bool) {
	v = strings.TrimPrefix(v, "v")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, 0, false
	}
	minor, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, 0, false
	}
	patch, err = strconv.Atoi(parts[2])
	if err != nil {
		return 0, 0, 0, false
	}

	return major, minor, patch, true
}

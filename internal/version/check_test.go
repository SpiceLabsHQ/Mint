package version

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCheck_CacheMiss_FetchesFromAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"tag_name": "v1.2.0"})
	}))
	defer srv.Close()

	cacheDir := t.TempDir()
	info, err := CheckWithEndpoint("v1.0.0", cacheDir, srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected VersionInfo, got nil")
	}
	if info.LatestVersion != "v1.2.0" {
		t.Errorf("LatestVersion = %q, want %q", info.LatestVersion, "v1.2.0")
	}
	if !info.UpdateAvailable {
		t.Error("expected UpdateAvailable = true")
	}
	if info.CheckedAt.IsZero() {
		t.Error("expected non-zero CheckedAt")
	}
}

func TestCheck_CacheHit_SkipsAPI(t *testing.T) {
	apiCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalled = true
		json.NewEncoder(w).Encode(map[string]string{"tag_name": "v2.0.0"})
	}))
	defer srv.Close()

	cacheDir := t.TempDir()
	cache := cacheFile{
		LatestVersion: "v1.5.0",
		CheckedAt:     time.Now().Add(-1 * time.Hour), // 1 hour ago, within 24h
	}
	data, _ := json.Marshal(cache)
	os.WriteFile(filepath.Join(cacheDir, "version-cache.json"), data, 0o644)

	info, err := CheckWithEndpoint("v1.0.0", cacheDir, srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if apiCalled {
		t.Error("API should not have been called when cache is valid")
	}
	if info == nil {
		t.Fatal("expected VersionInfo, got nil")
	}
	if info.LatestVersion != "v1.5.0" {
		t.Errorf("LatestVersion = %q, want %q (from cache)", info.LatestVersion, "v1.5.0")
	}
	if !info.UpdateAvailable {
		t.Error("expected UpdateAvailable = true (v1.5.0 > v1.0.0)")
	}
}

func TestCheck_CacheExpired_FetchesFromAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"tag_name": "v3.0.0"})
	}))
	defer srv.Close()

	cacheDir := t.TempDir()
	cache := cacheFile{
		LatestVersion: "v1.0.0",
		CheckedAt:     time.Now().Add(-25 * time.Hour), // expired
	}
	data, _ := json.Marshal(cache)
	os.WriteFile(filepath.Join(cacheDir, "version-cache.json"), data, 0o644)

	info, err := CheckWithEndpoint("v1.0.0", cacheDir, srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected VersionInfo, got nil")
	}
	if info.LatestVersion != "v3.0.0" {
		t.Errorf("LatestVersion = %q, want %q (from API, cache expired)", info.LatestVersion, "v3.0.0")
	}
}

func TestCheck_APIFailure_FailsOpen(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cacheDir := t.TempDir()
	info, err := CheckWithEndpoint("v1.0.0", cacheDir, srv.URL)
	if err != nil {
		t.Fatalf("expected nil error (fail open), got: %v", err)
	}
	if info != nil {
		t.Errorf("expected nil info (fail open), got: %+v", info)
	}
}

func TestCheck_NetworkError_FailsOpen(t *testing.T) {
	cacheDir := t.TempDir()
	info, err := CheckWithEndpoint("v1.0.0", cacheDir, "http://localhost:1") // unlikely to be running
	if err != nil {
		t.Fatalf("expected nil error (fail open), got: %v", err)
	}
	if info != nil {
		t.Errorf("expected nil info (fail open), got: %+v", info)
	}
}

func TestCheck_InvalidCacheJSON_FetchesFromAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"tag_name": "v2.0.0"})
	}))
	defer srv.Close()

	cacheDir := t.TempDir()
	os.WriteFile(filepath.Join(cacheDir, "version-cache.json"), []byte("not json"), 0o644)

	info, err := CheckWithEndpoint("v1.0.0", cacheDir, srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected VersionInfo, got nil")
	}
	if info.LatestVersion != "v2.0.0" {
		t.Errorf("LatestVersion = %q, want %q", info.LatestVersion, "v2.0.0")
	}
}

func TestCheck_CacheWritten(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"tag_name": "v1.0.0"})
	}))
	defer srv.Close()

	cacheDir := t.TempDir()
	_, err := CheckWithEndpoint("v1.0.0", cacheDir, srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cachePath := filepath.Join(cacheDir, "version-cache.json")
	data, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("cache file not written: %v", err)
	}

	var cache cacheFile
	if err := json.Unmarshal(data, &cache); err != nil {
		t.Fatalf("cache file is not valid JSON: %v", err)
	}
	if cache.LatestVersion != "v1.0.0" {
		t.Errorf("cached LatestVersion = %q, want %q", cache.LatestVersion, "v1.0.0")
	}
	if cache.CheckedAt.IsZero() {
		t.Error("cached CheckedAt should be non-zero")
	}
}

func TestCompareSemver(t *testing.T) {
	tests := []struct {
		name    string
		current string
		latest  string
		want    bool // true = update available
	}{
		{"same version", "v1.0.0", "v1.0.0", false},
		{"patch update", "v1.0.0", "v1.0.1", true},
		{"minor update", "v1.0.0", "v1.1.0", true},
		{"major update", "v1.0.0", "v2.0.0", true},
		{"current newer", "v2.0.0", "v1.0.0", false},
		{"no v prefix current", "1.0.0", "v1.1.0", true},
		{"no v prefix latest", "v1.0.0", "1.1.0", true},
		{"no v prefix either", "1.0.0", "1.1.0", true},
		{"dev version", "dev", "v1.0.0", false},
		{"invalid current", "invalid", "v1.0.0", false},
		{"invalid latest", "v1.0.0", "invalid", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isUpdateAvailable(tt.current, tt.latest)
			if got != tt.want {
				t.Errorf("isUpdateAvailable(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
			}
		})
	}
}

func TestCheck_SameVersion_NoUpdate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"tag_name": "v1.0.0"})
	}))
	defer srv.Close()

	cacheDir := t.TempDir()
	info, err := CheckWithEndpoint("v1.0.0", cacheDir, srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected VersionInfo, got nil")
	}
	if info.UpdateAvailable {
		t.Error("expected UpdateAvailable = false for same version")
	}
}

func TestCheck_DevVersion_SkipsComparison(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"tag_name": "v1.0.0"})
	}))
	defer srv.Close()

	cacheDir := t.TempDir()
	info, err := CheckWithEndpoint("dev", cacheDir, srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected VersionInfo, got nil")
	}
	if info.UpdateAvailable {
		t.Error("expected UpdateAvailable = false for dev version")
	}
}

func TestCheck_PublicEndpoint(t *testing.T) {
	// Test the public Check function uses the correct default endpoint.
	// We can't easily mock the real API, so just verify it fails open
	// when the cache dir doesn't exist.
	info, err := Check("dev", "/nonexistent/path/that/should/fail")
	if err != nil {
		t.Fatalf("expected nil error (fail open), got: %v", err)
	}
	// info may be nil (network fail) or non-nil (if GitHub is reachable)
	// Either way, no error should be returned.
	_ = info
}

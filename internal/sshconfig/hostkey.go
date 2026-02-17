package sshconfig

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// HostKeyStore manages SSH host key fingerprints for mint VMs using
// trust-on-first-use (TOFU) semantics per ADR-0019. Keys are stored
// in a simple key=value file at <configDir>/known_hosts, keyed by VM name.
type HostKeyStore struct {
	dir string
}

// NewHostKeyStore creates a HostKeyStore that reads and writes keys
// in the given directory.
func NewHostKeyStore(configDir string) *HostKeyStore {
	return &HostKeyStore{dir: configDir}
}

// path returns the filesystem path to the known_hosts file.
func (s *HostKeyStore) path() string {
	return filepath.Join(s.dir, "known_hosts")
}

// RecordKey saves or updates the fingerprint for the given VM name.
func (s *HostKeyStore) RecordKey(vmName, fingerprint string) error {
	entries, err := s.readAll()
	if err != nil {
		return err
	}

	entries[vmName] = fingerprint
	return s.writeAll(entries)
}

// CheckKey compares the given fingerprint against the stored one for vmName.
// Returns (true, fingerprint, nil) on match, (false, existingFingerprint, nil)
// on mismatch, or (false, "", nil) if no key is stored.
func (s *HostKeyStore) CheckKey(vmName, fingerprint string) (matched bool, existingFingerprint string, err error) {
	entries, err := s.readAll()
	if err != nil {
		return false, "", err
	}

	existing, ok := entries[vmName]
	if !ok {
		return false, "", nil
	}

	return existing == fingerprint, existing, nil
}

// RemoveKey deletes the stored fingerprint for the given VM name.
// Does not error if the VM has no stored key.
func (s *HostKeyStore) RemoveKey(vmName string) error {
	entries, err := s.readAll()
	if err != nil {
		return err
	}

	if _, ok := entries[vmName]; !ok {
		return nil
	}

	delete(entries, vmName)
	return s.writeAll(entries)
}

// readAll parses the known_hosts file into a map of vmName -> fingerprint.
func (s *HostKeyStore) readAll() (map[string]string, error) {
	entries := make(map[string]string)

	f, err := os.Open(s.path())
	if err != nil {
		if os.IsNotExist(err) {
			return entries, nil
		}
		return nil, fmt.Errorf("open known_hosts: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			entries[parts[0]] = parts[1]
		}
	}

	return entries, scanner.Err()
}

// writeAll persists the entries map to the known_hosts file with 0600 permissions.
func (s *HostKeyStore) writeAll(entries map[string]string) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	var b strings.Builder
	for vm, fp := range entries {
		fmt.Fprintf(&b, "%s=%s\n", vm, fp)
	}

	return os.WriteFile(s.path(), []byte(b.String()), 0o600)
}

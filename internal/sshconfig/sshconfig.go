// Package sshconfig manages SSH config file blocks and host keys for mint VMs.
// It handles managed block generation with checksum verification (ADR-0008),
// permission prompting (ADR-0015), and host key TOFU (ADR-0019).
package sshconfig

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// beginMarker returns the begin marker for a VM's managed block.
func beginMarker(vmName string) string {
	return fmt.Sprintf("# mint:begin %s", vmName)
}

// endMarker returns the end marker for a VM's managed block.
func endMarker(vmName string) string {
	return fmt.Sprintf("# mint:end %s", vmName)
}

// checksumPrefix is the prefix for the checksum line.
const checksumPrefix = "# mint:checksum:"

// computeChecksum returns the SHA256 hex digest of the content between
// begin and end markers (the inner block content).
func computeChecksum(innerContent string) string {
	h := sha256.Sum256([]byte(innerContent))
	return fmt.Sprintf("%x", h)
}

// GenerateBlock creates an SSH config Host block with mint managed markers
// and a SHA256 checksum for hand-edit detection (ADR-0008).
func GenerateBlock(vmName, hostname, user string, port int) string {
	inner := fmt.Sprintf("Host mint-%s\n"+
		"    HostName %s\n"+
		"    User %s\n"+
		"    Port %d\n"+
		"    StrictHostKeyChecking no\n"+
		"    UserKnownHostsFile /dev/null\n",
		vmName, hostname, user, port)

	begin := beginMarker(vmName)
	end := endMarker(vmName)
	checksum := computeChecksum(inner)

	return fmt.Sprintf("%s\n%s%s\n%s%s\n", begin, inner, end, checksumPrefix, checksum)
}

// ReadManagedBlock extracts the managed block for the given VM from the SSH
// config content. Returns the full block (including markers and checksum) and
// true if found, or empty string and false if not present.
func ReadManagedBlock(configContent, vmName string) (string, bool) {
	begin := beginMarker(vmName)
	end := endMarker(vmName)

	beginIdx := strings.Index(configContent, begin)
	if beginIdx == -1 {
		return "", false
	}

	// Scan line-by-line from the begin marker to capture the full block
	// including the checksum line that follows the end marker.
	lines := strings.SplitAfter(configContent[beginIdx:], "\n")
	var block strings.Builder
	foundEnd := false
	for _, line := range lines {
		block.WriteString(line)
		trimmed := strings.TrimRight(line, "\n")
		if strings.HasPrefix(trimmed, end) {
			foundEnd = true
			continue
		}
		if foundEnd {
			// Include the checksum line, then stop.
			break
		}
	}

	if !foundEnd {
		return "", false
	}

	return block.String(), true
}

// HasHandEdits checks whether the managed block for the given VM has been
// hand-edited by comparing the stored checksum against a fresh computation
// of the inner content. Returns false if no block is found.
func HasHandEdits(configContent, vmName string) bool {
	block, ok := ReadManagedBlock(configContent, vmName)
	if !ok {
		return false
	}

	begin := beginMarker(vmName)
	end := endMarker(vmName)

	// Extract inner content between begin and end markers.
	beginIdx := strings.Index(block, begin)
	endIdx := strings.Index(block, end)
	if beginIdx == -1 || endIdx == -1 {
		return false
	}

	inner := block[beginIdx+len(begin)+1 : endIdx]

	// Extract stored checksum.
	checksumIdx := strings.Index(block, checksumPrefix)
	if checksumIdx == -1 {
		return true // No checksum means we can't verify.
	}
	storedChecksum := strings.TrimSpace(block[checksumIdx+len(checksumPrefix):])

	return computeChecksum(inner) != storedChecksum
}

// WriteManagedBlock writes or replaces the managed block for the given VM
// in the SSH config file. Creates the file and parent directories if they
// don't exist. Sets file permissions to 0600.
func WriteManagedBlock(configPath, vmName, block string) error {
	// Ensure parent directory exists.
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create ssh config dir: %w", err)
	}

	// Read existing content.
	data, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read ssh config: %w", err)
	}
	content := string(data)

	// Remove existing block for this VM if present.
	content = removeManagedBlockFromContent(content, vmName)

	// Append the new block.
	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	if len(content) > 0 && !strings.HasSuffix(content, "\n\n") {
		content += "\n"
	}
	content += block

	return os.WriteFile(configPath, []byte(content), 0o600)
}

// RemoveManagedBlock removes the managed block for the given VM from the
// SSH config file. Does not error if the file or block doesn't exist.
func RemoveManagedBlock(configPath, vmName string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read ssh config: %w", err)
	}

	content := removeManagedBlockFromContent(string(data), vmName)
	return os.WriteFile(configPath, []byte(content), 0o600)
}

// removeManagedBlockFromContent removes the managed block for vmName from
// the content string, including the checksum line.
func removeManagedBlockFromContent(content, vmName string) string {
	begin := beginMarker(vmName)
	end := endMarker(vmName)

	beginIdx := strings.Index(content, begin)
	if beginIdx == -1 {
		return content
	}

	rest := content[beginIdx:]
	endIdx := strings.Index(rest, end)
	if endIdx == -1 {
		return content
	}

	// Find end of end-marker line.
	afterEnd := rest[endIdx+len(end):]
	cutEnd := endIdx + len(end)

	// Skip newline after end marker.
	if len(afterEnd) > 0 && afterEnd[0] == '\n' {
		afterEnd = afterEnd[1:]
		cutEnd++
	}

	// Skip checksum line if present.
	if strings.HasPrefix(afterEnd, checksumPrefix) {
		nlIdx := strings.Index(afterEnd, "\n")
		if nlIdx == -1 {
			cutEnd += len(afterEnd)
		} else {
			cutEnd += nlIdx + 1
		}
	}

	result := content[:beginIdx] + rest[cutEnd:]

	// Clean up extra blank lines.
	for strings.Contains(result, "\n\n\n") {
		result = strings.ReplaceAll(result, "\n\n\n", "\n\n")
	}

	return result
}

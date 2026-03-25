package license

import (
	"crypto/sha256"
	"fmt"
	"os"
	"runtime"
	"strings"
)

// MachineID generates a stable hardware fingerprint for this machine.
// Used to bind a license activation to a specific server.
//
// The fingerprint combines hostname + OS + arch to create a reasonably
// unique identifier. It's not tamper-proof (that would require TPM or
// platform-specific APIs), but sufficient for honest-user licensing.
func MachineID() string {
	parts := []string{
		runtime.GOOS,
		runtime.GOARCH,
	}

	// Hostname adds per-machine uniqueness
	if h, err := os.Hostname(); err == nil {
		parts = append(parts, h)
	}

	// On Linux, read machine-id for a stable system-level identifier.
	// This persists across reboots (unlike hostname which can change).
	if runtime.GOOS == "linux" {
		if data, err := os.ReadFile("/etc/machine-id"); err == nil {
			parts = append(parts, strings.TrimSpace(string(data)))
		}
	}

	// On macOS, use the IOPlatformSerialNumber via system_profiler
	// (would require exec, skip for now — hostname + OS is good enough)

	raw := strings.Join(parts, "|")
	hash := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%x", hash[:16]) // 32-char hex string
}

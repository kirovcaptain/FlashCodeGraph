package config

import (
	"os"
	"runtime"
	"strings"
)

// DetectDefaultDatabase returns the best storage backend for the current environment.
// WSL and environments where KùzuDB disk mode is unreliable default to FalkorDB.
// Native Linux/macOS/Windows default to KùzuDB.
func DetectDefaultDatabase() string {
	if IsWSL() {
		return "falkordb"
	}
	return "kuzu"
}

// IsWSL detects if running inside Windows Subsystem for Linux.
func IsWSL() bool {
	if runtime.GOOS != "linux" {
		return false
	}

	// Method 1: Check /proc/version for Microsoft/WSL
	data, err := os.ReadFile("/proc/version")
	if err == nil {
		version := strings.ToLower(string(data))
		if strings.Contains(version, "microsoft") || strings.Contains(version, "wsl") {
			return true
		}
	}

	// Method 2: Check WSL_DISTRO_NAME environment variable
	if os.Getenv("WSL_DISTRO_NAME") != "" {
		return true
	}

	return false
}

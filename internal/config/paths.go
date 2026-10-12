package config

import (
	"os"
	"path/filepath"
	"runtime"
)

// Returns the default installation directory for the operating system.
func DefaultPackagesDir() string {
	switch runtime.GOOS {
	case "windows":
		if base := os.Getenv("LOCALAPPDATA"); base != "" {
			return filepath.Join(base, "OpenVPI", "DiffScope_packages")
		}
		if base, err := os.UserConfigDir(); err == nil && base != "" {
			return filepath.Join(base, "OpenVPI", "DiffScope_packages")
		}
	case "darwin":
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, "Library", "Application Support", "OpenVPI", "DiffScope_packages")
		}
	default:
		if base := os.Getenv("XDG_DATA_HOME"); base != "" {
			return filepath.Join(base, "OpenVPI", "DiffScope_packages")
		}
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, ".local", "share", "OpenVPI", "DiffScope_packages")
		}
	}

	return ""
}

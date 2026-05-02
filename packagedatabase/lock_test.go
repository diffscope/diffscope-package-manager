package packagedatabase

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLockPackageDirExcludesConcurrentLockAndReleases(t *testing.T) {
	packageDir := filepath.Join(t.TempDir(), "hash-package")
	if err := os.MkdirAll(packageDir, 0o755); err != nil {
		t.Fatalf("create package directory: %v", err)
	}

	lock, err := LockPackageDir(packageDir)
	if err != nil {
		t.Fatalf("LockPackageDir() error = %v", err)
	}

	if second, err := LockPackageDir(packageDir); err == nil {
		_ = second.Unlock()
		t.Fatalf("second LockPackageDir() error = nil")
	}

	if err := lock.Unlock(); err != nil {
		t.Fatalf("Unlock() error = %v", err)
	}

	lock, err = LockPackageDir(packageDir)
	if err != nil {
		t.Fatalf("LockPackageDir() after unlock error = %v", err)
	}
	if err := lock.Unlock(); err != nil {
		t.Fatalf("final Unlock() error = %v", err)
	}
}

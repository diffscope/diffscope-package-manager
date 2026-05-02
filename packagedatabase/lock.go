package packagedatabase

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gofrs/flock"
)

// PackageDirLock is an exclusive lock for operations that mutate a package directory.
type PackageDirLock struct {
	packageDir string
	lockPath   string
	lock       *flock.Flock
}

// LockPackageDir takes an exclusive, non-blocking lock for packageDir.
//
// The lock file is stored outside packageDir so callers can delete packageDir while
// holding the lock. The OS lock is released automatically if the process exits.
func LockPackageDir(packageDir string) (*PackageDirLock, error) {
	if packageDir == "" {
		return nil, fmt.Errorf("packagedatabase: package directory path is required")
	}

	absolute, err := filepath.Abs(packageDir)
	if err != nil {
		return nil, fmt.Errorf("packagedatabase: resolve package directory path: %w", err)
	}
	absolute = filepath.Clean(absolute)
	parent := filepath.Dir(absolute)
	lockDir := filepath.Join(parent, ".locks")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		return nil, fmt.Errorf("packagedatabase: create lock directory: %w", err)
	}

	sum := sha256.Sum256([]byte(lockKey(absolute)))
	lockPath := filepath.Join(lockDir, hex.EncodeToString(sum[:])+".lock")
	fileLock := flock.New(lockPath)
	locked, err := fileLock.TryLock()
	if err != nil {
		return nil, fmt.Errorf("packagedatabase: lock package directory %s: %w", absolute, err)
	}
	if !locked {
		return nil, fmt.Errorf("packagedatabase: package directory is locked: %s", absolute)
	}

	return &PackageDirLock{packageDir: absolute, lockPath: lockPath, lock: fileLock}, nil
}

// Unlock releases the package directory lock.
func (l *PackageDirLock) Unlock() error {
	if l == nil || l.lock == nil {
		return nil
	}
	if err := l.lock.Unlock(); err != nil {
		return fmt.Errorf("packagedatabase: unlock package directory %s: %w", l.packageDir, err)
	}
	_ = os.Remove(l.lockPath)
	l.lock = nil
	return nil
}

func lockKey(path string) string {
	return strings.ToLower(filepath.Clean(path))
}

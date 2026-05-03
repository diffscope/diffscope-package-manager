package commands

import (
	"net/url"
	"path/filepath"
)

func installedPackageDir(packagesDir string, packageID string, version string) string {
	return filepath.Join(packagesDir, installedPackageDirName(packageID, version))
}

func installedPackageDirName(packageID string, version string) string {
	return url.PathEscape(packageID) + "@" + version
}

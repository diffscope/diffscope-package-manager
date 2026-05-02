package commands

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"diffscope-package-manager/packagedatabase"
	"diffscope-package-manager/packagedatabase/model"
)

func TestRemovePackagesRemovesPackageDatabaseRowsAndDirectory(t *testing.T) {
	packagesDir := makeRemoveTestDatabase(t)

	var output bytes.Buffer
	if err := RemovePackages([]string{"vendor/leaf@1.0"}, packagesDir, false, false, false, false, &output); err != nil {
		t.Fatalf("RemovePackages() error = %v\n%s", err, output.String())
	}

	assertRemovePackageMissing(t, packagesDir, "vendor/leaf", "1.0.0.0")
	if _, err := os.Stat(filepath.Join(packagesDir, "hash-leaf")); !os.IsNotExist(err) {
		t.Fatalf("leaf package directory still exists or stat failed: %v", err)
	}
	if !strings.Contains(output.String(), "vendor/leaf@1.0.0.0 [removed]") {
		t.Fatalf("output missing removed result:\n%s", output.String())
	}
}

func TestRemovePackagesRejectsDependentPackagesWithoutCascade(t *testing.T) {
	packagesDir := makeRemoveTestDatabase(t)

	var output bytes.Buffer
	err := RemovePackages([]string{"vendor/base@1.0"}, packagesDir, false, false, false, false, &output)
	if err == nil {
		t.Fatalf("RemovePackages() error = nil")
	}
	if !strings.Contains(err.Error(), "use --cascade") {
		t.Fatalf("RemovePackages() error = %v", err)
	}
	assertRemovePackageExists(t, packagesDir, "vendor/base", "1.0.0.0")
	assertRemovePackageExists(t, packagesDir, "vendor/mid", "1.0.0.0")
	if !strings.Contains(output.String(), "Packages to remove by cascade") ||
		!strings.Contains(output.String(), "vendor/mid@1.0.0.0") {
		t.Fatalf("output missing cascade plan:\n%s", output.String())
	}
}

func TestRemovePackagesCascadeRemovesRecursiveDependents(t *testing.T) {
	packagesDir := makeRemoveTestDatabase(t)

	var output bytes.Buffer
	if err := RemovePackages([]string{"vendor/base@1.0"}, packagesDir, true, false, false, false, &output); err != nil {
		t.Fatalf("RemovePackages() error = %v\n%s", err, output.String())
	}

	assertRemovePackageMissing(t, packagesDir, "vendor/base", "1.0.0.0")
	assertRemovePackageMissing(t, packagesDir, "vendor/mid", "1.0.0.0")
	assertRemovePackageMissing(t, packagesDir, "vendor/top", "1.0.0.0")
	assertRemovePackageExists(t, packagesDir, "vendor/leaf", "1.0.0.0")
	for _, hash := range []string{"hash-base", "hash-mid", "hash-top"} {
		if _, err := os.Stat(filepath.Join(packagesDir, hash)); !os.IsNotExist(err) {
			t.Fatalf("package directory %s still exists or stat failed: %v", hash, err)
		}
	}
	if !strings.Contains(output.String(), "Summary: 3 removed") {
		t.Fatalf("output missing summary:\n%s", output.String())
	}
}

func TestRemovePackagesDryRunAndIgnoreNonExistentDoNotMutate(t *testing.T) {
	packagesDir := makeRemoveTestDatabase(t)

	var output bytes.Buffer
	if err := RemovePackages([]string{"vendor/leaf@1.0", "vendor/missing@1.0"}, packagesDir, false, true, true, false, &output); err != nil {
		t.Fatalf("RemovePackages() error = %v\n%s", err, output.String())
	}

	assertRemovePackageExists(t, packagesDir, "vendor/leaf", "1.0.0.0")
	if _, err := os.Stat(filepath.Join(packagesDir, "hash-leaf")); err != nil {
		t.Fatalf("leaf package directory missing after dry run: %v", err)
	}
	if !strings.Contains(output.String(), "[DRY RUN] vendor/leaf@1.0.0.0") ||
		!strings.Contains(output.String(), "Ignored non-existent packages:\n  vendor/missing@1.0.0.0") {
		t.Fatalf("dry run output missing planned or ignored package:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "Summary (dry run): 1 removed, 1 ignored") {
		t.Fatalf("dry run output missing summary:\n%s", output.String())
	}
}

func TestRemovePackagesJSONReportsAmbiguousVersion(t *testing.T) {
	packagesDir := makeRemoveTestDatabase(t)

	var output bytes.Buffer
	err := RemovePackages([]string{"vendor/multi"}, packagesDir, false, false, false, true, &output)
	if err == nil {
		t.Fatalf("RemovePackages() error = nil")
	}

	events := decodeRemoveEvents(t, output.Bytes())
	if len(events) != 1 || events[0].Event != "ERROR" || events[0].Error == nil || events[0].Error.Code != "AMBIGUOUS_VERSION" {
		t.Fatalf("events = %#v", events)
	}
}

func makeRemoveTestDatabase(t *testing.T) string {
	t.Helper()

	packagesDir := t.TempDir()
	db, err := packagedatabase.Open(filepath.Join(packagesDir, "packages.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("DB() error = %v", err)
	}
	defer sqlDB.Close()

	packages := []model.Package{
		{ID: "vendor/base", Version: "1.0.0.0", Hash: "hash-base", InstalledAt: 1},
		{ID: "vendor/mid", Version: "1.0.0.0", Hash: "hash-mid", InstalledAt: 2},
		{ID: "vendor/top", Version: "1.0.0.0", Hash: "hash-top", InstalledAt: 3},
		{ID: "vendor/leaf", Version: "1.0.0.0", Hash: "hash-leaf", InstalledAt: 4},
		{ID: "vendor/multi", Version: "1.0.0.0", Hash: "hash-multi-1", InstalledAt: 5},
		{ID: "vendor/multi", Version: "2.0.0.0", Hash: "hash-multi-2", InstalledAt: 6},
	}
	if err := db.Create(&packages).Error; err != nil {
		t.Fatalf("create packages: %v", err)
	}
	dependencies := []model.Dependency{
		{
			PackageID:               "vendor/mid",
			PackageVersion:          "1.0.0.0",
			DependentPackageID:      "vendor/base",
			DependentPackageVersion: "1.0.0.0",
		},
		{
			PackageID:               "vendor/top",
			PackageVersion:          "1.0.0.0",
			DependentPackageID:      "vendor/mid",
			DependentPackageVersion: "1.0.0.0",
		},
	}
	if err := db.Create(&dependencies).Error; err != nil {
		t.Fatalf("create dependencies: %v", err)
	}
	for _, pkg := range packages {
		dir := filepath.Join(packagesDir, pkg.Hash)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create package dir %s: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "desc.json"), []byte("{}"), 0o644); err != nil {
			t.Fatalf("write package marker: %v", err)
		}
	}
	return packagesDir
}

func assertRemovePackageExists(t *testing.T, packagesDir string, id string, version string) {
	t.Helper()

	db, err := packagedatabase.Open(filepath.Join(packagesDir, "packages.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("DB() error = %v", err)
	}
	defer sqlDB.Close()

	var count int64
	if err := db.Model(&model.Package{}).Where("id = ? AND version = ?", id, version).Count(&count).Error; err != nil {
		t.Fatalf("count package: %v", err)
	}
	if count != 1 {
		t.Fatalf("package %s@%s count = %d, want 1", id, version, count)
	}
}

func assertRemovePackageMissing(t *testing.T, packagesDir string, id string, version string) {
	t.Helper()

	db, err := packagedatabase.Open(filepath.Join(packagesDir, "packages.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("DB() error = %v", err)
	}
	defer sqlDB.Close()

	var count int64
	if err := db.Model(&model.Package{}).Where("id = ? AND version = ?", id, version).Count(&count).Error; err != nil {
		t.Fatalf("count package: %v", err)
	}
	if count != 0 {
		t.Fatalf("package %s@%s count = %d, want 0", id, version, count)
	}
}

func decodeRemoveEvents(t *testing.T, data []byte) []removeEvent {
	t.Helper()

	var events []removeEvent
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		var event removeEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("unmarshal remove event: %v\n%s", err, string(data))
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan events: %v", err)
	}
	return events
}

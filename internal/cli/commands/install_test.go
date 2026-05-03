package commands

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"diffscope-package-manager/packagedatabase"
	"diffscope-package-manager/packagedatabase/model"
)

func TestInstallPackagesInstallsArchiveAndWritesDatabase(t *testing.T) {
	packagesDir := t.TempDir()
	packageFile := filepath.Join(t.TempDir(), "simple.dspk")
	if err := os.WriteFile(packageFile, makeInstallTestArchive(t, "vendor/simple", "1.0", nil), 0o644); err != nil {
		t.Fatalf("write package file: %v", err)
	}

	var output bytes.Buffer
	if err := InstallPackages([]string{packageFile}, packagesDir, false, false, false, &output); err != nil {
		t.Fatalf("InstallPackages() error = %v\n%s", err, output.String())
	}

	db, err := packagedatabase.Open(filepath.Join(packagesDir, "packages.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("DB() error = %v", err)
	}
	defer sqlDB.Close()

	var pkg model.Package
	if err := db.Where("id = ? AND version = ?", "vendor/simple", "1.0.0.0").First(&pkg).Error; err != nil {
		t.Fatalf("load installed package: %v", err)
	}
	if pkg.Hash == "" {
		t.Fatalf("installed package hash is empty")
	}
	if _, err := os.Stat(filepath.Join(installedPackageDir(packagesDir, pkg.ID, pkg.Version), "desc.json")); err != nil {
		t.Fatalf("extracted desc.json: %v", err)
	}
	if !strings.Contains(output.String(), "installed") {
		t.Fatalf("output missing installed result:\n%s", output.String())
	}
}

func TestInstallPackagesDryRunDoesNotExtractOrWriteDatabase(t *testing.T) {
	packagesDir := t.TempDir()
	packageFile := filepath.Join(t.TempDir(), "simple.dspk")
	if err := os.WriteFile(packageFile, makeInstallTestArchive(t, "vendor/simple", "1.0", nil), 0o644); err != nil {
		t.Fatalf("write package file: %v", err)
	}

	var output bytes.Buffer
	if err := InstallPackages([]string{packageFile}, packagesDir, false, true, false, &output); err != nil {
		t.Fatalf("InstallPackages() error = %v\n%s", err, output.String())
	}

	entries, err := os.ReadDir(packagesDir)
	if err != nil {
		t.Fatalf("read packages dir: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			t.Fatalf("dry run created directory %s", entry.Name())
		}
	}
	if !strings.Contains(output.String(), "[install]") {
		t.Fatalf("dry run output missing action:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "Summary (dry run): 1 installed, 0 overwritten, 0 skipped") {
		t.Fatalf("dry run output missing summary:\n%s", output.String())
	}
}

func TestInstallPackagesRejectsDuplicatePackageIdentityInSameCommand(t *testing.T) {
	packagesDir := t.TempDir()
	tempDir := t.TempDir()
	first := filepath.Join(tempDir, "first.dspk")
	second := filepath.Join(tempDir, "second.dspk")
	if err := os.WriteFile(first, makeInstallTestArchive(t, "vendor/simple", "1.0", map[string]string{"a.txt": "a"}), 0o644); err != nil {
		t.Fatalf("write first package file: %v", err)
	}
	if err := os.WriteFile(second, makeInstallTestArchive(t, "vendor/simple", "1.0", map[string]string{"b.txt": "b"}), 0o644); err != nil {
		t.Fatalf("write second package file: %v", err)
	}

	var output bytes.Buffer
	err := InstallPackages([]string{first, second}, packagesDir, true, false, false, &output)
	if err == nil {
		t.Fatalf("InstallPackages() error = nil")
	}
	if !strings.Contains(err.Error(), "appears more than once") {
		t.Fatalf("InstallPackages() error = %v", err)
	}
}

func makeInstallTestArchive(t *testing.T, id string, version string, extraFiles map[string]string) []byte {
	t.Helper()

	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	files := map[string]string{
		"desc.json": `{
			"contributes": {
				"inferences": [],
				"singers": []
			},
			"dependencies": [],
			"id": "` + id + `",
			"name": "Simple",
			"version": "` + version + `"
		}`,
	}
	for name, body := range extraFiles {
		files[name] = body
	}
	for name, body := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create zip entry %q: %v", name, err)
		}
		if _, err := entry.Write([]byte(body)); err != nil {
			t.Fatalf("write zip entry %q: %v", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buffer.Bytes()
}

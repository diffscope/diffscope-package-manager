package commands

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"diffscope-package-manager/packagedatabase"
	"diffscope-package-manager/packagedatabase/model"
)

func TestInspectPackageFileJSONReportsInstalledStatus(t *testing.T) {
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
	if err := db.Create(&model.Package{
		ID:          "vendor/dep",
		Version:     "1.0.0.0",
		Hash:        "hash",
		InstalledAt: 1,
	}).Error; err != nil {
		t.Fatalf("create package: %v", err)
	}

	packageFile := filepath.Join(t.TempDir(), "sample.dspk")
	if err := os.WriteFile(packageFile, makeInspectTestArchive(t), 0o644); err != nil {
		t.Fatalf("write package file: %v", err)
	}

	var output bytes.Buffer
	if err := InspectPackageFile(packageFile, packagesDir, "en", true, false, &output); err != nil {
		t.Fatalf("InspectPackageFile() error = %v", err)
	}

	var payload inspectOutput
	if err := json.Unmarshal(output.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, output.String())
	}

	if !payload.OK {
		t.Fatalf("payload.OK = false: %#v", payload.Error)
	}
	if payload.Data.File.Path == "" {
		t.Fatalf("file path is empty")
	}
	if len(payload.Data.Dependencies) != 1 ||
		payload.Data.Dependencies[0].Reference != "vendor/dep@1.0.0.0" ||
		payload.Data.Dependencies[0].Status != "installed" ||
		!payload.Data.Dependencies[0].Installed {
		t.Fatalf("dependencies = %#v", payload.Data.Dependencies)
	}
	if payload.Data.Package.Name != "Package" {
		t.Fatalf("package name = %#v, want selected string", payload.Data.Package.Name)
	}
	if payload.Data.File.Hash != "" {
		t.Fatalf("hash = %q, want empty without --hash", payload.Data.File.Hash)
	}
	if len(payload.Data.Singers) != 1 || len(payload.Data.Singers[0].Imports) != 2 {
		t.Fatalf("singers = %#v", payload.Data.Singers)
	}
	gotCurrentImport := payload.Data.Singers[0].Imports[0]
	if gotCurrentImport.Reference != "vendor/package@1.0.0.0:acoustic" ||
		gotCurrentImport.Status != "ready" ||
		!gotCurrentImport.PackageInstalled ||
		!gotCurrentImport.InferenceInstalled {
		t.Fatalf("current import status = %#v", gotCurrentImport)
	}
	gotDependencyImport := payload.Data.Singers[0].Imports[1]
	if gotDependencyImport.Status != "missingInference" ||
		!gotDependencyImport.PackageInstalled ||
		gotDependencyImport.InferenceInstalled {
		t.Fatalf("dependency import status = %#v", gotDependencyImport)
	}
}

func TestInspectPackageFileJSONReportsAllLanguagesForWildcardLanguage(t *testing.T) {
	packagesDir := t.TempDir()
	packageFile := filepath.Join(t.TempDir(), "sample.dspk")
	if err := os.WriteFile(packageFile, makeInspectTestArchive(t), 0o644); err != nil {
		t.Fatalf("write package file: %v", err)
	}

	var output bytes.Buffer
	if err := InspectPackageFile(packageFile, packagesDir, "*", true, false, &output); err != nil {
		t.Fatalf("InspectPackageFile() error = %v", err)
	}

	var payload inspectOutput
	if err := json.Unmarshal(output.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, output.String())
	}

	name, ok := payload.Data.Package.Name.(map[string]any)
	if !ok {
		t.Fatalf("package name = %#v, want language map", payload.Data.Package.Name)
	}
	if name["_"] != "Package" || name["zh-CN"] != "包" {
		t.Fatalf("package name map = %#v", name)
	}
}

func TestInspectPackageFileJSONIncludesHashWhenRequested(t *testing.T) {
	packagesDir := t.TempDir()
	packageFile := filepath.Join(t.TempDir(), "sample.dspk")
	if err := os.WriteFile(packageFile, makeInspectTestArchive(t), 0o644); err != nil {
		t.Fatalf("write package file: %v", err)
	}

	var output bytes.Buffer
	if err := InspectPackageFile(packageFile, packagesDir, "en", true, true, &output); err != nil {
		t.Fatalf("InspectPackageFile() error = %v", err)
	}

	var payload inspectOutput
	if err := json.Unmarshal(output.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, output.String())
	}
	if len(payload.Data.File.Hash) != 128 {
		t.Fatalf("hash length = %d, want 128: %q", len(payload.Data.File.Hash), payload.Data.File.Hash)
	}
}

func TestInspectPackageFileJSONReportsMissingCurrentInferenceImport(t *testing.T) {
	packagesDir := t.TempDir()
	packageFile := filepath.Join(t.TempDir(), "sample.dspk")
	if err := os.WriteFile(packageFile, makeInspectTestArchiveWithImports(t, `"missing"`), 0o644); err != nil {
		t.Fatalf("write package file: %v", err)
	}

	var output bytes.Buffer
	if err := InspectPackageFile(packageFile, packagesDir, "en", true, false, &output); err != nil {
		t.Fatalf("InspectPackageFile() error = %v", err)
	}

	var payload inspectOutput
	if err := json.Unmarshal(output.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, output.String())
	}

	if len(payload.Data.Singers) != 1 || len(payload.Data.Singers[0].Imports) != 1 {
		t.Fatalf("singers = %#v", payload.Data.Singers)
	}
	gotImport := payload.Data.Singers[0].Imports[0]
	if gotImport.Status != "missingInference" ||
		!gotImport.PackageInstalled ||
		gotImport.InferenceInstalled {
		t.Fatalf("current missing import status = %#v", gotImport)
	}
}

func makeInspectTestArchive(t *testing.T) []byte {
	return makeInspectTestArchiveWithImports(t, `"acoustic",
				{"id": "vendor/dep", "version": "1.0", "inferenceId": "missing"}`)
}

func makeInspectTestArchiveWithImports(t *testing.T, imports string) []byte {
	t.Helper()

	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	files := map[string]string{
		"desc.json": `{
			"contributes": {
				"inferences": ["acoustic.json"],
				"singers": ["singer.json"]
			},
			"dependencies": [
				{"id": "vendor/dep", "version": "1.0"}
			],
			"id": "vendor/package",
			"name": {"_": "Package", "zh-CN": "包"},
			"version": "1.0"
		}`,
		"acoustic.json": `{
			"$version": "1.0",
			"class": "DiffSingerAcoustic",
			"id": "acoustic",
			"level": 1,
			"name": "Acoustic"
		}`,
		"singer.json": `{
			"$version": "1.0",
			"class": "DiffSingerSinger",
			"id": "singer",
			"imports": [
				` + imports + `
			],
			"level": 1,
			"name": "Singer"
		}`,
	}

	for name, body := range files {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create zip entry %q: %v", name, err)
		}
		if _, err := file.Write([]byte(body)); err != nil {
			t.Fatalf("write zip entry %q: %v", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buffer.Bytes()
}

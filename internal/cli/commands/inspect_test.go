package commands

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
	if err := db.Create(&model.PackageMultilingualInfo{
		PackageID:      "vendor/dep",
		PackageVersion: "1.0.0.0",
		Language:       "_",
		Name:           stringPointer("Dependency"),
	}).Error; err != nil {
		t.Fatalf("create package name: %v", err)
	}
	if err := db.Create(&model.Package{
		ID:          "vendor/package",
		Version:     "1.0.0.0",
		Hash:        "hash",
		InstalledAt: 2,
	}).Error; err != nil {
		t.Fatalf("create current package: %v", err)
	}
	if err := db.Create(&model.Inference{
		ID:             "acoustic",
		PackageID:      "vendor/package",
		PackageVersion: "1.0.0.0",
	}).Error; err != nil {
		t.Fatalf("create inference: %v", err)
	}
	if err := db.Create(&model.InferenceMultilingualInfo{
		InferenceID:    "acoustic",
		PackageID:      "vendor/package",
		PackageVersion: "1.0.0.0",
		Language:       "_",
		Name:           stringPointer("Acoustic"),
	}).Error; err != nil {
		t.Fatalf("create inference name: %v", err)
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
		payload.Data.Dependencies[0].ID != "vendor/dep" ||
		payload.Data.Dependencies[0].Version != "1.0.0.0" ||
		payload.Data.Dependencies[0].Name != "Dependency" ||
		payload.Data.Dependencies[0].Status != "installed" ||
		!payload.Data.Dependencies[0].Installed {
		t.Fatalf("dependencies = %#v", payload.Data.Dependencies)
	}
	assertInspectJSONHasNoReferenceField(t, output.Bytes())
	if payload.Data.Package.Name != "Package" {
		t.Fatalf("package name = %#v, want selected string", payload.Data.Package.Name)
	}
	if payload.Data.File.Hash != "" {
		t.Fatalf("hash = %q, want empty without --hash", payload.Data.File.Hash)
	}
	if len(payload.Data.Inferences) != 1 ||
		payload.Data.Inferences[0].ID != "acoustic" ||
		payload.Data.Inferences[0].Path != "acoustic.json" {
		t.Fatalf("inferences = %#v", payload.Data.Inferences)
	}
	if len(payload.Data.Singers) != 1 || len(payload.Data.Singers[0].Imports) != 2 {
		t.Fatalf("singers = %#v", payload.Data.Singers)
	}
	if payload.Data.Singers[0].Path != "singer.json" {
		t.Fatalf("singer path = %q", payload.Data.Singers[0].Path)
	}
	gotCurrentImport := payload.Data.Singers[0].Imports[0]
	if gotCurrentImport.ID != "vendor/package" ||
		gotCurrentImport.Version != "1.0.0.0" ||
		gotCurrentImport.InferenceID != "acoustic" ||
		gotCurrentImport.Name != "Acoustic" ||
		gotCurrentImport.Status != "ready" ||
		!gotCurrentImport.PackageInstalled ||
		!gotCurrentImport.InferenceInstalled {
		t.Fatalf("current import status = %#v", gotCurrentImport)
	}
	gotDependencyImport := payload.Data.Singers[0].Imports[1]
	if gotDependencyImport.Status != "missingInference" ||
		gotDependencyImport.Name != nil ||
		!gotDependencyImport.PackageInstalled ||
		gotDependencyImport.InferenceInstalled {
		t.Fatalf("dependency import status = %#v", gotDependencyImport)
	}
}

func TestInspectPackageFileSuggestsInfoForPackageReference(t *testing.T) {
	var output bytes.Buffer
	err := InspectPackageFile("vendor/package@1.0.0.0", t.TempDir(), "en", false, false, &output)
	if err == nil {
		t.Fatalf("InspectPackageFile() error = nil")
	}

	got := output.String()
	if !strings.Contains(got, "Suggestion:") ||
		!strings.Contains(got, "dspm info vendor/package@1.0.0.0") {
		t.Fatalf("output = %q", got)
	}
}

func TestInspectPackageFileJSONDoesNotSuggestInfoForPackageReference(t *testing.T) {
	var output bytes.Buffer
	err := InspectPackageFile("vendor/package@1.0.0.0", t.TempDir(), "en", true, false, &output)
	if err == nil {
		t.Fatalf("InspectPackageFile() error = nil")
	}

	got := output.String()
	if strings.Contains(got, "Suggestion:") ||
		strings.Contains(got, "dspm info") {
		t.Fatalf("json output should not include suggestion: %q", got)
	}

	var payload inspectOutput
	if err := json.Unmarshal(output.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, output.String())
	}
	if payload.OK || payload.Error == nil {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestInspectPackageFileTextIncludesInstalledReferenceNames(t *testing.T) {
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
	if err := db.Create([]model.PackageMultilingualInfo{
		{
			PackageID:      "vendor/dep",
			PackageVersion: "1.0.0.0",
			Language:       "_",
			Name:           stringPointer("Dependency"),
		},
		{
			PackageID:      "vendor/dep",
			PackageVersion: "1.0.0.0",
			Language:       "en-US",
			Name:           stringPointer("Dependency"),
		},
		{
			PackageID:      "vendor/dep",
			PackageVersion: "1.0.0.0",
			Language:       "zh-CN",
			Name:           stringPointer("依赖"),
		},
	}).Error; err != nil {
		t.Fatalf("create package names: %v", err)
	}

	packageFile := filepath.Join(t.TempDir(), "sample.dspk")
	if err := os.WriteFile(packageFile, makeInspectTestArchive(t), 0o644); err != nil {
		t.Fatalf("write package file: %v", err)
	}

	var output bytes.Buffer
	if err := InspectPackageFile(packageFile, packagesDir, "*", false, false, &output); err != nil {
		t.Fatalf("InspectPackageFile() error = %v", err)
	}

	got := output.String()
	want := "[_] Dependency [en-US] Dependency [zh-CN] 依赖 (vendor/dep@1.0.0.0)"
	if !strings.Contains(got, want) {
		t.Fatalf("text output missing %q:\n%s", want, got)
	}
	if !strings.Contains(got, "vendor/dep@1.0.0.0:missing") {
		t.Fatalf("text output should keep unnamed missing import reference:\n%s", got)
	}
	if !strings.Contains(got, "acoustic -> acoustic.json") ||
		!strings.Contains(got, "singer -> singer.json") {
		t.Fatalf("text output missing contribution paths:\n%s", got)
	}
}

func TestInspectPackageFileIncludesInstalledImportNames(t *testing.T) {
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
	if err := db.Create(&model.Inference{
		ID:             "external",
		PackageID:      "vendor/dep",
		PackageVersion: "1.0.0.0",
	}).Error; err != nil {
		t.Fatalf("create inference: %v", err)
	}
	if err := db.Create([]model.InferenceMultilingualInfo{
		{
			InferenceID:    "external",
			PackageID:      "vendor/dep",
			PackageVersion: "1.0.0.0",
			Language:       "_",
			Name:           stringPointer("External"),
		},
		{
			InferenceID:    "external",
			PackageID:      "vendor/dep",
			PackageVersion: "1.0.0.0",
			Language:       "zh-CN",
			Name:           stringPointer("外部推理"),
		},
	}).Error; err != nil {
		t.Fatalf("create inference names: %v", err)
	}

	packageFile := filepath.Join(t.TempDir(), "sample.dspk")
	if err := os.WriteFile(packageFile, makeInspectTestArchiveWithImports(t, `{"id": "vendor/dep", "version": "1.0", "inferenceId": "external"}`), 0o644); err != nil {
		t.Fatalf("write package file: %v", err)
	}

	var jsonOutput bytes.Buffer
	if err := InspectPackageFile(packageFile, packagesDir, "en", true, false, &jsonOutput); err != nil {
		t.Fatalf("InspectPackageFile() json error = %v", err)
	}

	var payload inspectOutput
	if err := json.Unmarshal(jsonOutput.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, jsonOutput.String())
	}
	if len(payload.Data.Singers) != 1 || len(payload.Data.Singers[0].Imports) != 1 {
		t.Fatalf("singers = %#v", payload.Data.Singers)
	}
	gotImport := payload.Data.Singers[0].Imports[0]
	if gotImport.ID != "vendor/dep" ||
		gotImport.Version != "1.0.0.0" ||
		gotImport.InferenceID != "external" ||
		gotImport.Name != "External" ||
		gotImport.Status != "ready" {
		t.Fatalf("import = %#v", gotImport)
	}

	var textOutput bytes.Buffer
	if err := InspectPackageFile(packageFile, packagesDir, "*", false, false, &textOutput); err != nil {
		t.Fatalf("InspectPackageFile() text error = %v", err)
	}
	want := "[_] External [zh-CN] 外部推理 (vendor/dep@1.0.0.0:external)"
	if !strings.Contains(textOutput.String(), want) {
		t.Fatalf("text output missing %q:\n%s", want, textOutput.String())
	}
}

func TestInspectPackageFileIncludesCurrentPackageImportNames(t *testing.T) {
	packagesDir := t.TempDir()
	packageFile := filepath.Join(t.TempDir(), "sample.dspk")
	if err := os.WriteFile(packageFile, makeInspectTestArchive(t), 0o644); err != nil {
		t.Fatalf("write package file: %v", err)
	}

	var jsonOutput bytes.Buffer
	if err := InspectPackageFile(packageFile, packagesDir, "en", true, false, &jsonOutput); err != nil {
		t.Fatalf("InspectPackageFile() json error = %v", err)
	}

	var payload inspectOutput
	if err := json.Unmarshal(jsonOutput.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, jsonOutput.String())
	}
	if len(payload.Data.Singers) != 1 || len(payload.Data.Singers[0].Imports) == 0 {
		t.Fatalf("singers = %#v", payload.Data.Singers)
	}
	gotImport := payload.Data.Singers[0].Imports[0]
	if gotImport.ID != "vendor/package" ||
		gotImport.Version != "1.0.0.0" ||
		gotImport.InferenceID != "acoustic" ||
		gotImport.Name != "Acoustic" ||
		gotImport.Status != "ready" {
		t.Fatalf("current import = %#v", gotImport)
	}

	var textOutput bytes.Buffer
	if err := InspectPackageFile(packageFile, packagesDir, "en", false, false, &textOutput); err != nil {
		t.Fatalf("InspectPackageFile() text error = %v", err)
	}
	want := "Acoustic (vendor/package@1.0.0.0:acoustic)"
	if !strings.Contains(textOutput.String(), want) {
		t.Fatalf("text output missing %q:\n%s", want, textOutput.String())
	}
}

func TestInspectPackageFileOutputsSingerResourcePathsRelativeToPackage(t *testing.T) {
	packagesDir := t.TempDir()
	packageFile := filepath.Join(t.TempDir(), "sample.dspk")
	if err := os.WriteFile(packageFile, makeInspectTestArchiveWithNestedSinger(t), 0o644); err != nil {
		t.Fatalf("write package file: %v", err)
	}

	var jsonOutput bytes.Buffer
	if err := InspectPackageFile(packageFile, packagesDir, "en", true, false, &jsonOutput); err != nil {
		t.Fatalf("InspectPackageFile() json error = %v", err)
	}

	var payload inspectOutput
	if err := json.Unmarshal(jsonOutput.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, jsonOutput.String())
	}
	if len(payload.Data.Singers) != 1 {
		t.Fatalf("singers = %#v", payload.Data.Singers)
	}
	singer := payload.Data.Singers[0]
	if singer.Avatar != "singers/assets/avatar.png" ||
		singer.Background != "singers/assets/background.png" ||
		len(singer.DemoAudio) != 1 ||
		singer.DemoAudio[0].Audio != "singers/assets/demo.ogg" {
		t.Fatalf("singer resource paths = %#v", singer)
	}

	var textOutput bytes.Buffer
	if err := InspectPackageFile(packageFile, packagesDir, "en", false, false, &textOutput); err != nil {
		t.Fatalf("InspectPackageFile() text error = %v", err)
	}
	text := textOutput.String()
	for _, want := range []string{"singers/assets/avatar.png", "singers/assets/background.png", "singers/assets/demo.ogg"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text output missing %q:\n%s", want, text)
		}
	}
}

func assertInspectJSONHasNoReferenceField(t *testing.T, output []byte) {
	t.Helper()

	var payload map[string]any
	if err := json.Unmarshal(output, &payload); err != nil {
		t.Fatalf("unmarshal raw output: %v", err)
	}
	if containsJSONKey(payload, "reference") {
		t.Fatalf("json output contains deprecated reference field: %s", string(output))
	}
}

func containsJSONKey(value any, key string) bool {
	switch typed := value.(type) {
	case map[string]any:
		for currentKey, currentValue := range typed {
			if currentKey == key || containsJSONKey(currentValue, key) {
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if containsJSONKey(item, key) {
				return true
			}
		}
	}
	return false
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

func makeInspectTestArchiveWithNestedSinger(t *testing.T) []byte {
	t.Helper()

	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	files := map[string]string{
		"desc.json": `{
			"contributes": {
				"inferences": ["inferences/acoustic.json"],
				"singers": ["singers/main.json"]
			},
			"dependencies": [],
			"id": "vendor/package",
			"version": "1.0"
		}`,
		"inferences/acoustic.json": `{
			"$version": "1.0",
			"class": "DiffSingerAcoustic",
			"id": "acoustic",
			"level": 1
		}`,
		"singers/main.json": `{
			"$version": "1.0",
			"avatar": "assets/avatar.png",
			"background": "assets/background.png",
			"class": "DiffSingerSinger",
			"demoAudio": "assets/demo.ogg",
			"id": "singer",
			"imports": ["acoustic"],
			"level": 1
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

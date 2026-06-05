package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diffscope/diffscope-package-manager/packagearchive"
	"github.com/diffscope/diffscope-package-manager/packagedatabase"
	"github.com/diffscope/diffscope-package-manager/packagedatabase/model"
	"github.com/diffscope/diffscope-package-manager/packageinfo"
)

func TestShowInfoJSONForInferenceFiltersModulesAndIncludesInstallation(t *testing.T) {
	packagesDir := makeInfoTestDatabase(t)

	var output bytes.Buffer
	if err := ShowInfo("vendor/package@1.0:acoustic", packagesDir, "en", true, &output); err != nil {
		t.Fatalf("ShowInfo() error = %v", err)
	}

	var payload infoOutput
	if err := json.Unmarshal(output.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, output.String())
	}
	if !payload.OK || payload.Command != "info" {
		t.Fatalf("payload = %#v", payload)
	}
	if payload.Data.Type != "inference" {
		t.Fatalf("type = %q", payload.Data.Type)
	}
	packageDir := installedPackageDir(packagesDir, "vendor/package", "1.0.0.0")
	if payload.Data.Installation.Path != packageDir ||
		payload.Data.Installation.Hash != "hash-package" ||
		payload.Data.Installation.InstalledAt != "1970-01-01T00:00:02.345Z" {
		t.Fatalf("installation = %#v", payload.Data.Installation)
	}
	if payload.Data.Package.ID != "vendor/package" ||
		payload.Data.Package.Version != "1.0.0.0" ||
		payload.Data.Package.Name != "Package" {
		t.Fatalf("package = %#v", payload.Data.Package)
	}
	if payload.Data.Package.Readme != filepath.Join(packageDir, "README.md") ||
		payload.Data.Package.License != filepath.Join(packagesDir, "absolute-license.txt") {
		t.Fatalf("package paths = readme %#v license %#v", payload.Data.Package.Readme, payload.Data.Package.License)
	}
	if len(payload.Data.Dependencies) != 1 ||
		payload.Data.Dependencies[0].ID != "vendor/dep" ||
		payload.Data.Dependencies[0].Version != "1.0.0.0" ||
		payload.Data.Dependencies[0].Name != "Dependency" {
		t.Fatalf("dependencies = %#v", payload.Data.Dependencies)
	}
	if len(payload.Data.Inferences) != 1 ||
		payload.Data.Inferences[0].ID != "acoustic" ||
		payload.Data.Inferences[0].Name != "Acoustic" ||
		payload.Data.Inferences[0].Path != filepath.Join(packageDir, "acoustic.json") {
		t.Fatalf("inferences = %#v", payload.Data.Inferences)
	}
	if len(payload.Data.Singers) != 0 {
		t.Fatalf("singers should be omitted for inference target: %#v", payload.Data.Singers)
	}
}

func TestShowInfoTextForSingerFiltersModulesAndOmitsStatuses(t *testing.T) {
	packagesDir := makeInfoTestDatabase(t)

	var output bytes.Buffer
	if err := ShowInfo("vendor/package@1.0[singer]", packagesDir, "en", false, &output); err != nil {
		t.Fatalf("ShowInfo() error = %v", err)
	}

	got := output.String()
	if !strings.Contains(got, "Installation") ||
		!strings.Contains(got, "Path: "+installedPackageDir(packagesDir, "vendor/package", "1.0.0.0")) ||
		!strings.Contains(got, "Hash: hash-package") ||
		!strings.Contains(got, "InstalledAt: ") {
		t.Fatalf("output missing installation block:\n%s", got)
	}
	if strings.Index(got, "Path: ") > strings.Index(got, "Hash: ") {
		t.Fatalf("installation Path should be printed before Hash:\n%s", got)
	}
	if strings.Contains(got, "Inferences") {
		t.Fatalf("singer target should not print Inferences block:\n%s", got)
	}
	if !strings.Contains(got, "Singers") ||
		!strings.Contains(got, "singer -> "+filepath.Join(installedPackageDir(packagesDir, "vendor/package", "1.0.0.0"), "singer.json")) ||
		!strings.Contains(got, "Singer") ||
		!strings.Contains(got, "Acoustic (vendor/package@1.0.0.0:acoustic)") ||
		!strings.Contains(got, filepath.Join(installedPackageDir(packagesDir, "vendor/package", "1.0.0.0"), "avatar.png")) ||
		!strings.Contains(got, filepath.Join(installedPackageDir(packagesDir, "vendor/package", "1.0.0.0"), "background.png")) {
		t.Fatalf("output missing singer details:\n%s", got)
	}
	if strings.Contains(got, "Ready") ||
		strings.Contains(got, "✓ Installed") ||
		strings.Contains(got, "Missing") {
		t.Fatalf("info output should not include inspect status labels:\n%s", got)
	}
}

func TestShowInfoSuggestsInspectForPackageFile(t *testing.T) {
	packageFile := filepath.Join(t.TempDir(), "sample package.dspk")
	if err := os.WriteFile(packageFile, []byte("not used"), 0o644); err != nil {
		t.Fatalf("write package file: %v", err)
	}

	var output bytes.Buffer
	err := ShowInfo(packageFile, t.TempDir(), "en", false, &output)
	if err == nil {
		t.Fatalf("ShowInfo() error = nil")
	}

	got := output.String()
	if !strings.Contains(got, "Suggestion:") ||
		!strings.Contains(got, "dspm inspect "+packageFile) {
		t.Fatalf("output = %q", got)
	}
}

func TestShowInfoJSONDoesNotSuggestInspectForPackageFile(t *testing.T) {
	packageFile := filepath.Join(t.TempDir(), "sample package.dspk")
	if err := os.WriteFile(packageFile, []byte("not used"), 0o644); err != nil {
		t.Fatalf("write package file: %v", err)
	}

	var output bytes.Buffer
	err := ShowInfo(packageFile, t.TempDir(), "en", true, &output)
	if err == nil {
		t.Fatalf("ShowInfo() error = nil")
	}

	got := output.String()
	if strings.Contains(got, "Suggestion:") ||
		strings.Contains(got, "dspm inspect") {
		t.Fatalf("json output should not include suggestion: %q", got)
	}

	var payload infoOutput
	if err := json.Unmarshal(output.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, output.String())
	}
	if payload.OK || payload.Error == nil {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestShowInfoJSONReportsAmbiguousVersion(t *testing.T) {
	packagesDir := makeInfoTestDatabase(t)

	var output bytes.Buffer
	err := ShowInfo("vendor/multi", packagesDir, "en", true, &output)
	if err == nil {
		t.Fatalf("ShowInfo() error = nil")
	}

	var payload infoOutput
	if unmarshalErr := json.Unmarshal(output.Bytes(), &payload); unmarshalErr != nil {
		t.Fatalf("unmarshal output: %v\n%s", unmarshalErr, output.String())
	}
	if payload.OK || payload.Error == nil || payload.Error.Code != "AMBIGUOUS_VERSION" {
		t.Fatalf("payload = %#v", payload)
	}
	candidates, ok := payload.Error.Details["candidates"].([]any)
	if !ok || len(candidates) != 2 {
		t.Fatalf("candidates = %#v", payload.Error.Details["candidates"])
	}
}

func TestAbsolutizeInfoPathsConvertsResourcePaths(t *testing.T) {
	absoluteLicense := filepath.Join(t.TempDir(), "LICENSE.txt")
	info := infoPackage{
		Inspection: packagearchive.PackageInspection{
			Readme:  &packageinfo.MultilingualText{Default: "README.md"},
			License: &packageinfo.MultilingualText{Default: absoluteLicense},
			Contributes: packagearchive.PackageInspectionContributions{
				Singers: []packagearchive.SingerInspection{
					{
						Avatar:     &packageinfo.MultilingualText{Default: "avatar.png"},
						Background: &packageinfo.MultilingualText{Texts: map[string]string{"zh-CN": "background.png"}},
						DemoAudio: []packagearchive.SingerDemoAudioInspection{
							{
								Audio: packageinfo.MultilingualText{Default: "demo.wav"},
							},
						},
					},
				},
			},
		},
	}
	packageDir := filepath.Join(t.TempDir(), "hash-package")

	absolutizeInfoPaths(&info, packageDir)

	if info.Inspection.Readme.Default != filepath.Join(packageDir, "README.md") {
		t.Fatalf("readme = %q", info.Inspection.Readme.Default)
	}
	if info.Inspection.License.Default != absoluteLicense {
		t.Fatalf("absolute license = %q", info.Inspection.License.Default)
	}
	singer := info.Inspection.Contributes.Singers[0]
	if singer.Avatar.Default != filepath.Join(packageDir, "avatar.png") ||
		singer.Background.Texts["zh-CN"] != filepath.Join(packageDir, "background.png") ||
		singer.DemoAudio[0].Audio.Default != filepath.Join(packageDir, "demo.wav") {
		t.Fatalf("singer paths = %#v", singer)
	}
}

func makeInfoTestDatabase(t *testing.T) string {
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

	if err := db.Create([]model.Package{
		{ID: "vendor/dep", Version: "1.0.0.0", Hash: "hash-dep", InstalledAt: 1234},
		{ID: "vendor/package", Version: "1.0.0.0", Hash: "hash-package", InstalledAt: 2345},
		{ID: "vendor/multi", Version: "1.0.0.0", Hash: "hash-multi-1", InstalledAt: 1},
		{ID: "vendor/multi", Version: "2.0.0.0", Hash: "hash-multi-2", InstalledAt: 2},
	}).Error; err != nil {
		t.Fatalf("create packages: %v", err)
	}
	if err := db.Create([]model.PackageMultilingualInfo{
		{
			PackageID:      "vendor/dep",
			PackageVersion: "1.0.0.0",
			Language:       "_",
			Name:           stringPointer("Dependency"),
		},
		{
			PackageID:      "vendor/package",
			PackageVersion: "1.0.0.0",
			Language:       "_",
			Name:           stringPointer("Package"),
			Readme:         stringPointer("README.md"),
			License:        stringPointer(filepath.Join(packagesDir, "absolute-license.txt")),
		},
	}).Error; err != nil {
		t.Fatalf("create package infos: %v", err)
	}
	if err := db.Create(&model.Dependency{
		PackageID:               "vendor/package",
		PackageVersion:          "1.0.0.0",
		DependentPackageID:      "vendor/dep",
		DependentPackageVersion: "1.0.0.0",
	}).Error; err != nil {
		t.Fatalf("create dependency: %v", err)
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
		t.Fatalf("create inference info: %v", err)
	}
	if err := db.Create(&model.Singer{
		ID:             "singer",
		PackageID:      "vendor/package",
		PackageVersion: "1.0.0.0",
		Class:          "DiffSingerSinger",
	}).Error; err != nil {
		t.Fatalf("create singer: %v", err)
	}
	if err := db.Create(&model.SingerMultilingualInfo{
		SingerID:       "singer",
		PackageID:      "vendor/package",
		PackageVersion: "1.0.0.0",
		Language:       "_",
		Name:           stringPointer("Singer"),
		Avatar:         stringPointer("avatar.png"),
		Background:     stringPointer("background.png"),
	}).Error; err != nil {
		t.Fatalf("create singer info: %v", err)
	}
	if err := db.Create(&model.SingerImport{
		SingerID:               "singer",
		PackageID:              "vendor/package",
		PackageVersion:         "1.0.0.0",
		ImportedInferenceID:    "acoustic",
		ImportedPackageID:      "vendor/package",
		ImportedPackageVersion: "1.0.0.0",
	}).Error; err != nil {
		t.Fatalf("create singer import: %v", err)
	}

	packageDir := installedPackageDir(packagesDir, "vendor/package", "1.0.0.0")
	if err := os.MkdirAll(packageDir, 0o755); err != nil {
		t.Fatalf("create package dir: %v", err)
	}
	files := map[string]string{
		"desc.json": `{
			"contributes": {
				"inferences": ["acoustic.json"],
				"singers": ["singer.json"]
			}
		}`,
		"acoustic.json": `{"id": "acoustic"}`,
		"singer.json":   `{"id": "singer"}`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(packageDir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	return packagesDir
}

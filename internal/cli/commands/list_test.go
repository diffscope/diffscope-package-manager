package commands

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diffscope/diffscope-package-manager/packagedatabase"
	"github.com/diffscope/diffscope-package-manager/packagedatabase/model"
)

func TestListPackagesTextDefaultsToIDAndVersion(t *testing.T) {
	packagesDir := makeListTestDatabase(t)

	var output bytes.Buffer
	if err := ListPackages(packagesDir, "en", "", false, &output); err != nil {
		t.Fatalf("ListPackages() error = %v", err)
	}

	got := output.String()
	if !strings.Contains(got, "ID") || !strings.Contains(got, "Version") {
		t.Fatalf("default output missing headers:\n%s", got)
	}
	if strings.Contains(got, "Hash") || strings.Contains(got, "Name") || strings.Contains(got, "InstalledAt") {
		t.Fatalf("default output contains non-default columns:\n%s", got)
	}
	if strings.Index(got, "vendor/alpha") > strings.Index(got, "vendor/beta") {
		t.Fatalf("packages not sorted by id:\n%s", got)
	}
	if strings.Index(got, "2.0.0.0") > strings.Index(got, "1.0.0.0") {
		t.Fatalf("versions not sorted descending within id:\n%s", got)
	}
}

func TestListPackagesTextUsesRequestedColumnsAndWildcardLanguage(t *testing.T) {
	packagesDir := makeListTestDatabase(t)

	var output bytes.Buffer
	if err := ListPackages(packagesDir, "*", "name,id,installed_at", false, &output); err != nil {
		t.Fatalf("ListPackages() error = %v", err)
	}

	got := output.String()
	if !strings.Contains(got, "Name") || !strings.Contains(got, "ID") || !strings.Contains(got, "InstalledAt") {
		t.Fatalf("output missing requested headers:\n%s", got)
	}
	if !strings.Contains(got, "[_] Alpha") || !strings.Contains(got, "[zh-CN] 阿尔法") {
		t.Fatalf("wildcard language output missing multilingual name:\n%s", got)
	}
	if !strings.Contains(got, formatInstalledAtText(2345)) {
		t.Fatalf("installed_at not formatted with shared formatter:\n%s", got)
	}
}

func TestListPackagesJSONUsesRequestedColumnsAndLanguageSelection(t *testing.T) {
	packagesDir := makeListTestDatabase(t)

	var output bytes.Buffer
	if err := ListPackages(packagesDir, "zh-CN", "id,name,hash", true, &output); err != nil {
		t.Fatalf("ListPackages() error = %v", err)
	}

	var payload listOutput
	if err := json.Unmarshal(output.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, output.String())
	}
	if !payload.OK || payload.Command != "list" {
		t.Fatalf("payload = %#v", payload)
	}
	if len(payload.Data.Packages) != 3 {
		t.Fatalf("packages = %#v", payload.Data.Packages)
	}
	first := payload.Data.Packages[0]
	if first.ID != "vendor/alpha" || first.Name != "阿尔法" || first.Hash != "hash-alpha-2" {
		t.Fatalf("first package = %#v", first)
	}
	if first.Version != "" || first.InstalledAt != "" {
		t.Fatalf("unrequested fields should be omitted in struct: %#v", first)
	}
}

func TestListPackagesJSONFormatsInstalledAtAsUTCMilliseconds(t *testing.T) {
	packagesDir := makeListTestDatabase(t)

	var output bytes.Buffer
	if err := ListPackages(packagesDir, "en", "id,installed_at", true, &output); err != nil {
		t.Fatalf("ListPackages() error = %v", err)
	}

	var payload listOutput
	if err := json.Unmarshal(output.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, output.String())
	}
	if len(payload.Data.Packages) == 0 {
		t.Fatalf("packages is empty")
	}
	if got := payload.Data.Packages[0].InstalledAt; got != "1970-01-01T00:00:02.345Z" {
		t.Fatalf("installedAt = %q, want millisecond UTC ISO 8601", got)
	}
}

func TestListPackagesJSONWildcardLanguageReturnsMap(t *testing.T) {
	packagesDir := makeListTestDatabase(t)

	var output bytes.Buffer
	if err := ListPackages(packagesDir, "*", "id,name", true, &output); err != nil {
		t.Fatalf("ListPackages() error = %v", err)
	}

	var payload listOutput
	if err := json.Unmarshal(output.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, output.String())
	}
	name, ok := payload.Data.Packages[0].Name.(map[string]any)
	if !ok {
		t.Fatalf("name = %#v, want language map", payload.Data.Packages[0].Name)
	}
	if name["_"] != "Alpha" || name["zh-CN"] != "阿尔法" {
		t.Fatalf("name map = %#v", name)
	}
}

func makeListTestDatabase(t *testing.T) string {
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
		{ID: "vendor/beta", Version: "1.0.0.0", Hash: "hash-beta", InstalledAt: 1234},
		{ID: "vendor/alpha", Version: "1.0.0.0", Hash: "hash-alpha-1", InstalledAt: 1234},
		{ID: "vendor/alpha", Version: "2.0.0.0", Hash: "hash-alpha-2", InstalledAt: 2345},
	}).Error; err != nil {
		t.Fatalf("create packages: %v", err)
	}
	if err := db.Create([]model.PackageMultilingualInfo{
		{
			PackageID:      "vendor/alpha",
			PackageVersion: "2.0.0.0",
			Language:       "_",
			Name:           stringPointer("Alpha"),
		},
		{
			PackageID:      "vendor/alpha",
			PackageVersion: "2.0.0.0",
			Language:       "zh-CN",
			Name:           stringPointer("阿尔法"),
		},
		{
			PackageID:      "vendor/beta",
			PackageVersion: "1.0.0.0",
			Language:       "_",
			Name:           stringPointer("Beta"),
		},
	}).Error; err != nil {
		t.Fatalf("create package names: %v", err)
	}

	return packagesDir
}

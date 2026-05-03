package packagearchive

import (
	"bytes"
	"strings"
	"testing"
)

func TestInspectPackage(t *testing.T) {
	archive := makeZipArchive(t, []zipTestFile{
		{name: "desc.json", body: `{
			"contributes": {
				"inferences": ["inf/acoustic.json", "inf/variance.json"],
				"singers": ["singers/main.json"]
			},
			"dependencies": [
				{"id": "vendor/base", "version": "1.0"},
				{"id": "vendor/base", "version": "2.0"},
				{"id": "vendor/explicit", "version": "3.1"}
			],
			"description": "Package description",
			"id": "vendor/package",
			"name": "Package name",
			"version": "1.2.3"
		}`},
		{name: "inf/acoustic.json", body: `{
			"$version": "1.0",
			"class": "DiffSingerAcoustic",
			"id": "acoustic",
			"level": 1,
			"name": "Acoustic"
		}`},
		{name: "inf/variance.json", body: `{
			"$version": "1.0",
			"class": "DiffSingerVariance",
			"id": "variance",
			"level": 1
		}`},
		{name: "singers/main.json", body: `{
			"$version": "1.0",
			"avatar": "avatar.png",
			"background": "background.png",
			"class": "DiffSingerSinger",
			"demoAudio": "demo.wav",
			"id": "singer",
			"imports": [
				"acoustic",
				{"id": "vendor/base", "inferenceId": "base_inf"},
				{"id": "vendor/explicit", "version": "3.1", "inferenceId": "explicit_inf"}
			],
			"level": 1,
			"name": "Singer"
		}`},
	})

	inspection, err := InspectPackage(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("InspectPackage() error = %v", err)
	}

	if inspection.ID != "vendor/package" || inspection.Version.String() != "1.2.3.0" {
		t.Fatalf("package identity = %s@%s", inspection.ID, inspection.Version.String())
	}
	if len(inspection.Dependencies) != 3 {
		t.Fatalf("Dependencies length = %d", len(inspection.Dependencies))
	}
	if inspection.Dependencies[0].PackageID != "vendor/base" || inspection.Dependencies[0].Version.String() != "1.0.0.0" {
		t.Fatalf("Dependencies[0] = %#v", inspection.Dependencies[0])
	}
	if len(inspection.Contributes.Inferences) != 2 {
		t.Fatalf("Inferences length = %d", len(inspection.Contributes.Inferences))
	}
	if inspection.Contributes.Inferences[0].ID != "acoustic" {
		t.Fatalf("Inferences[0] = %#v", inspection.Contributes.Inferences[0])
	}
	if len(inspection.Contributes.Singers) != 1 {
		t.Fatalf("Singers length = %d", len(inspection.Contributes.Singers))
	}

	singer := inspection.Contributes.Singers[0]
	if singer.ID != "singer" || singer.Class != "DiffSingerSinger" {
		t.Fatalf("Singer = %#v", singer)
	}
	if singer.Avatar == nil || singer.Avatar.Default != "singers/avatar.png" {
		t.Fatalf("Avatar = %#v", singer.Avatar)
	}
	if singer.Background == nil || singer.Background.Default != "singers/background.png" {
		t.Fatalf("Background = %#v", singer.Background)
	}
	if len(singer.DemoAudio) != 1 || singer.DemoAudio[0].Audio.Default != "singers/demo.wav" {
		t.Fatalf("DemoAudio = %#v", singer.DemoAudio)
	}
	if len(singer.Imports) != 3 {
		t.Fatalf("Imports length = %d", len(singer.Imports))
	}

	if got := singer.Imports[0].String(); got != "vendor/package@1.2.3.0:acoustic" {
		t.Fatalf("Imports[0] = %q", got)
	}
	if got := singer.Imports[1].String(); got != "vendor/base@2.0.0.0:base_inf" {
		t.Fatalf("Imports[1] = %q", got)
	}
	if got := singer.Imports[2].String(); got != "vendor/explicit@3.1.0.0:explicit_inf" {
		t.Fatalf("Imports[2] = %q", got)
	}
}

func TestInspectPackageRejectsVersionWithoutImportID(t *testing.T) {
	archive := makeInspectionArchiveWithImports(t, `[{"version": "1.0", "inferenceId": "acoustic"}]`)

	_, err := InspectPackage(bytes.NewReader(archive))
	if err == nil {
		t.Fatal("InspectPackage() expected error")
	}
	if !strings.Contains(err.Error(), "version cannot be specified without package id") {
		t.Fatalf("InspectPackage() error = %v", err)
	}
}

func TestInspectPackageAllowsMissingCurrentInferenceImport(t *testing.T) {
	archive := makeInspectionArchiveWithImports(t, `["missing"]`)

	inspection, err := InspectPackage(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("InspectPackage() error = %v", err)
	}
	if got := inspection.Contributes.Singers[0].Imports[0].String(); got != "vendor/package@1.0.0.0:missing" {
		t.Fatalf("Imports[0] = %q", got)
	}
}

func TestInspectPackageRejectsMissingDependency(t *testing.T) {
	archive := makeInspectionArchiveWithImports(t, `[{"id": "vendor/missing", "inferenceId": "remote"}]`)

	_, err := InspectPackage(bytes.NewReader(archive))
	if err == nil {
		t.Fatal("InspectPackage() expected error")
	}
	if !strings.Contains(err.Error(), `dependency "vendor/missing" not found`) {
		t.Fatalf("InspectPackage() error = %v", err)
	}
}

func makeInspectionArchiveWithImports(t *testing.T, imports string) []byte {
	t.Helper()

	return makeZipArchive(t, []zipTestFile{
		{name: "desc.json", body: `{
			"contributes": {
				"inferences": ["acoustic.json"],
				"singers": ["singer.json"]
			},
			"dependencies": [
				{"id": "vendor/base", "version": "1.0"}
			],
			"id": "vendor/package",
			"version": "1.0"
		}`},
		{name: "acoustic.json", body: `{
			"$version": "1.0",
			"class": "DiffSingerAcoustic",
			"id": "acoustic",
			"level": 1
		}`},
		{name: "singer.json", body: `{
			"$version": "1.0",
			"class": "DiffSingerSinger",
			"id": "singer",
			"imports": ` + imports + `,
			"level": 1
		}`},
	})
}

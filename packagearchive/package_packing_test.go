package packagearchive

import (
	"archive/zip"
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanAndCreatePackageConvertsNonJSONDescriptions(t *testing.T) {
	sourceDir := t.TempDir()
	writePackTestFile(t, sourceDir, "desc.toml", `
id = "vendor/sample"
version = "1.0"
name = "Sample"
description = "Sample package"
vendor = "Vendor"
readme = "README.md"
license = "LICENSE.txt"
url = "https://example.invalid"
dependencies = []

[contributes]
inferences = ["inferences/acoustic.yaml"]
singers = ["singers/main.toml"]
`)
	writePackTestFile(t, sourceDir, "README.md", "readme")
	writePackTestFile(t, sourceDir, "LICENSE.txt", "license")
	writePackTestFile(t, sourceDir, "inferences/acoustic.yaml", `
"$version": "1.0"
class: DiffScopeTestInference
id: acoustic
level: 1
name: Acoustic
`)
	writePackTestFile(t, sourceDir, "singers/main.toml", `
"$version" = "1.0"
class = "DiffScopeTestSinger"
id = "main"
imports = ["acoustic"]
level = 1
name = "Main"
avatar = "assets/avatar.png"
background = "assets/background.png"
demoAudio = "assets/demo.ogg"
`)
	writePackTestPNG(t, sourceDir, "assets/avatar.png", 2, 2)
	writePackTestPNG(t, sourceDir, "assets/background.png", 4, 2)
	writePackTestFile(t, sourceDir, "assets/demo.ogg", string([]byte{'O', 'g', 'g', 'S', 0, 1, 'v', 'o', 'r', 'b', 'i', 's'}))

	outputFile := filepath.Join(t.TempDir(), "sample.dspk")
	plan, err := PlanPackage(sourceDir, PackOptions{OutputFile: outputFile})
	if err != nil {
		t.Fatalf("PlanPackage() error = %v", err)
	}
	if plan.PackageID != "vendor/sample" || plan.Version.String() != "1.0.0.0" {
		t.Fatalf("identity = %s@%s", plan.PackageID, plan.Version.String())
	}
	if len(plan.Conversions) != 3 {
		t.Fatalf("Conversions length = %d, want 3: %#v", len(plan.Conversions), plan.Conversions)
	}

	var progress []PackProgress
	if err := CreatePackage(context.Background(), plan, func(item PackProgress) {
		progress = append(progress, item)
	}); err != nil {
		t.Fatalf("CreatePackage() error = %v", err)
	}
	if len(progress) == 0 {
		t.Fatalf("CreatePackage() did not report progress")
	}

	data, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatalf("read output package: %v", err)
	}
	entries := zipEntryNames(t, data)
	for _, want := range []string{"desc.json", "inferences/acoustic.json", "singers/main.json"} {
		if !entries[want] {
			t.Fatalf("package missing %s; entries=%v", want, entries)
		}
	}
	for _, forbidden := range []string{"desc.toml", "inferences/acoustic.yaml", "singers/main.toml"} {
		if entries[forbidden] {
			t.Fatalf("package included source description %s", forbidden)
		}
	}
	if _, err := InspectPackage(bytes.NewReader(data)); err != nil {
		t.Fatalf("InspectPackage(created package) error = %v", err)
	}
}

func TestPlanPackageRejectsContributedDescriptionJSONPathConflict(t *testing.T) {
	sourceDir := t.TempDir()
	writeMinimalPackPackage(t, sourceDir, []string{"singers/main.toml", "singers/main.yaml"}, nil)
	writePackTestFile(t, sourceDir, "singers/main.toml", `
"$version" = "1.0"
class = "DiffScopeTestSinger"
id = "main_toml"
imports = []
level = 1
`)
	writePackTestFile(t, sourceDir, "singers/main.yaml", `
"$version": "1.0"
class: DiffScopeTestSinger
id: main_yaml
imports: []
level: 1
`)

	_, err := PlanPackage(sourceDir, PackOptions{})
	if err == nil {
		t.Fatal("PlanPackage() expected error")
	}
	if !strings.Contains(err.Error(), "both pack as") {
		t.Fatalf("PlanPackage() error = %v", err)
	}
}

func TestPlanPackageRejectsMissingCurrentPackageInferenceImport(t *testing.T) {
	sourceDir := t.TempDir()
	writeMinimalPackPackage(t, sourceDir, nil, []string{"singers/main.json"})
	writePackTestFile(t, sourceDir, "singers/main.json", `{
		"$version": "1.0",
		"class": "DiffScopeTestSinger",
		"id": "main",
		"imports": ["missing"],
		"level": 1
	}`)

	_, err := PlanPackage(sourceDir, PackOptions{})
	if err == nil {
		t.Fatal("PlanPackage() expected error")
	}
	if !strings.Contains(err.Error(), `references missing current-package inference "missing"`) {
		t.Fatalf("PlanPackage() error = %v", err)
	}
}

func TestPlanPackageWarnsForSingerResourceFormats(t *testing.T) {
	sourceDir := t.TempDir()
	writeMinimalPackPackage(t, sourceDir, []string{"inferences/acoustic.json"}, []string{"singers/main.json"})
	writePackTestFile(t, sourceDir, "inferences/acoustic.json", `{
		"$version": "1.0",
		"class": "DiffScopeTestInference",
		"id": "acoustic",
		"level": 1
	}`)
	writePackTestFile(t, sourceDir, "singers/main.json", `{
		"$version": "1.0",
		"avatar": "assets/avatar.png",
		"background": "assets/background.txt",
		"class": "DiffScopeTestSinger",
		"demoAudio": "assets/demo.wav",
		"id": "main",
		"imports": ["acoustic"],
		"level": 1
	}`)
	writePackTestPNG(t, sourceDir, "assets/avatar.png", 4, 2)
	writePackTestFile(t, sourceDir, "assets/background.txt", "not png")
	writePackTestFile(t, sourceDir, "assets/demo.wav", "not ogg")

	plan, err := PlanPackage(sourceDir, PackOptions{})
	if err != nil {
		t.Fatalf("PlanPackage() error = %v", err)
	}
	var joined strings.Builder
	for _, warning := range plan.Warnings {
		joined.WriteString(warning.Message)
		joined.WriteByte('\n')
	}
	text := joined.String()
	for _, want := range []string{"avatar[_] is not square", "background[_] is not a PNG file", "demoAudio[0].path[_] is not OGG Vorbis"} {
		if !strings.Contains(text, want) {
			t.Fatalf("warnings missing %q:\n%s", want, text)
		}
	}
}

func writeMinimalPackPackage(t *testing.T, sourceDir string, inferences []string, singers []string) {
	t.Helper()
	inferenceJSON, _ := jsonStringArray(inferences)
	singerJSON, _ := jsonStringArray(singers)
	writePackTestFile(t, sourceDir, "desc.json", `{
		"contributes": {
			"inferences": `+inferenceJSON+`,
			"singers": `+singerJSON+`
		},
		"dependencies": [],
		"id": "vendor/sample",
		"version": "1.0"
	}`)
}

func jsonStringArray(values []string) (string, error) {
	var buffer bytes.Buffer
	buffer.WriteByte('[')
	for index, value := range values {
		if index > 0 {
			buffer.WriteByte(',')
		}
		buffer.WriteByte('"')
		buffer.WriteString(value)
		buffer.WriteByte('"')
	}
	buffer.WriteByte(']')
	return buffer.String(), nil
}

func writePackTestFile(t *testing.T, sourceDir string, packagePath string, body string) {
	t.Helper()
	target := filepath.Join(sourceDir, filepath.FromSlash(packagePath))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("create directory for %s: %v", packagePath, err)
	}
	if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", packagePath, err)
	}
}

func writePackTestPNG(t *testing.T, sourceDir string, packagePath string, width int, height int) {
	t.Helper()
	target := filepath.Join(sourceDir, filepath.FromSlash(packagePath))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("create directory for %s: %v", packagePath, err)
	}
	file, err := os.Create(target)
	if err != nil {
		t.Fatalf("create %s: %v", packagePath, err)
	}
	defer file.Close()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	if err := png.Encode(file, img); err != nil {
		t.Fatalf("encode %s: %v", packagePath, err)
	}
}

func zipEntryNames(t *testing.T, data []byte) map[string]bool {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	entries := make(map[string]bool, len(reader.File))
	for _, file := range reader.File {
		entries[file.Name] = true
	}
	return entries
}

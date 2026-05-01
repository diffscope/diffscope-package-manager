package packageinfo

import (
	"encoding/json"
	"testing"

	"github.com/go-playground/validator/v10"
)

func TestPackageDescriptionJSON(t *testing.T) {
	data := []byte(`{
		"contributes": {
			"inferences": ["inference.json"],
			"singers": ["singer.json"]
		},
		"dependencies": [
			{"id": "vendor/base", "version": "1.2.3"}
		],
		"description": {"_": "Default description", "zh-CN": "中文描述"},
		"id": "vendor/package",
		"name": "Package name",
		"version": "2.0"
	}`)

	var description PackageDescription
	if err := json.Unmarshal(data, &description); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if description.ID != "vendor/package" {
		t.Fatalf("ID = %q", description.ID)
	}
	if description.Version.String() != "2.0.0.0" {
		t.Fatalf("Version = %q", description.Version.String())
	}
	if len(description.Dependencies) != 1 || description.Dependencies[0].Version.String() != "1.2.3.0" {
		t.Fatalf("Dependencies = %#v", description.Dependencies)
	}
	if description.Name == nil || description.Name.Default != "Package name" {
		t.Fatalf("Name = %#v", description.Name)
	}
	if description.Description == nil || description.Description.Texts["zh-CN"] != "中文描述" {
		t.Fatalf("Description = %#v", description.Description)
	}
}

func TestPackageDescriptionValidatorTags(t *testing.T) {
	v := validator.New()
	if err := RegisterValidator(v); err != nil {
		t.Fatalf("RegisterValidator() error = %v", err)
	}

	description := PackageDescription{
		Contributes: PackageContributions{
			Inferences: []string{"inference.json"},
			Singers:    []string{"singer.json"},
		},
		Dependencies: []PackageDependency{
			{ID: "vendor/base", Version: MustParsePackageVersion("1.2.3")},
		},
		ID:      "vendor/package",
		Version: MustParsePackageVersion("2.0"),
	}
	if err := v.Struct(description); err != nil {
		t.Fatalf("valid description rejected: %v", err)
	}

	description.Dependencies[0].ID = "bad/package/"
	if err := v.Struct(description); err == nil {
		t.Fatal("invalid dependency ID accepted unexpectedly")
	}
}

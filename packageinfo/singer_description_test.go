package packageinfo

import (
	"encoding/json"
	"testing"

	"github.com/go-playground/validator/v10"
)

func TestSingerDescriptionJSON(t *testing.T) {
	data := []byte(`{
		"$version": "1.0",
		"avatar": "avatar.png",
		"background": {"_": "background.png", "zh-CN": "background.zh.png"},
		"class": "DiffSingerSinger",
		"configuration": {"phonemeMode": "ds"},
		"demoAudio": [
			{
				"name": {"_": "Default demo", "zh-CN": "默认试听"},
				"path": {"_": "demo.wav", "zh-CN": "demo.zh.wav"}
			}
		],
		"id": "singer",
		"imports": [
			"acoustic",
			{
				"id": "vendor/package",
				"inferenceId": "variance",
				"options": {"predict": true},
				"version": "1.2.3"
			}
		],
		"level": 1,
		"name": {"_": "Singer", "zh-CN": "歌手"}
	}`)

	var description SingerDescription
	if err := json.Unmarshal(data, &description); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if description.FormatVersion != SingerDescriptionFormatVersion {
		t.Fatalf("FormatVersion = %q", description.FormatVersion)
	}
	if description.Avatar == nil || description.Avatar.Default != "avatar.png" {
		t.Fatalf("Avatar = %#v", description.Avatar)
	}
	if description.Background == nil || description.Background.Texts["zh-CN"] != "background.zh.png" {
		t.Fatalf("Background = %#v", description.Background)
	}
	if description.ID != "singer" {
		t.Fatalf("ID = %q", description.ID)
	}
	if len(description.DemoAudio) != 1 || description.DemoAudio[0].Name.Texts["zh-CN"] != "默认试听" {
		t.Fatalf("DemoAudio = %#v", description.DemoAudio)
	}
	if len(description.Imports) != 2 {
		t.Fatalf("Imports = %#v", description.Imports)
	}
	if description.Imports[0].InferenceID != "acoustic" || description.Imports[0].ID != "" {
		t.Fatalf("Imports[0] = %#v", description.Imports[0])
	}
	if description.Imports[1].Version == nil || description.Imports[1].Version.String() != "1.2.3.0" {
		t.Fatalf("Imports[1].Version = %#v", description.Imports[1].Version)
	}
	if description.Imports[1].Options == nil || !json.Valid(*description.Imports[1].Options) {
		t.Fatalf("Imports[1].Options = %s", rawJSONForTest(description.Imports[1].Options))
	}
	if description.Configuration == nil || !json.Valid(*description.Configuration) {
		t.Fatalf("Configuration = %s", rawJSONForTest(description.Configuration))
	}
}

func TestSingerDescriptionSingleDemoAudioJSON(t *testing.T) {
	data := []byte(`{
		"$version": "1.0",
		"class": "DiffSingerSinger",
		"demoAudio": {"_": "demo.wav", "zh-CN": "demo.zh.wav"},
		"id": "singer",
		"imports": ["acoustic"],
		"level": 1
	}`)

	var description SingerDescription
	if err := json.Unmarshal(data, &description); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if len(description.DemoAudio) != 1 {
		t.Fatalf("DemoAudio length = %d", len(description.DemoAudio))
	}
	if description.DemoAudio[0].Name.Default != "" {
		t.Fatalf("DemoAudio[0].Name = %#v", description.DemoAudio[0].Name)
	}
	if description.DemoAudio[0].Path.Texts["zh-CN"] != "demo.zh.wav" {
		t.Fatalf("DemoAudio[0].Path = %#v", description.DemoAudio[0].Path)
	}
}

func TestSingerDescriptionValidatorTags(t *testing.T) {
	v := validator.New()
	if err := RegisterValidator(v); err != nil {
		t.Fatalf("RegisterValidator() error = %v", err)
	}

	description := SingerDescription{
		FormatVersion: SingerDescriptionFormatVersion,
		Class:         "DiffSingerSinger",
		ID:            "singer",
		Imports: SingerInferenceImports{
			{InferenceID: "acoustic"},
			{
				ID:          "vendor/package",
				InferenceID: "variance",
				Version:     packageVersionPtrForTest("1.2.3"),
			},
		},
		Level: 1,
	}
	if err := v.Struct(description); err != nil {
		t.Fatalf("valid description rejected: %v", err)
	}

	description.ID = "bad/singer"
	if err := v.Struct(description); err == nil {
		t.Fatal("invalid singer ID accepted unexpectedly")
	}

	description.ID = "singer"
	description.Imports[1].ID = "bad/package/"
	if err := v.Struct(description); err == nil {
		t.Fatal("invalid import package ID accepted unexpectedly")
	}

	description.Imports[1].ID = "vendor/package"
	description.Imports[0].InferenceID = "bad/inference"
	if err := v.Struct(description); err == nil {
		t.Fatal("invalid import inference ID accepted unexpectedly")
	}
}

func packageVersionPtrForTest(text string) *PackageVersion {
	version := MustParsePackageVersion(text)
	return &version
}

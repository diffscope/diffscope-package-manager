package packageinfo

import (
	"encoding/json"
	"testing"

	"github.com/go-playground/validator/v10"
)

func TestInferenceDescriptionJSON(t *testing.T) {
	data := []byte(`{
		"$version": "1.0",
		"class": "DiffSingerAcoustic",
		"configuration": {"sampleRate": 44100},
		"id": "acoustic",
		"level": 1,
		"name": {"_": "Acoustic inference", "zh-CN": "声学推理"},
		"schema": {"type": "object"}
	}`)

	var description InferenceDescription
	if err := json.Unmarshal(data, &description); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if description.FormatVersion != InferenceDescriptionFormatVersion {
		t.Fatalf("FormatVersion = %q", description.FormatVersion)
	}
	if description.Class != "DiffSingerAcoustic" {
		t.Fatalf("Class = %q", description.Class)
	}
	if description.ID != "acoustic" {
		t.Fatalf("ID = %q", description.ID)
	}
	if description.Level != 1 {
		t.Fatalf("Level = %d", description.Level)
	}
	if description.Name == nil || description.Name.Texts["zh-CN"] != "声学推理" {
		t.Fatalf("Name = %#v", description.Name)
	}
	if description.Configuration == nil || !json.Valid(*description.Configuration) {
		t.Fatalf("Configuration = %s", rawJSONForTest(description.Configuration))
	}
	if description.Schema == nil || !json.Valid(*description.Schema) {
		t.Fatalf("Schema = %s", rawJSONForTest(description.Schema))
	}
}

func TestInferenceDescriptionValidatorTags(t *testing.T) {
	v := validator.New()
	if err := RegisterValidator(v); err != nil {
		t.Fatalf("RegisterValidator() error = %v", err)
	}

	description := InferenceDescription{
		FormatVersion: InferenceDescriptionFormatVersion,
		Class:         "DiffSingerAcoustic",
		ID:            "acoustic",
		Level:         1,
	}
	if err := v.Struct(description); err != nil {
		t.Fatalf("valid description rejected: %v", err)
	}

	description.ID = "bad/inference"
	if err := v.Struct(description); err == nil {
		t.Fatal("invalid inference ID accepted unexpectedly")
	}

	description.ID = "acoustic"
	description.FormatVersion = "2.0"
	if err := v.Struct(description); err == nil {
		t.Fatal("invalid format version accepted unexpectedly")
	}
}

func rawJSONForTest(value *json.RawMessage) string {
	if value == nil {
		return "<nil>"
	}
	return string(*value)
}

package packageinfo

import (
	"encoding/json"
	"testing"
)

func TestMultilingualTextUnmarshalString(t *testing.T) {
	var text MultilingualText
	if err := json.Unmarshal([]byte(`"hello"`), &text); err != nil {
		t.Fatalf("Unmarshal string error = %v", err)
	}
	if text.Default != "hello" {
		t.Fatalf("Default = %q", text.Default)
	}
	if text.Texts != nil {
		t.Fatalf("Texts expected nil")
	}
}

func TestMultilingualTextUnmarshalMap(t *testing.T) {
	var text MultilingualText
	input := []byte(`{"_":"base","zh-CN":"zh"}`)
	if err := json.Unmarshal(input, &text); err != nil {
		t.Fatalf("Unmarshal map error = %v", err)
	}
	if text.Default != "base" {
		t.Fatalf("Default = %q", text.Default)
	}
	if text.Texts == nil || text.Texts["zh-CN"] != "zh" {
		t.Fatalf("Texts = %#v", text.Texts)
	}
}

func TestMultilingualTextUnmarshalMissingDefault(t *testing.T) {
	var text MultilingualText
	if err := json.Unmarshal([]byte(`{"zh-CN":"zh"}`), &text); err == nil {
		t.Fatal("expected error for missing default")
	}
}

func TestMultilingualTextMarshalString(t *testing.T) {
	text := MultilingualText{Default: "hello"}
	got, err := json.Marshal(text)
	if err != nil {
		t.Fatalf("Marshal error = %v", err)
	}
	if string(got) != `"hello"` {
		t.Fatalf("Marshal = %s", string(got))
	}
}

func TestMultilingualTextMarshalMap(t *testing.T) {
	text := MultilingualText{Default: "base", Texts: map[string]string{"zh-CN": "zh"}}
	got, err := json.Marshal(text)
	if err != nil {
		t.Fatalf("Marshal error = %v", err)
	}

	var decoded map[string]string
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("Unmarshal output error = %v", err)
	}
	if decoded["_"] != "base" || decoded["zh-CN"] != "zh" {
		t.Fatalf("Decoded = %#v", decoded)
	}
}

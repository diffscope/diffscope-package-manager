package packageinfo

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCompletePackageMetadata(t *testing.T) {
	root := t.TempDir()
	desc := `{"$version":"1.0","runtimeLevel":1,"id":"vendor/voice","version":"2.3","compatVersion":"1","vars":[{"name":"title","value":"Voice"},{"name":"assets","value":"${root}/docs"}],"name":"${title}","vendor":{"_":"Vendor","ZH-cn":"提供者"},"license":{"_":"docs\\..\\LICENSE.txt","zh-CN":"${assets}/../许可证.txt","ZH-cn":"alternate.txt"},"description":"Description ${title}","readme":{"_":"docs\\..\\readme.txt","zh-CN":"${assets}/../说明.txt"},"url":"https://example.test/${title}","dependencies":[{"id":"vendor/base","version":"1.2"}],"contributions":{"vendor.data":[{"id":"main","custom":{"file":"../../unchecked"}}]},"extension":{"text":"${title}","enabled":true,"value":null}}`
	p, err := Load(manifestFS(desc, map[string]string{"readme.txt": "Readme", "说明.txt": "说明", "LICENSE.txt": "License", "许可证.txt": "许可证", "alternate.txt": "Alternate license"}), root)
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "vendor/voice" || p.Version.String() != "2.3.0.0" || p.CompatVersion.String() != "1.0.0.0" || p.FormatVersion.String() != "1.0.0.0" || p.RuntimeLevel != 1 {
		t.Fatalf("incomplete package identity or requirements: %+v", p)
	}
	for _, field := range []struct {
		name string
		got  Text
		want Text
	}{
		{"name", p.Name, Text{"_": "Voice"}},
		{"vendor", p.Vendor, Text{"_": "Vendor", "ZH-cn": "提供者"}},
		{"license", p.License, Text{"_": filepath.Join(root, "LICENSE.txt"), "zh-CN": filepath.Join(root, "许可证.txt"), "ZH-cn": filepath.Join(root, "alternate.txt")}},
		{"description", p.Description, Text{"_": "Description Voice"}},
		{"readme", p.Readme, Text{"_": filepath.Join(root, "readme.txt"), "zh-CN": filepath.Join(root, "说明.txt")}},
	} {
		if !reflect.DeepEqual(field.got, field.want) {
			t.Errorf("%s: got %#v, want %#v", field.name, field.got, field.want)
		}
	}
	if p.URL == nil || *p.URL != "https://example.test/Voice" {
		t.Fatalf("missing expanded URL: %v", p.URL)
	}
	if len(p.Dependencies) != 1 || p.Dependencies[0].ID != "vendor/base" || p.Dependencies[0].Version.String() != "1.2.0.0" {
		t.Fatal("missing typed dependencies", p.Dependencies)
	}
	if len(p.Contributions) != 1 || p.Contributions[0].Entry["custom"].(map[string]any)["file"] != "../../unchecked" {
		t.Fatal("transparent contribution changed", p.Contributions)
	}
	readme := p.Manifest["readme"].(map[string]any)
	if readme["_"] != `docs\..\readme.txt` || readme["zh-CN"] != root+"/docs/../说明.txt" {
		t.Fatal("resolved readme overwrote expanded declaration", readme)
	}
	license := p.Manifest["license"].(map[string]any)
	if license["_"] != `docs\..\LICENSE.txt` || license["zh-CN"] != root+"/docs/../许可证.txt" {
		t.Fatal("License path resolution modified the expanded declaration.", license)
	}
	if p.Manifest["extension"].(map[string]any)["text"] != "Voice" {
		t.Fatal("unknown extension lost", p.Manifest)
	}
	if _, exists := p.Manifest["vars"]; exists {
		t.Fatal("consumed variables leaked into metadata")
	}
	encoded, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"id", "version", "formatVersion", "runtimeLevel", "compatVersion", "dependencies", "contributions", "name", "vendor", "license", "description", "readme", "url", "manifest"} {
		if _, exists := fields[field]; !exists {
			t.Errorf("standard metadata missing from serialized package: %s", field)
		}
	}
}

func TestOptionalPackageMetadataPreservesPresence(t *testing.T) {
	emptyURL := ""
	for _, test := range []struct {
		name        string
		more        string
		wantLicense string
		wantURL     *string
	}{
		{"absent", "", "", nil},
		{"empty-url", `,"url":""`, "", &emptyURL},
		{"license-string", `,"license":"LICENSE.txt"`, "LICENSE.txt", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			p, err := Load(manifestFS(`{"$version":"1","runtimeLevel":1,"id":"voice","version":"1"`+test.more+`}`, map[string]string{"LICENSE.txt": "License"}), root)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &fields); err != nil {
				t.Fatal(err)
			}
			if p.Readme != nil || fields["readme"] != nil {
				t.Fatal("Absent readme metadata was emitted.", p.Readme)
			}
			if !reflect.DeepEqual(p.URL, test.wantURL) || (fields["url"] != nil) != (test.wantURL != nil) {
				t.Fatalf("URL presence was not preserved: %s", encoded)
			}
			if test.wantURL != nil && string(fields["url"]) != `""` {
				t.Fatalf("The explicit empty URL was not preserved: %s", encoded)
			}
			var wantLicense Text
			if test.wantLicense != "" {
				wantLicense = Text{"_": filepath.Join(root, test.wantLicense)}
			}
			if !reflect.DeepEqual(p.License, wantLicense) || (fields["license"] != nil) != (wantLicense != nil) {
				t.Fatalf("License presence or path resolution is invalid: %s", encoded)
			}
		})
	}
}

func TestStandardPackageMetadataRejectsInvalidValues(t *testing.T) {
	for _, test := range []struct {
		field string
		value string
	}{
		{"license", `null`},
		{"license", `""`},
		{"license", `{"en":"LICENSE.txt"}`},
		{"license", `{"_":"LICENSE.txt","en":5}`},
		{"license", `"missing.txt"`},
		{"license", `"../outside.txt"`},
		{"license", `"docs"`},
		{"license", `{"_":"LICENSE.txt","zh-CN":""}`},
		{"license", `{"_":"LICENSE.txt","zh-CN":"../outside.txt"}`},
		{"license", `{"_":"LICENSE.txt","zh-CN":"missing.txt"}`},
		{"url", `false`},
		{"url", `null`},
		{"readme", `{"_":"readme.txt","zh-CN":"../outside.txt"}`},
		{"readme", `{"_":"readme.txt","zh-CN":"missing.txt"}`},
	} {
		t.Run(test.field+"/"+test.value, func(t *testing.T) {
			desc := `{"$version":"1","runtimeLevel":1,"id":"voice","version":"1","` + test.field + `":` + test.value + `}`
			_, err := Load(manifestFS(desc, map[string]string{"readme.txt": "Readme", "LICENSE.txt": "License", "docs/note.txt": "Note"}), t.TempDir())
			if err == nil || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("expected %s validation failure, got %v", test.field, err)
			}
		})
	}
}

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
	desc := `{"$version":"1.0","runtimeLevel":1,"id":"vendor/voice","version":"2.3","compatVersion":"1","vars":[{"name":"title","value":"Voice"},{"name":"assets","value":"${root}/docs"}],"name":"${title}","vendor":{"_":"Vendor","ZH-cn":"提供者"},"copyright":{"_":"","zh-CN":"版权 ${title}","ZH-cn":"Different key"},"description":"Description ${title}","readme":{"_":"docs\\..\\readme.txt","zh-CN":"${assets}/../说明.txt"},"url":"https://example.test/${title}","dependencies":[{"id":"vendor/base","version":"1.2"}],"contributions":{"vendor.data":[{"id":"main","custom":{"file":"../../unchecked"}}]},"extension":{"text":"${title}","enabled":true,"value":null}}`
	p, err := Load(manifestFS(desc, map[string]string{"readme.txt": "Readme", "说明.txt": "说明"}), root)
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
		{"copyright", p.Copyright, Text{"_": "", "zh-CN": "版权 Voice", "ZH-cn": "Different key"}},
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
	for _, field := range []string{"id", "version", "formatVersion", "runtimeLevel", "compatVersion", "dependencies", "contributions", "name", "vendor", "copyright", "description", "readme", "url", "manifest"} {
		if _, exists := fields[field]; !exists {
			t.Errorf("standard metadata missing from serialized package: %s", field)
		}
	}
}

func TestOptionalPackageMetadataPreservesPresence(t *testing.T) {
	for _, test := range []struct {
		name string
		more string
	}{
		{"absent", ""},
		{"empty", `,"copyright":"","url":""`},
	} {
		t.Run(test.name, func(t *testing.T) {
			p, err := Load(manifestFS(`{"$version":"1","runtimeLevel":1,"id":"voice","version":"1"`+test.more+`}`, nil), t.TempDir())
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
			if p.Readme != nil {
				t.Fatal("absent readme invented", p.Readme)
			}
			if test.name == "absent" {
				if p.Copyright != nil || p.URL != nil || fields["copyright"] != nil || fields["url"] != nil || fields["readme"] != nil {
					t.Fatalf("optional metadata invented: %s", encoded)
				}
			} else if p.Copyright["_"] != "" || p.URL == nil || *p.URL != "" || string(fields["copyright"]) != `{"_":""}` || string(fields["url"]) != `""` {
				t.Fatalf("explicit empty metadata lost: %s", encoded)
			}
		})
	}
}

func TestStandardPackageMetadataRejectsInvalidValues(t *testing.T) {
	for _, test := range []struct {
		field string
		value string
	}{
		{"copyright", `null`},
		{"copyright", `{"en":"Missing default"}`},
		{"copyright", `{"_":"Copyright","en":5}`},
		{"url", `false`},
		{"url", `null`},
		{"readme", `{"_":"readme.txt","zh-CN":"../outside.txt"}`},
		{"readme", `{"_":"readme.txt","zh-CN":"missing.txt"}`},
	} {
		t.Run(test.field+"/"+test.value, func(t *testing.T) {
			desc := `{"$version":"1","runtimeLevel":1,"id":"voice","version":"1","` + test.field + `":` + test.value + `}`
			_, err := Load(manifestFS(desc, map[string]string{"readme.txt": "Readme"}), t.TempDir())
			if err == nil || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("expected %s validation failure, got %v", test.field, err)
			}
		})
	}
}

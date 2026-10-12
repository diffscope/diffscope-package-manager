package packageinfo

import (
	"encoding/json"
	"strings"
	"testing"
)

const singerPackage = `{"$version":"1","runtimeLevel":1,"id":"voice","version":"1","contributions":{"singer":[{"id":"main","path":"singer.json"}]}}`

func TestSingerRequiredFieldsHaveExplicitFailureState(t *testing.T) {
	for _, field := range []string{"interface", "level", "variant"} {
		for _, invalid := range []struct {
			name  string
			value any
		}{
			{"missing", nil},
			{"null", nil},
			{"incorrect-type", false},
		} {
			t.Run(field+"/"+invalid.name, func(t *testing.T) {
				declaration := map[string]any{
					"interface": "example.Singer", "level": json.Number("2"), "variant": "v",
					"name": "Singer", "avatar": "../../unchecked.png", "imports": false,
				}
				if invalid.name == "missing" {
					delete(declaration, field)
				} else {
					declaration[field] = invalid.value
				}
				data, err := json.Marshal(declaration)
				if err != nil {
					t.Fatal(err)
				}
				p, err := Load(manifestFS(singerPackage, map[string]string{"singer.json": string(data)}), t.TempDir())
				if err != nil {
					t.Fatal("A singer extraction failure rejected the package.", err)
				}
				c := p.Contributions[0]
				if c.SingerDeclaration == nil || !c.Expanded || c.ModuleMetadata != nil || !strings.Contains(c.MetadataError, field) || len(p.Diagnostics) == 0 {
					t.Fatal("The singer extraction failure state is incomplete.", c, p.Diagnostics)
				}
				if c.Name["_"] != "Singer" || c.Singer == nil || c.Singer.Avatar["_"] != "../../unchecked.png" || c.Manifest["imports"] != false {
					t.Fatal("Available metadata or the retained declaration was discarded.", c)
				}
				if _, exists := c.Manifest[field]; exists != (invalid.name != "missing") {
					t.Fatal("The declaration field presence was modified.", c.Manifest)
				}
				encoded, err := json.Marshal(c)
				if err != nil {
					t.Fatal(err)
				}
				var wire map[string]json.RawMessage
				if err := json.Unmarshal(encoded, &wire); err != nil {
					t.Fatal(err)
				}
				for _, required := range []string{"interface", "level", "variant"} {
					if _, exists := wire[required]; exists {
						t.Fatal("Partial common module metadata was emitted as a successful result.", string(encoded))
					}
				}
				if wire["metadataError"] == nil {
					t.Fatal("The serialized extraction failure omitted its error.", string(encoded))
				}
			})
		}
	}
}

func TestSingerCompleteMetadataContainsRequiredFields(t *testing.T) {
	p, err := Load(manifestFS(singerPackage, map[string]string{
		"singer.json": `{"interface":"example.Singer","level":2e0,"variant":"v","name":{"_":"","zh-CN":"歌手"},"avatar":"../../unchecked.png","imports":false}`,
	}), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := p.Contributions[0]
	if c.ModuleMetadata == nil || c.MetadataError != "" || len(p.Diagnostics) != 0 || c.Interface != "example.Singer" || c.Level != "2" || c.Variant != "v" || c.Name["_"] != "" {
		t.Fatal("Complete singer metadata was not extracted.", c, p.Diagnostics)
	}
	encoded, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	for field, want := range map[string]string{"interface": `"example.Singer"`, "level": "2", "variant": `"v"`} {
		if string(wire[field]) != want {
			t.Fatal("A required singer field is absent or incorrect.", field, string(encoded))
		}
	}
	if wire["metadataError"] != nil {
		t.Fatal("A successful singer extraction included an error.", string(encoded))
	}
}

func TestSingerExpansionFailureRetainsDeclaration(t *testing.T) {
	p, err := Load(manifestFS(singerPackage, map[string]string{
		"singer.json": `{"vars":42,"interface":"example.Singer","level":2,"variant":"v","name":"${title}"}`,
	}), t.TempDir())
	if err != nil {
		t.Fatal("A singer expansion failure rejected the package.", err)
	}
	c := p.Contributions[0]
	if c.Expanded || c.ModuleMetadata != nil || c.MetadataError == "" || c.Name != nil || c.Singer != nil || c.Manifest["name"] != "${title}" || len(p.Diagnostics) == 0 {
		t.Fatal("The singer expansion failure state is incomplete.", c, p.Diagnostics)
	}
	if _, exists := c.Manifest["vars"]; !exists {
		t.Fatal("The unexpanded declaration was modified.", c.Manifest)
	}
}

func TestPackageRequiredFieldsRemainRequired(t *testing.T) {
	for _, field := range []string{"$version", "id", "version", "runtimeLevel"} {
		t.Run(field, func(t *testing.T) {
			declaration := map[string]any{"$version": "1", "id": "voice", "version": "1", "runtimeLevel": 1}
			delete(declaration, field)
			data, err := json.Marshal(declaration)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Load(manifestFS(string(data), nil), t.TempDir()); err == nil {
				t.Fatal("A package without a required field was accepted.", field)
			}
		})
	}
}

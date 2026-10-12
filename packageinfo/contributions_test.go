package packageinfo

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"reflect"
	"testing"
)

// Direct desc.json references permit Stat. Opaque declaration content must not be opened.
type contributionReadGuard struct {
	fs.FS
	blocked map[string]bool
	opens   map[string]int
}

func (g *contributionReadGuard) Open(name string) (fs.File, error) {
	g.opens[name]++
	if g.blocked[name] {
		return nil, fmt.Errorf("opaque contribution was opened: %s", name)
	}
	return g.FS.Open(name)
}

func (g *contributionReadGuard) Stat(name string) (fs.FileInfo, error) {
	return fs.Stat(g.FS, name)
}

func TestNonSingerDeclarationsAreOpaque(t *testing.T) {
	for _, category := range []string{"inference", "linguist", "com.example.data", "singer.extra"} {
		for _, declaration := range []struct {
			name string
			data string
		}{
			{"binary", "\xff\x00BINARY"},
			{"invalid-json", `{"name":`},
			{"bom-and-comments", "\ufeff{/* comment */\"name\":\"opaque\"}// comment"},
			{"duplicate-keys", `{"name":"one","name":"two"}`},
			{"invalid-vars", `{"vars":42,"name":"not indexed"}`},
			{"valid-json", `{"vars":[{"name":"title","value":"not indexed"}],"name":"${title}","interface":"example.Contract","level":9,"variant":"v","avatar":"outside.png"}`},
		} {
			t.Run(category+"/"+declaration.name, func(t *testing.T) {
				desc := fmt.Sprintf(`{"$version":"1","runtimeLevel":1,"id":"voice","version":"1","vars":[{"name":"folder","value":"data"}],"contributions":{%s:[{"id":"main","path":"${folder}/resource.bin","name":false,"interface":{},"level":"opaque","variant":null,"custom":{"file":"../../unchecked"}}]}}`, quote(category))
				files := &contributionReadGuard{
					FS:      manifestFS(desc, map[string]string{"data/resource.bin": declaration.data}),
					blocked: map[string]bool{"data/resource.bin": true},
					opens:   map[string]int{},
				}
				p, err := Load(files, t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				if len(p.Contributions) != 1 || len(p.Diagnostics) != 0 || files.opens["data/resource.bin"] != 0 {
					t.Fatal("opaque content interpreted", p.Contributions, p.Diagnostics, files.opens)
				}
				c := p.Contributions[0]
				if c.Category != category || c.ID != "main" || c.Path != "data/resource.bin" {
					t.Fatal("identity or direct reference lost", c)
				}
				if c.Entry["path"] != "data/resource.bin" || c.Entry["custom"].(map[string]any)["file"] != "../../unchecked" {
					t.Fatal("opaque entry lost", c)
				}
				if c.SingerDeclaration != nil {
					t.Fatal("declaration metadata invented", c)
				}
				data, err := json.Marshal(c)
				if err != nil {
					t.Fatal(err)
				}
				var wire map[string]any
				if err := json.Unmarshal(data, &wire); err != nil {
					t.Fatal(err)
				}
				for _, field := range []string{"name", "interface", "level", "variant", "manifest", "expanded", "singer", "metadataError"} {
					if _, exists := wire[field]; exists {
						t.Fatalf("opaque contribution exposed %s: %s", field, data)
					}
				}
			})
		}
	}
}

func TestOpaqueCategoryOwnsNonStringPath(t *testing.T) {
	for _, value := range []string{`null`, `false`, `42`, `{"custom":true}`, `["not","file","references"]`} {
		t.Run(value, func(t *testing.T) {
			desc := `{"$version":"1","runtimeLevel":1,"id":"voice","version":"1","contributions":{"com.example.data":[{"id":"main","path":` + value + `}]}}`
			p, err := Load(manifestFS(desc, nil), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			c := p.Contributions[0]
			var want any
			if err := json.Unmarshal([]byte(value), &want); err != nil {
				t.Fatal(err)
			}
			// JSON numbers in loaded metadata use json.Number.
			got, err := json.Marshal(c.Entry["path"])
			if err != nil {
				t.Fatal(err)
			}
			var actual any
			if err := json.Unmarshal(got, &actual); err != nil {
				t.Fatal(err)
			}
			if c.Path != "" || c.SingerDeclaration != nil || !reflect.DeepEqual(actual, want) || len(p.Diagnostics) != 0 {
				t.Fatal("category-owned path interpreted", c, p.Diagnostics)
			}
		})
	}
}

func TestOnlySingerDeclarationIsRead(t *testing.T) {
	desc := `{"$version":"1","runtimeLevel":1,"id":"voice","version":"1","vars":[{"name":"title","value":"Singer"}],"contributions":{"inference":[{"id":"opaque","path":"shared.json"}],"singer":[{"id":"main","path":"shared.json"}]}}`
	files := &contributionReadGuard{
		FS:    manifestFS(desc, map[string]string{"shared.json": `{"name":"${title}","interface":"example.Singer","level":2,"variant":"v","avatar":"../../missing.png","background":{"_":"missing.png","zh-CN":"/outside.png"},"demoAudio":false,"imports":false}`}),
		opens: map[string]int{},
	}
	p, err := Load(files, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if files.opens["shared.json"] != 1 || len(p.Contributions) != 2 {
		t.Fatal("declaration not read exclusively for singer", files.opens, p.Contributions)
	}
	opaque, singer := p.Contributions[0], p.Contributions[1]
	if opaque.SingerDeclaration != nil {
		t.Fatal("singer declaration leaked into opaque contribution", opaque)
	}
	if singer.SingerDeclaration == nil || singer.ModuleMetadata == nil || singer.Name["_"] != "Singer" || singer.Interface != "example.Singer" || singer.Level != 2 || singer.Variant != "v" || singer.Singer == nil || singer.Singer.Avatar["_"] != "../../missing.png" || singer.Singer.Background["zh-CN"] != "/outside.png" || singer.Singer.DemoAudio != nil || singer.MetadataError != "" {
		t.Fatal("singer extraction changed", singer)
	}
}

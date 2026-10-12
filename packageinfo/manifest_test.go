package packageinfo

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func manifestFS(desc string, extra map[string]string) fstest.MapFS {
	f := fstest.MapFS{"desc.json": &fstest.MapFile{Data: []byte(desc)}}
	for k, v := range extra {
		f[k] = &fstest.MapFile{Data: []byte(v)}
	}
	return f
}
func TestVersionAndSelectors(t *testing.T) {
	for _, s := range []string{"0", "1.2", "9999999999999999999999999999999999999999999999999999.1.0.0"} {
		v, err := ParseVersion(s)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(v.String(), ".") != 3 {
			t.Fatal(v)
		}
	}
	for _, s := range []string{"", "01", "1.01", "+1", "1.2.3.4.5", "1..2", "１"} {
		if _, err := ParseVersion(s); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
	a, _ := ParseVersion("99999999999999999999999999")
	b, _ := ParseVersion("100000000000000000000000000")
	if a.Compare(b) >= 0 {
		t.Fatal("version comparison overflow")
	}
	one, _ := ParseVersion("1")
	same, _ := ParseVersion("1.0.0.0")
	if one.Compare(same) != 0 {
		t.Fatal("normalization")
	}
	s, err := ParseSelector("Vendor/voice=1:com.example.singer/main")
	if err != nil || s.ID != "Vendor/voice" || s.Version.String() != "1.0.0.0" || s.Category != "com.example.singer" {
		t.Fatal(s, err)
	}
	for _, s := range []string{"foo@1", "foo=1:singer/", "foo=1:singer/id/extra", ":singer/id", "foo=1:非/main"} {
		if _, err := ParseSelector(s); err == nil {
			t.Fatal("accepted", s)
		}
	}
}
func TestJSONProfile(t *testing.T) {
	valid := []string{
		`{"url":"https://example.test/path","line":"// literal","block":"/* literal */","escaped":"\"// literal"}`,
		"{\"literal\":\"\ufeff\",\"escaped\":\"\\ufeff\"}",
		`{"x":{"x":1},"y":{"x":2}}`,
	}
	for _, s := range valid {
		if _, err := DecodeJSON([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []string{
		"\ufeff{}", " \ufeff{}", "{}\ufeff",
		"// comment\n{}", "{\"x\":1,// comment\n\"y\":2}", "{}// comment",
		`/* comment */{}`, `{"x":/* comment */1}`, `{"x":[1,/* comment */2]}`, `{}/* comment */`,
		`{"x":1,"x":2}`, `{"x":{"a":1,"a":2}}`, `{"x":1,}`, `{"x":[1,]}`, `{} {}`, `/* unclosed`,
		"{\"x\":\"\xff\"}", strings.Repeat("[", 130) + strings.Repeat("]", 130),
	} {
		if _, err := DecodeJSON([]byte(s)); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}

func TestParsedDeclarationsRejectBOMAndComments(t *testing.T) {
	desc := `{"$version":"1","runtimeLevel":1,"id":"voice","version":"1","contributions":{"singer":[{"id":"main","path":"singer.json"}]}}`
	singer := `{"interface":"example.Singer","level":1,"variant":"v"}`
	for _, invalid := range []struct {
		name   string
		prefix string
		suffix string
	}{
		{"bom", "\ufeff", ""},
		{"line-comment", "// comment\n", ""},
		{"block-comment", "/* comment */", ""},
		{"trailing-line-comment", "", "// comment"},
		{"trailing-block-comment", "", "/* comment */"},
	} {
		for _, file := range []string{"desc.json", "singer.json"} {
			t.Run(file+"/"+invalid.name, func(t *testing.T) {
				files := manifestFS(desc, map[string]string{"singer.json": singer})
				files[file].Data = []byte(invalid.prefix + string(files[file].Data) + invalid.suffix)
				if _, err := Load(files, t.TempDir()); err == nil {
					t.Fatal("accepted invalid declaration JSON")
				}
			})
		}
	}
}
func TestVariablesAndTransparentContributions(t *testing.T) {
	root := t.TempDir()
	desc := `{"$version":"1","runtimeLevel":1,"id":"vendor/voice","version":"2","compatVersion":"1","vars":[{"name":"base","value":"PACKAGE"},{"name":"pinned","value":"${base}"},{"name":"selector","value":"base"},{"name":"d","value":"$"}],"vendor":{"_":"","Odd locale":"V"},"extension":{"kept":"${pinned}"},"contributions":{"com.example.data":[{"id":"a","path":"${root}/data.json"},{"id":"b","path":"data.json"}],"singer":[{"id":"a","path":"data.json"},{"id":"b","path":"data.json"},{"id":"voice","path":"singer.json"}]}}`
	declaration := `{"vars":[{"name":"base","value":"MODULE"}],"name":{"_":"","ZH-cn":"A","zh-CN":"B"},"interface":"com.example.Contract","level":12,"variant":"x","scope":"${base}/${pinned}/${${selector}}","literal":"${d}{base}/$${base}","imports":false,"configuration":{"arbitrary":null}}`
	singer := `{"name":"S","interface":"example.Singer","level":1,"variant":"v","avatar":"../../outside.png","background":25,"demoAudio":["old.wav"],"imports":[{"garbage":true}]}`
	p, err := Load(manifestFS(desc, map[string]string{"data.json": declaration, "singer.json": singer}), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Contributions) != 5 || p.Contributions[0].ID != "a" || p.Contributions[1].ID != "b" {
		t.Fatal("contribution identities", p.Contributions)
	}
	for _, c := range p.Contributions[:2] {
		if c.SingerDeclaration != nil {
			t.Fatal("transparent declaration was parsed", c)
		}
	}
	c := p.Contributions[2]
	if c.Manifest["scope"] != "MODULE/PACKAGE/MODULE" || c.Manifest["literal"] != "${base}/${base}" {
		t.Fatal(c.Manifest)
	}
	if c.Name["_"] != "" || c.Name["ZH-cn"] != "A" || c.Name["zh-CN"] != "B" {
		t.Fatal(c.Name)
	}
	if p.Manifest["extension"].(map[string]any)["kept"] != "PACKAGE" {
		t.Fatal("unknown fields lost")
	}
	if p.Vendor["_"] != "" {
		t.Fatal("empty default changed")
	}
	s := p.Contributions[4]
	if s.Singer.Avatar["_"] != "../../outside.png" || s.Singer.Background != nil || s.Singer.DemoAudio != nil {
		t.Fatal(s.Singer)
	}
}
func TestDirectReferencesOnly(t *testing.T) {
	root := t.TempDir()
	base := `{"$version":"1","runtimeLevel":1,"id":"a","version":"1",%s}`
	for _, field := range []string{`"readme":"../outside"`, `"readme":"missing"`, `"contributions":{"singer":[{"id":"a"}]}`, `"contributions":{"singer":[{"id":"a","path":"../outside.json"}]}`, `"contributions":{"singer":[{"id":"a","path":"missing.json"}]}`, `"contributions":{"singer":[{"id":"a","path":null}]}`, `"contributions":{"inference":[{"id":"a","path":"../outside.bin"}]}`, `"contributions":{"com.example.data":[{"id":"a","path":"missing.bin"}]}`} {
		if _, err := Load(manifestFS(fmt.Sprintf(base, field), nil), root); err == nil {
			t.Fatal("accepted", field)
		}
	}
	desc := fmt.Sprintf(base, `"readme":"docs\\..\\readme.txt","contributions":{"x":[{"id":"a","path":"x.json"}],"singer":[{"id":"s","path":"s.json"}]}`)
	p, err := Load(manifestFS(desc, map[string]string{"readme.txt": "ok", "x.json": `{"outside":"/outside/not/a/file","imports":"bad","vars":42}`, "s.json": `{"avatar":null,"demoAudio":"/outside/not/audio","name":false,"level":-1}`}), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Diagnostics) == 0 {
		t.Fatal("missing extraction diagnostics")
	}
	// Absolute paths inside the logical root remain valid for an archive-backed view.
	d := fmt.Sprintf(base, `"readme":`+quote(filepath.Join(root, "readme.txt")))
	if _, err := Load(manifestFS(d, map[string]string{"readme.txt": "ok"}), root); err != nil {
		t.Fatal(err)
	}
}
func quote(s string) string { b, _ := json.Marshal(s); return string(b) }
func TestPackageValidation(t *testing.T) {
	for _, desc := range []string{`{"$version":"2","runtimeLevel":1,"id":"a","version":"1"}`, `{"$version":"1","id":"a","version":"1"}`, `{"$version":"1","runtimeLevel":2,"id":"a","version":"1"}`, `{"$version":"1","runtimeLevel":1,"id":"a","version":"1","compatVersion":"2"}`, `{"$version":"1","runtimeLevel":1,"id":"a","version":"1","dependencies":[{"id":"a","version":"1"}]}`, `{"$version":"1","runtimeLevel":1,"id":"a","version":"1","dependencies":[{"id":"b","version":"1"},{"id":"b","version":"2"}]}`, `{"$version":"1","runtimeLevel":1,"id":"a","version":"1","vars":[{"name":"root","value":"x"}]}`, `{"$version":"1","runtimeLevel":1,"id":"a","version":"1","contributions":{"x":[{"id":"a"},{"id":"a"}]}}`} {
		if _, err := Load(manifestFS(desc, nil), t.TempDir()); err == nil {
			t.Fatal("accepted", desc)
		}
	}
	if _, err := Load(manifestFS(`{"$version":"1","runtimeLevel":1,"id":"a","version":"1","contributions":{"singer":[{"id":"a","path":"x.json"}]}}`, map[string]string{"x.json": `{"a":1,"a":2}`}), t.TempDir()); err == nil {
		t.Fatal("accepted invalid singer declaration JSON")
	}
}
func TestIntegerNumbersAndUnknownVarsObjects(t *testing.T) {
	for _, level := range []string{"1", "1.0", "1e0", "10e-1"} {
		desc := fmt.Sprintf(`{"$version":"1","runtimeLevel":%s,"id":"a","version":"1","vars":[{"name":"x","value":"expanded"}],"extension":{"vars":{"value":"${x}"}},"contributions":{"singer":[{"id":"a","path":"singer.json"}]}}`, level)
		p, err := Load(manifestFS(desc, map[string]string{"singer.json": `{"interface":"example.Singer","level":1e2,"variant":"v"}`}), t.TempDir())
		if err != nil {
			t.Fatal(level, err)
		}
		if p.Manifest["extension"].(map[string]any)["vars"].(map[string]any)["value"] != "expanded" || p.Contributions[0].Level.String() != "100" {
			t.Fatal(p)
		}
	}
	for _, level := range []string{"0", "-1", "1.1", "1e1000000000"} {
		desc := fmt.Sprintf(`{"$version":"1","runtimeLevel":%s,"id":"a","version":"1"}`, level)
		if _, err := Load(manifestFS(desc, nil), t.TempDir()); err == nil {
			t.Fatal(level)
		}
	}
}
func TestRootedPathsDoNotBecomePackageRelative(t *testing.T) {
	root := t.TempDir()
	f := manifestFS(`{"$version":"1","runtimeLevel":1,"id":"a","version":"1","readme":"/outside"}`, map[string]string{"outside": "inside file with same name"})
	if _, err := Load(f, root); err == nil {
		t.Fatal("rooted external reference became package relative")
	}
	f = manifestFS(`{"$version":"1","runtimeLevel":1,"id":"a","version":"1"}`, nil)
	if _, err := Load(f, root+string([]byte{0xff})); err == nil {
		t.Fatal("unrepresentable root accepted")
	}
}

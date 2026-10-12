package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diffscope/diffscope-package-manager/internal/packagemanager"
)

func TestOpaqueContributionsThroughLocalWorkflow(t *testing.T) {
	scratch := t.TempDir()
	source, root, archive := filepath.Join(scratch, "source"), filepath.Join(scratch, "packages"), filepath.Join(scratch, "voice.dspk")
	files := map[string][]byte{
		"desc.json":        []byte(`{"$version":"1","runtimeLevel":1,"id":"voice","version":"1","vars":[{"name":"folder","value":"data"}],"contributions":{"inference":[{"id":"binary","path":"${folder}/payload.bin"},{"id":"broken","path":"data/broken.json"}],"com.example.data":[{"id":"valid","path":"data/valid.json","name":false,"interface":{}},{"id":"inline","path":{"categoryOwned":true},"level":"opaque"}],"singer":[{"id":"main","path":"singer.json"}]}}`),
		"data/payload.bin": {0xff, 0x00, 0xfe},
		"data/broken.json": []byte(`{"vars":42,"name":"one","name":"two"}`),
		"data/valid.json":  []byte(`{"name":"HiddenDeclarationName","interface":"hidden.Contract","level":9,"variant":"hidden","vars":[{"name":"x","value":"${undefined}"}]}`),
		"singer.json":      []byte(`{"name":"VisibleSinger","interface":"example.Singer","level":2,"variant":"v","avatar":"../../missing.png","demoAudio":"/outside/missing.ogg","imports":false}`),
	}
	for name, data := range files {
		filename := filepath.Join(source, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	invoke := func(args ...string) string {
		t.Helper()
		out, errOut, err := execute(t, append([]string{"--json", "--packages-dir", root}, args...)...)
		if err != nil {
			t.Fatalf("%v: %v\nstdout: %s\nstderr: %s", args, err, out, errOut)
		}
		return out
	}
	info := func(args ...string) packagemanager.Info {
		t.Helper()
		var response struct {
			OK          bool                `json:"ok"`
			Data        packagemanager.Info `json:"data"`
			Diagnostics []string            `json:"diagnostics"`
		}
		out := invoke(append([]string{"info"}, args...)...)
		if err := json.Unmarshal([]byte(out), &response); err != nil || !response.OK || len(response.Diagnostics) != 0 {
			t.Fatalf("unexpected info response: %s, %v", out, err)
		}
		for _, c := range response.Data.Package.Contributions {
			if c.Category == "singer" {
				if c.SingerDeclaration == nil || c.ModuleMetadata == nil || c.Name["_"] != "VisibleSinger" || c.Interface != "example.Singer" || c.Level != "2" || c.Variant != "v" || c.Singer == nil || c.Singer.Avatar["_"] != "../../missing.png" || c.MetadataError != "" {
					t.Fatal("singer metadata lost", c)
				}
				continue
			}
			if c.SingerDeclaration != nil || c.Entry["id"] != c.ID {
				t.Fatal("opaque declaration metadata exposed", c)
			}
		}
		return response.Data
	}
	invoke("pack", source, "--output", archive)
	fileInfo := info("--file", archive)
	if len(fileInfo.Package.Contributions) != 5 {
		t.Fatal("contribution entries lost", fileInfo.Package.Contributions)
	}
	invoke("install", "--file", archive)
	installed := info("--installed", "voice=1")
	if installed.Installation == nil || installed.Installation.Hash != fileInfo.Hash {
		t.Fatal("installed contents differ from archive", installed)
	}
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(root, installed.Installation.Directory, filepath.FromSlash(name)))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatal("resource bytes changed", name, err)
		}
	}
	for _, selector := range []string{"voice=1:inference/binary", "voice=1:com.example.data/valid", "voice=1:com.example.data/inline", "voice=1:singer/main"} {
		if selected := info("--installed", selector); len(selected.Package.Contributions) != 1 {
			t.Fatal("contribution selector lost", selector, selected.Package.Contributions)
		}
	}
	var listing struct {
		Data []packagemanager.InstalledPackage `json:"data"`
	}
	if err := json.Unmarshal([]byte(invoke("list")), &listing); err != nil || len(listing.Data) != 1 || len(listing.Data[0].Contributions) != 5 {
		t.Fatal("installed contribution index lost", listing, err)
	}
	for _, c := range listing.Data[0].Contributions {
		if c.Category != "singer" && (c.Interface != "" || c.Level != "" || c.Variant != "" || len(c.Name) != 0) {
			t.Fatal("opaque declaration indexed", c)
		}
	}
	text, errOut, err := execute(t, "--packages-dir", root, "info", "--installed", "voice=1:inference/binary")
	if err != nil || !strings.Contains(text, "binary -> data/payload.bin") {
		t.Fatal("opaque text identity lost", text, errOut, err)
	}
	for _, field := range []string{"      Name:", "      Interface:", "      Level:", "      Variant:", "expansion failed"} {
		if strings.Contains(text, field) {
			t.Fatalf("opaque text metadata exposed %q: %s", field, text)
		}
	}
	invoke("remove", "voice=1")
}

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

func TestInfoCompleteMetadataFromArchiveAndInstallation(t *testing.T) {
	scratch := t.TempDir()
	source, root, archive := filepath.Join(scratch, "source"), filepath.Join(scratch, "packages"), filepath.Join(scratch, "voice.dspk")
	desc := []byte(`{"$version":"1","runtimeLevel":1,"id":"vendor/voice","version":"1","vars":[{"name":"title","value":"Voice"},{"name":"docs","value":"${root}/docs"}],"name":"${title}","vendor":"Vendor","description":"Description ${title}","copyright":{"_":"","en":"Copyright ${title}","zh-CN":"版权 ${title}"},"readme":{"_":"docs/../docs/readme.txt","zh-CN":"${docs}/说明.txt"},"url":"https://example.test/${title}","contributions":{"singer":[{"id":"main","path":"singer.json"}],"vendor.custom":[{"id":"data","configuration":{"file":"../../unchecked"}}]},"extension":{"text":"${title}","enabled":true}}`)
	for name, content := range map[string][]byte{
		"desc.json": desc, "docs/readme.txt": []byte("Readme"), "docs/说明.txt": []byte("说明"),
		"singer.json": []byte(`{"name":"Singer","avatar":"../../unchecked.png"}`),
	} {
		filename := filepath.Join(source, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	invoke := func(args ...string) string {
		t.Helper()
		out, errOut, err := execute(t, append([]string{"--packages-dir", root}, args...)...)
		if err != nil {
			t.Fatalf("%v: %v\nstdout: %s\nstderr: %s", args, err, out, errOut)
		}
		return out
	}
	readInfo := func(args ...string) packagemanager.Info {
		t.Helper()
		out := invoke(append([]string{"--json", "--language", "zh-CN", "info"}, args...)...)
		var response struct {
			OK   bool                `json:"ok"`
			Data packagemanager.Info `json:"data"`
		}
		if err := json.Unmarshal([]byte(out), &response); err != nil || !response.OK {
			t.Fatalf("invalid info response: %s, %v", out, err)
		}
		return response.Data
	}
	checkMetadata := func(info packagemanager.Info, expectedRoot string) {
		t.Helper()
		p := info.Package
		if p.Name["_"] != "Voice" || p.Vendor["_"] != "Vendor" || p.Description["_"] != "Description Voice" || len(p.Copyright) != 3 || p.Copyright["_"] != "" || p.Copyright["en"] != "Copyright Voice" || p.Copyright["zh-CN"] != "版权 Voice" || p.URL == nil || *p.URL != "https://example.test/Voice" {
			t.Fatalf("incomplete typed metadata: %+v", p)
		}
		if len(p.Readme) != 2 || p.Readme["_"] != filepath.Join(expectedRoot, "docs", "readme.txt") || p.Readme["zh-CN"] != filepath.Join(expectedRoot, "docs", "说明.txt") {
			t.Fatal("readme resolved in the wrong context", p.Readme, expectedRoot)
		}
		if p.Manifest["extension"].(map[string]any)["text"] != "Voice" || p.Manifest["readme"].(map[string]any)["_"] != "docs/../docs/readme.txt" {
			t.Fatal("expanded declaration lost or rewritten", p.Manifest)
		}
		if _, exists := p.Manifest["vars"]; exists {
			t.Fatal("variable definitions leaked into parsed metadata")
		}
	}
	invoke("--json", "pack", source, "--output", archive)
	fileInfo := readInfo("--file", archive)
	checkMetadata(fileInfo, scratch)
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("archive info initialized an installed-package store")
	}
	invoke("--json", "install", "--file", archive)
	installedInfo := readInfo("--installed", "vendor/voice=1")
	if installedInfo.Installation == nil {
		t.Fatal("installed info omitted installation")
	}
	installedRoot := filepath.Join(root, installedInfo.Installation.Directory)
	checkMetadata(installedInfo, installedRoot)
	if installedInfo.Installation.Hash != fileInfo.Hash {
		t.Fatal("metadata extraction changed content hash")
	}
	installedDesc, err := os.ReadFile(filepath.Join(installedRoot, "desc.json"))
	if err != nil || !bytes.Equal(installedDesc, desc) {
		t.Fatal("metadata parsing rewrote desc.json", err)
	}
	for _, args := range [][]string{{"--file", archive}, {"--installed", "vendor/voice=1"}} {
		out := invoke(append([]string{"--language", "*", "info"}, args...)...)
		for _, text := range []string{"Copyright[_]: \n", "Copyright[en]: Copyright Voice", "Copyright[zh-CN]: 版权 Voice", "Readme[_]:", "Readme[zh-CN]:", "URL: https://example.test/Voice", "Avatar[_]: ../../unchecked.png"} {
			if !strings.Contains(out, text) {
				t.Fatalf("info omitted %q:\n%s", text, out)
			}
		}
	}
	var listing struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(invoke("--json", "list")), &listing); err != nil || len(listing.Data) != 1 {
		t.Fatal("invalid installed index response", listing, err)
	}
	for _, field := range []string{"copyright", "readme", "url", "manifest"} {
		if _, exists := listing.Data[0][field]; exists {
			t.Errorf("full metadata leaked into the installed-package index: %s", field)
		}
	}
}

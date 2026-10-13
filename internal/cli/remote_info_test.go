package cli

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/diffscope/diffscope-package-manager/internal/packagemanager"
	"github.com/diffscope/diffscope-package-manager/internal/testutil"
)

func executeCLI(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	if executable := os.Getenv("DSPM_TEST_CLI_BINARY"); executable != "" {
		var out, errOut bytes.Buffer
		child := exec.Command(executable, args...)
		child.Stdout, child.Stderr = &out, &errOut
		err := child.Run()
		return out.String(), errOut.String(), err
	}
	return execute(t, args...)
}

func TestInfoRemoteArchiveAndInstalledParity(t *testing.T) {
	t.Setenv("DSPM_JSON", "false")
	t.Setenv("NO_COLOR", "1")
	root := t.TempDir()
	source, archive := filepath.Join(root, "source"), filepath.Join(root, "voice.dspk")
	const level = "2"
	files := map[string]string{
		"desc.json":   `{"$version":"1","runtimeLevel":1,"id":"vendor/voice","version":"2","compatVersion":"1","name":{"_":"Default voice","en":"Voice","zh-CN":"音源"},"description":{"_":"Default description","en":"","zh-CN":"音源说明"},"vendor":{"_":"Default vendor","en":"Vendor","zh-CN":"发布者"},"license":{"_":"docs/LICENSE.txt","en":"docs/LICENSE.en.txt","zh-CN":"docs/许可 文件.txt"},"readme":{"_":"docs/README.txt","zh-CN":"docs/说明.txt"},"url":"https://example.org/voice?token=secret","dependencies":[{"id":"base","version":"1"}],"contributions":{"singer":[{"id":"main","path":"main.json"},{"id":"other","path":"other.json"}],"vendor.custom":[{"id":"lexicon","path":"lexicon.bin"}]}}`,
		"main.json":   `{"interface":"example.Singer","level":` + level + `,"variant":"example.Neural","name":{"_":"Default singer","en":"Lead","zh-CN":"主音源"},"avatar":"media/avatar.png","background":"media/background.png","demoAudio":"media/demo.wav"}`,
		"other.json":  `{"interface":"example.Singer","level":3,"variant":"example.Other","name":"Other"}`,
		"lexicon.bin": "lexicon", "docs/LICENSE.txt": "License", "docs/LICENSE.en.txt": "License", "docs/许可 文件.txt": "许可", "docs/README.txt": "Readme", "docs/说明.txt": "说明",
	}
	for name, data := range files {
		filename := filepath.Join(source, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	flags := []string{"--packages-dir", filepath.Join(root, "packages"), "--cache-dir", filepath.Join(root, "cache"), "--config", filepath.Join(root, "absent-config.json"), "--progress", "none"}
	invoke := func(name string, args ...string) string {
		t.Helper()
		out, errOut, err := executeCLI(t, append(append([]string{}, flags...), args...)...)
		if directory := os.Getenv("DSPM_TEST_INFO_CAPTURE_DIR"); directory != "" {
			if err := os.MkdirAll(directory, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, strings.ReplaceAll(name, "*", "all")+".txt"), []byte(out), 0644); err != nil {
				t.Fatal(err)
			}
		}
		if err != nil {
			t.Fatalf("%v: %v\nstdout: %s\nstderr: %s", args, err, out, errOut)
		}
		return out
	}
	invoke("pack", "--json", "pack", source, "--output", archive)
	base := testutil.Package(t, "base", "1", "1", nil, nil)
	server := testutil.ServeRepository(t, archive, base)
	flags = append(flags, "--registry", server.Server.URL+"/packages/", "--allow-unrestricted-downloads")
	remote := invoke("remote-en", "--language", "en", "info", "vendor/voice")
	positions := []int{}
	for _, heading := range []string{"Package\n", "Dependencies\n", "Singers\n", "Download\n"} {
		positions = append(positions, strings.Index(remote, heading))
	}
	for i, position := range positions {
		if position < 0 || i > 0 && position <= positions[i-1] {
			t.Fatal("Remote information has inconsistent sections.", positions, remote)
		}
	}
	for _, text := range []string{"  ID: vendor/voice\n", "  Version: 2.0.0.0\n", "  CompatVersion: 1.0.0.0\n", "  Description: \n", "Dependencies\n  base=1.0.0.0", "Singers\n  main\n", "    Level: " + level, "  other\n", "  SHA-512:", "  Size: 2.2 KiB"} {
		if !strings.Contains(remote, text) {
			t.Fatalf("Remote information omitted %q:\n%s", text, remote)
		}
	}
	if licenseAt := strings.Index(remote, "  License:"); licenseAt < positions[0] || licenseAt > positions[1] || strings.Contains(remote, "secret") || strings.Contains(remote, "RuntimeLevel:") || strings.Contains(remote, "Avatar:") {
		t.Fatal("Remote information misplaces documentation, exposes credentials, or fabricates archive metadata.", remote)
	}
	if _, err := os.Stat(flags[1]); !os.IsNotExist(err) {
		t.Fatal("Remote information initialized the installation directory.", err)
	}
	file := invoke("archive-json", "--json", "info", "--file", archive)
	var fileInfo, installedInfo struct {
		OK   bool                `json:"ok"`
		Data packagemanager.Info `json:"data"`
	}
	if err := json.Unmarshal([]byte(file), &fileInfo); err != nil || !fileInfo.OK {
		t.Fatal(file, err)
	}
	invoke("install", "--json", "install", "--file-only", archive, base)
	installed := invoke("installed-json", "--json", "info", "--installed", "vendor/voice=2")
	if err := json.Unmarshal([]byte(installed), &installedInfo); err != nil || !installedInfo.OK || installedInfo.Data.Installation == nil || installedInfo.Data.Installation.Hash != fileInfo.Data.Hash {
		t.Fatal("Archive and installed information disagree.", installed, err)
	}
	for _, info := range []packagemanager.Info{fileInfo.Data, installedInfo.Data} {
		if len(info.Package.Contributions) != 3 {
			t.Fatal("Local information lost contribution categories.", info)
		}
		for _, contribution := range info.Package.Contributions {
			if contribution.Category == "singer" && contribution.ID == "main" && contribution.Level != 2 {
				t.Fatal("Local information changed an integer level.", contribution.Level)
			}
		}
	}
	for _, language := range []string{"en", "zh-CN", "*"} {
		remoteText := invoke("remote-"+language, "--language", language, "info", "vendor/voice=2")
		if strings.Contains(remoteText, "Contributions\n") || strings.Contains(remoteText, "  singer:\n") {
			t.Fatal("Remote singer summaries contain redundant category grouping.", remoteText)
		}
		for _, field := range []string{"Repository\n", "SourceID:", "Priority:", "Revision:", "Root index SHA-512:", "Manifest SHA-512:", "RepositoryFormatVersion:", "Freshness:", "Network policy:", "Repository conformance:", "Address validation:"} {
			if strings.Contains(remoteText, field) {
				t.Fatalf("Remote text information includes query provenance %q:\n%s", field, remoteText)
			}
		}
		for name, args := range map[string][]string{"archive": {"--file", archive}, "installed": {"--installed", "vendor/voice=2"}} {
			local := invoke(name+"-"+language, append([]string{"--language", language, "info"}, args...)...)
			for _, line := range strings.Split(remoteText[:strings.Index(remoteText, "Download\n")], "\n") {
				trimmed := strings.TrimSpace(line)
				if trimmed == "" || strings.HasPrefix(trimmed, "License") || strings.HasPrefix(trimmed, "Readme") || trimmed == "Singers" || trimmed == "main" || trimmed == "other" {
					continue
				}
				if !strings.Contains(local, strings.TrimLeft(line, " \t")+"\n") {
					t.Fatalf("%s information differs from remote information for language %s at %q:\n%s", name, language, line, local)
				}
			}
		}
	}
	for _, selector := range []string{"vendor/voice:singer/main", "vendor/voice=2:singer/other"} {
		selected := invoke("remote-selected-"+strings.Split(selector, "/")[2], "--language", "en", "info", selector)
		other := "other"
		if strings.HasSuffix(selector, "/other") {
			other = "main"
		}
		if strings.Contains(selected, "  "+other+"\n") {
			t.Fatal("A remote contribution selector retained an unselected singer.", selected)
		}
	}
	var remoteInfo struct {
		OK   bool                      `json:"ok"`
		Data packagemanager.RemoteInfo `json:"data"`
	}
	remoteJSON := invoke("remote-json", "--json", "--language", "en", "info", "vendor/voice=2:singer/main")
	if err := json.Unmarshal([]byte(remoteJSON), &remoteInfo); err != nil || !remoteInfo.OK || len(remoteInfo.Data.Package.Singers) != 1 || remoteInfo.Data.Package.Singers[0].ID != "main" || remoteInfo.Data.Package.Singers[0].Level != 2 {
		t.Fatal("Remote structured information lost summaries or selector scope.", remoteJSON, err)
	}
	if strings.Contains(remoteJSON, `"selectedSingers"`) || !strings.Contains(remoteJSON, `"level":2`) {
		t.Fatal("Remote information contains a duplicate selection or a non-integer level.", remoteJSON)
	}
	provenance := remoteInfo.Data
	if provenance.Repository.URL != server.Server.URL+"/packages/" || provenance.Repository.ID == "" || provenance.Repository.Revision == "" || provenance.Repository.RootSha512 == "" || provenance.ManifestSha512 == "" || provenance.NetworkPolicy != "unrestricted" || provenance.RepositoryConformance != "client-url-override" || provenance.AddressValidation != "unrestricted" {
		t.Fatal("Remote JSON information lost query provenance.", remoteJSON)
	}
	for field, paths := range map[string]map[string]string{
		"license": {"_": "docs/LICENSE.txt", "en": "docs/LICENSE.en.txt", "zh-CN": "docs/许可 文件.txt"},
		"readme":  {"_": "docs/README.txt", "zh-CN": "docs/说明.txt"},
	} {
		actual := remoteInfo.Data.Package.License
		if field == "readme" {
			actual = remoteInfo.Data.Package.Readme
		}
		for language, path := range paths {
			if actual[language] != "2.0.0.0/"+path {
				t.Fatal("Remote information modified a publication resource path.", field, actual)
			}
			parsed, err := url.Parse(remoteInfo.Data.AssetURLs[field][language])
			if err != nil || parsed.Path != "/packages/vendor/voice/@assets/2.0.0.0/"+path {
				t.Fatal("A documentation URL has an incorrect encoded path.", parsed, err)
			}
		}
		if len(actual) != len(paths) || len(remoteInfo.Data.AssetURLs[field]) != len(paths) {
			t.Fatal("Remote information lost documentation languages.", field)
		}
	}
	list := invoke("installed-list", "--json", "list")
	var listed struct {
		Data []packagemanager.InstalledPackage `json:"data"`
	}
	if err := json.Unmarshal([]byte(list), &listed); err != nil {
		t.Fatal(list, err)
	}
	foundSummary := false
	for _, p := range listed.Data {
		if p.ID != "vendor/voice" {
			continue
		}
		for _, c := range p.Contributions {
			if c.Category == "singer" && c.ID == "main" {
				foundSummary = true
				if c.Level != 2 {
					t.Fatal("An installed summary changed a singer level.", c)
				}
			}
		}
	}
	if !foundSummary || !strings.Contains(list, `"level":2`) {
		t.Fatal("The installed list omitted an integer singer level.", list)
	}
	selectedJSON := invoke("installed-selected-json", "--json", "info", "--installed", "vendor/voice=2:singer/main")
	var selectedInfo struct {
		Data packagemanager.Info `json:"data"`
	}
	if err := json.Unmarshal([]byte(selectedJSON), &selectedInfo); err != nil || len(selectedInfo.Data.Package.Contributions) != 1 {
		t.Fatal(selectedJSON, err)
	}
	localSinger := selectedInfo.Data.Package.Contributions[0]
	remoteSinger := remoteInfo.Data.Package.Singers[0]
	if localSinger.ID != remoteSinger.ID || localSinger.Level != remoteSinger.Level || localSinger.Interface != remoteSinger.Interface || localSinger.Variant != remoteSinger.Variant || !reflect.DeepEqual(localSinger.Name, remoteSinger.Name) {
		t.Fatal("Local and remote contribution selectors disagree.", localSinger, remoteSinger)
	}
	for _, text := range []string{"RuntimeLevel", "runtimeLevel", "formatVersion", "avatar", "token=secret"} {
		if strings.Contains(remoteJSON, text) {
			t.Fatal("Remote structured information contains unavailable or unredacted metadata.", text, remoteJSON)
		}
	}
	empty := invoke("remote-empty", "info", "base")
	if !strings.Contains(empty, "Dependencies\n  (none)") || !strings.Contains(empty, "Singers\n  (none)") {
		t.Fatal("Empty remote metadata does not identify its scope.", empty)
	}
	for _, selector := range []string{"vendor/voice:singer/missing", "vendor/voice:vendor.custom/lexicon"} {
		out, _, err := executeCLI(t, append(append([]string{}, flags...), "--json", "info", selector)...)
		code := "NOT_FOUND"
		if strings.Contains(selector, "vendor.custom") {
			code = "UNSUPPORTED_REMOTE_CONTRIBUTION"
		}
		if err == nil || !json.Valid([]byte(out)) || !strings.Contains(out, `"code":"`+code+`"`) {
			t.Fatal("A remote contribution error has an incorrect code or envelope.", selector, out, err)
		}
	}
	if server.Count("/archives/vendor/voice/2.0.0.0") != 0 || server.Count("/archives/base/1.0.0.0") != 0 || server.Count("/packages/vendor/voice/@assets/2.0.0.0/docs/LICENSE.txt") != 0 {
		t.Fatal("Information queries downloaded an archive or documentation asset.")
	}
}

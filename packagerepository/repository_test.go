package packagerepository_test

import (
	"context"
	"encoding/json"
	"github.com/diffscope/diffscope-package-manager/internal/testutil"
	"github.com/diffscope/diffscope-package-manager/packagearchive"
	"github.com/diffscope/diffscope-package-manager/packageinfo"
	repo "github.com/diffscope/diffscope-package-manager/packagerepository"
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

func fixture(t *testing.T) repo.RemoteManifest {
	t.Helper()
	f := testutil.Package(t, "foo/bar", "10", "1", nil, nil)
	a, err := packagearchive.Open(f, packagearchive.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	p, err := repo.ProjectFS(context.Background(), a.Files(), repo.ProjectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(f)
	m, _, err := repo.BuildManifest(p, repo.Publication{DownloadURL: "https://example.org/release.dspk", DownloadSha512: repo.Sha512(raw)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSingerLevelIntegerProtocol(t *testing.T) {
	m := fixture(t)
	m.Singers = []repo.SingerSummary{{ID: "main", Name: packageinfo.Text{"_": "Voice"}, Interface: "example.Singer", Level: 2, Variant: "v"}}
	raw, err := repo.MarshalManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err = json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	singer := wire["singers"].([]any)[0].(map[string]any)
	if singer["level"] != float64(2) {
		t.Fatal("A singer level is not a JSON integer.", string(raw))
	}
	for _, invalid := range []any{"2", 0, -1, 2.5, nil} {
		singer["level"] = invalid
		b, err := json.Marshal(wire)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = repo.DecodeManifest(b, repo.ManifestDecodeOptions{}); err == nil {
			t.Fatal("An invalid singer level was accepted.", invalid)
		}
	}
}
func TestStrictWireFormat(t *testing.T) {
	m := fixture(t)
	raw, _ := repo.MarshalManifest(m)
	for _, bad := range []string{strings.Replace(string(raw), `"version": "10.0.0.0"`, `"version": "10"`, 1), strings.Replace(string(raw), `"dependencies": []`, `"dependencies": null`, 1), strings.Replace(string(raw), `"id": "foo/bar"`, `"id": "foo/bar", "\u0069d": "foo/bar"`, 1), strings.Replace(string(raw), `"repositoryFormatVersion": 1`, `"repositoryFormatVersion": 1, "unknown": true`, 1), strings.Replace(string(raw), `"_": "foo/bar"`, `"_": "\ud800"`, 1), "\ufeff" + string(raw)} {
		if _, err := repo.DecodeManifest([]byte(bad), repo.ManifestDecodeOptions{}); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
	m.Name["version"] = "translation"
	raw, _ = repo.MarshalManifest(m)
	if _, err := repo.DecodeManifest(raw, repo.ManifestDecodeOptions{}); err != nil {
		t.Fatal(err)
	}
	m.DownloadURL = "http://127.0.0.1/release"
	raw, _ = repo.Marshal(m)
	if _, err := repo.DecodeManifest(raw, repo.ManifestDecodeOptions{}); err == nil {
		t.Fatal("HTTP publication accepted")
	}
	if _, err := repo.DecodeManifest(raw, repo.ManifestDecodeOptions{DownloadURLMode: repo.ClientUnrestrictedURLs}); err != nil {
		t.Fatal(err)
	}
}
func TestProjectionPortableAssetsAndSinger(t *testing.T) {
	base := `{"$version":"1","runtimeLevel":1,"id":"foo","version":"1","license":{"_":"${root}/LICENSE","zh-CN":"${root}/LICENSE"},"readme":"${root}/README"}`
	files := fstest.MapFS{"desc.json": {Data: []byte(base)}, "LICENSE": {Data: []byte("\ufeffLicense\r\n")}, "README": {Data: []byte("\r\n")}}
	p, err := repo.ProjectFS(context.Background(), files, repo.ProjectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if p.Metadata.License["_"] != "LICENSE" {
		t.Fatal(p)
	}
	m, mapping, err := repo.BuildManifest(p, repo.Publication{DownloadURL: "https://example.org/f", DownloadSha512: strings.Repeat("a", 128)}, repo.AssetLayout{"LICENSE": "shared/LICENSE"})
	if err != nil {
		t.Fatal(err)
	}
	diff, err := repo.CompareMetadata(m, p)
	if err != nil || len(diff) > 0 {
		t.Fatal(diff, err)
	}
	assets := fstest.MapFS{"shared/LICENSE": files["LICENSE"], "1.0.0.0/README": files["README"]}
	diff, err = repo.CompareAssetBytes(context.Background(), mapping, assets)
	if err != nil || len(diff) > 0 {
		t.Fatal(diff, err)
	}
	files["desc.json"].Data = []byte(strings.Replace(base, `"version":"1"`, `"version":"1","description":"in ${root}"`, 1))
	if _, err = repo.ProjectFS(context.Background(), files, repo.ProjectionOptions{}); err == nil || !strings.Contains(err.Error(), "NONPORTABLE") {
		t.Fatal(err)
	}
	f := testutil.Package(t, "voice", "1", "1", nil, map[string]string{"singer.json": `{"name":"bad","interface":"x.y","variant":"v"}`})
	a, err := packagearchive.Open(f, packagearchive.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err = repo.ProjectFS(context.Background(), a.Files(), repo.ProjectionOptions{}); err == nil {
		t.Fatal("missing triple accepted")
	}
}
func TestIndexOrderAndRequirements(t *testing.T) {
	for _, raw := range []string{`{"repositoryFormatVersion":1,"repositoryRevision":"` + strings.Repeat("0", 40) + `","packages":null}`, `{"repositoryFormatVersion":1,"id":"foo","latestVersion":"1.0.0.0","versions":[]}`} {
		var err error
		if strings.Contains(raw, "repositoryRevision") {
			_, err = repo.DecodePackagesIndex([]byte(raw), repo.DecodeLimits{})
		} else {
			_, err = repo.DecodeVersionsIndex([]byte(raw), repo.DecodeLimits{})
		}
		if err == nil {
			t.Fatal("invalid index accepted")
		}
	}
}

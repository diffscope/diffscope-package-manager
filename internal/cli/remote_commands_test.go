package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diffscope/diffscope-package-manager/internal/testutil"
)

func TestRemoteVersionListingAndArchiveExport(t *testing.T) {
	t.Setenv("DSPM_JSON", "false")
	t.Setenv("NO_COLOR", "1")
	base := testutil.Package(t, "base", "2", "1", nil, nil)
	low := testutil.Package(t, "voice", "2", "1", []map[string]string{{"id": "base", "version": "1"}}, nil)
	high := testutil.Package(t, "voice", "10", "1", []map[string]string{{"id": "base", "version": "1"}}, nil)
	server := testutil.ServeRepository(t, base, low, high)
	deprecated := server.Manifests["voice=2.0.0.0"]
	deprecated.Deprecated = true
	deprecated.DeprecationReason = "A newer release is available."
	server.Manifests[deprecated.String()] = deprecated
	server.Reindex(t)
	parent := t.TempDir()
	packages := filepath.Join(parent, "packages")
	destination := filepath.Join(parent, "exports")
	flags := []string{"--packages-dir", packages, "--cache-dir", filepath.Join(parent, "cache"), "--config", filepath.Join(parent, "absent.json"), "--registry", server.Server.URL + "/packages/", "--allow-unrestricted-downloads", "--progress", "plain"}
	invoke := func(name string, args ...string) string {
		t.Helper()
		out, errOut, err := executeCLI(t, append(append([]string{}, flags...), args...)...)
		if err != nil {
			t.Fatal(name, err, out, errOut)
		}
		if capture := os.Getenv("DSPM_TEST_INSTALL_CAPTURE_DIR"); capture != "" {
			if err = os.MkdirAll(capture, 0755); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(capture, name+".stdout.txt"), []byte(out), 0644); err != nil {
				t.Fatal(err)
			}
		}
		return out
	}
	versions := invoke("versions-text", "info", "--versions", "voice")
	if !strings.Contains(versions, "voice=2.0.0.0 [deprecated]") || strings.Index(versions, "voice=10.0.0.0") > strings.Index(versions, "voice=2.0.0.0") {
		t.Fatal(versions)
	}
	if server.Count("/packages/voice/@versions/10.0.0.0.json") != 0 {
		t.Fatal("Version listing fetched an individual manifest.")
	}
	invoke("versions-json", "--json", "info", "--versions", "voice")
	invoke("remote-info-json", "--json", "info", "voice")
	dry := invoke("download-dry-json", "--json", "download", "--dry-run", "voice", "--output", destination)
	if !strings.Contains(dry, `"resolution_plan"`) || server.Count("/archives/voice/10.0.0.0") != 0 {
		t.Fatal(dry)
	}
	text := invoke("download-text", "download", "voice", "--output", destination)
	for _, required := range []string{"Download plan", "voice=10.0.0.0 [download]", "base=2.0.0.0 [download, dependency]", "Download size:", "Archive export is complete (exported: 2, skipped: 0).", filepath.Join(destination, "voice", "10.0.0.0.dspk")} {
		if !strings.Contains(text, required) {
			t.Fatal(required, text)
		}
	}
	if strings.Contains(text, "Installing a package") || strings.Contains(text, "Network policy") {
		t.Fatal(text)
	}
	invoke("download-offline-json", "--json", "--offline", "download", "voice", "--output", destination)
	invoke("versions-offline-json", "--json", "--offline", "info", "--versions", "voice")
	if _, err := os.Stat(packages); !os.IsNotExist(err) {
		t.Fatal("Archive export accessed or initialized the store.", err)
	}
	if server.Count("/archives/voice/10.0.0.0") != 1 || server.Count("/archives/base/2.0.0.0") != 1 {
		t.Fatal("Offline export issued archive requests.")
	}
}

func TestDownloadRetryFlagAndUsefulError(t *testing.T) {
	t.Setenv("DSPM_JSON", "false")
	file := testutil.Package(t, "voice", "1", "1", nil, nil)
	for _, retries := range []string{"0", "2"} {
		t.Run(retries, func(t *testing.T) {
			server := testutil.ServeRepository(t, file)
			server.ArchiveHandler = func(w http.ResponseWriter, r *http.Request, data []byte) { w.WriteHeader(503) }
			parent := t.TempDir()
			out, errOut, err := executeCLI(t, "--packages-dir", filepath.Join(parent, "packages"), "--cache-dir", filepath.Join(parent, "cache"), "--config", filepath.Join(parent, "absent.json"), "--registry", server.Server.URL+"/packages/", "--allow-unrestricted-downloads", "--download-retries", retries, "--download-timeout", "1m", "download", "voice", "--output", filepath.Join(parent, "exports"))
			if err == nil || !strings.Contains(errOut, "voice=1.0.0.0") || !strings.Contains(errOut, "503") {
				t.Fatal(err, out, errOut)
			}
			want := 1
			if retries == "2" {
				want = 3
			}
			if count := server.Count("/archives/voice/1.0.0.0"); count != want {
				t.Fatal(count, want)
			}
		})
	}
}

func TestInvalidRemoteNetworkParameters(t *testing.T) {
	for _, args := range [][]string{{"--download-retries", "-1"}, {"--download-timeout", "0s"}} {
		parent := t.TempDir()
		flags := []string{"--packages-dir", filepath.Join(parent, "packages"), "--cache-dir", filepath.Join(parent, "cache"), "--config", filepath.Join(parent, "absent.json"), "--registry", "https://example.invalid/packages/", "--json"}
		flags = append(flags, args...)
		flags = append(flags, "download", "voice")
		out, _, err := executeCLI(t, flags...)
		var result struct{ Error struct{ Code string } }
		if err == nil || json.Unmarshal([]byte(out), &result) != nil || result.Error.Code != "INVALID_ARGUMENT" {
			t.Fatal(out, err)
		}
	}
}

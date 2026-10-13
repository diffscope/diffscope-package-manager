package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/diffscope/diffscope-package-manager/internal/packagemanager"
	"github.com/diffscope/diffscope-package-manager/internal/testutil"
)

func TestInstallPlanAndSummary(t *testing.T) {
	t.Setenv("DSPM_JSON", "false")
	t.Setenv("NO_COLOR", "1")
	base := testutil.Package(t, "base", "2", "1", nil, nil)
	middle := testutil.Package(t, "middle", "1", "1", []map[string]string{{"id": "base", "version": "1"}}, nil)
	voice := testutil.Package(t, "voice", "1", "1", []map[string]string{{"id": "middle", "version": "1"}, {"id": "base", "version": "1"}}, nil)
	unrelated := testutil.Package(t, "unrelated", "1", "1", []map[string]string{{"id": "base", "version": "1"}}, nil)
	server := testutil.ServeRepository(t, base, middle, voice)
	root := t.TempDir()
	flags := []string{"--packages-dir", filepath.Join(root, "packages"), "--cache-dir", filepath.Join(root, "cache"), "--config", filepath.Join(root, "absent-config.json"), "--registry", server.Server.URL + "/packages/", "--allow-unrestricted-downloads", "--progress", "plain"}
	invoke := func(name string, args ...string) string {
		t.Helper()
		out, errOut, err := executeCLI(t, append(append([]string{}, flags...), args...)...)
		if directory := os.Getenv("DSPM_TEST_INSTALL_CAPTURE_DIR"); directory != "" {
			if err := os.MkdirAll(directory, 0755); err != nil {
				t.Fatal(err)
			}
			for suffix, text := range map[string]string{".stdout.txt": out, ".stderr.txt": errOut} {
				if err := os.WriteFile(filepath.Join(directory, name+suffix), []byte(text), 0644); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err != nil {
			t.Fatal(name, err, out, errOut)
		}
		return out
	}
	invoke("seed", "install", "--file-only", base, unrelated)
	checkPlan := func(out, rootAction string) {
		t.Helper()
		for _, text := range []string{"Installation plan", "voice=1.0.0.0", "middle=1.0.0.0", "base=2.0.0.0", "Installed dependencies:", rootAction} {
			if !strings.Contains(out, text) {
				t.Fatalf("The plan omits %q:\n%s", text, out)
			}
		}
		if strings.Contains(out, "unrelated") || strings.Contains(out, "Network policy") || strings.Contains(out, "commit committed") || strings.Count(out, "Installation plan") != 1 {
			t.Fatal("The installation output includes unrelated or duplicate information.", out)
		}
	}
	dry := invoke("remote-dry", "install", "--dry-run", "voice")
	checkPlan(dry, "Installations:")
	if !strings.Contains(dry, "voice=1.0.0.0 [download]") || !strings.Contains(dry, "middle=1.0.0.0 [download, dependency]") {
		t.Fatal("The dry run plan does not identify downloads and dependencies.", dry)
	}
	if !strings.Contains(dry, "Archive validation is pending (archives: 2). No packages were installed.") || strings.Contains(dry, "[installed]") || server.Count("/archives/voice/1.0.0.0") != 0 || server.Count("/archives/middle/1.0.0.0") != 0 {
		t.Fatal("The remote dry run performed acquisition or omitted pending checks.", dry)
	}
	normal := invoke("remote-install", "install", "voice")
	checkPlan(normal, "Installations:")
	if strings.Count(normal, "Summary:") != 1 || !strings.Contains(normal, "Installation is complete (installed: 2, overwritten: 0, skipped: 0).") || strings.Contains(normal, "Inspecting ") || !strings.Contains(normal, "Download size:") {
		t.Fatal("The installation plan or final counts are invalid.", normal)
	}
	if len(regexp.MustCompile(`\[hash=[0-9a-f]{8}…\]`).FindAllString(normal, -1)) != 2 || regexp.MustCompile(`[0-9a-f]{128}\]`).MatchString(normal) {
		t.Fatal("Installation result digests are not abbreviated.", normal)
	}
	repeated := invoke("remote-skip", "install", "voice")
	checkPlan(repeated, "Archive checks:")
	if !strings.Contains(repeated, "voice=1.0.0.0 [cache]") {
		t.Fatal("The repeated installation plan omits cache reuse.", repeated)
	}
	if !strings.Contains(repeated, "Installation is complete (installed: 0, overwritten: 0, skipped: 1).") || server.Count("/archives/voice/1.0.0.0") != 1 || server.Count("/archives/middle/1.0.0.0") != 1 {
		t.Fatal("The repeated installation did not reuse installed dependencies and cached archives.", repeated)
	}
	changed := testutil.Package(t, "voice", "1", "1", []map[string]string{{"id": "middle", "version": "1"}, {"id": "base", "version": "1"}}, map[string]string{"extra.txt": "replacement"})
	fresh := testutil.Package(t, "fresh", "1", "1", nil, nil)
	localDry := invoke("local-dry", "install", "--file-only", "--dry-run", "--overwrite-existing", changed, fresh, base)
	for _, text := range []string{changed, fresh, base, "Overwrites:", "Installations:", "Skips:", "The plan is complete (installations: 1, overwrites: 1, skips: 1)."} {
		if !strings.Contains(localDry, text) {
			t.Fatalf("The local dry run omits %q:\n%s", text, localDry)
		}
	}
	info := invoke("installed-json", "--json", "info", "--installed", "voice")
	var installed struct {
		Data struct {
			Installation struct{ Hash string }
		}
	}
	if err := json.Unmarshal([]byte(info), &installed); err != nil || len(installed.Data.Installation.Hash) != 128 {
		t.Fatal("The installed JSON digest is incomplete.", info, err)
	}
	if !strings.Contains(normal, "[hash="+installed.Data.Installation.Hash[:8]+"…]") {
		t.Fatal("The displayed digest differs from the stored digest.", normal, info)
	}
	localInstall := invoke("local-overwrite", "install", "--file-only", "--overwrite-existing", changed, fresh, base)
	if !strings.Contains(localInstall, "Installation is complete (installed: 1, overwritten: 1, skipped: 1).") || strings.Count(localInstall, "Summary:") != 1 {
		t.Fatal("The local installation counts differ from the applied actions.", localInstall)
	}
	localSkip := invoke("local-skip", "install", "--file-only", changed, fresh, base)
	if !strings.Contains(localSkip, "Installation is complete (installed: 0, overwritten: 0, skipped: 3).") {
		t.Fatal("The local skip summary is invalid.", localSkip)
	}
	jsonDry := invoke("remote-dry-json", "--json", "install", "--dry-run", "voice")
	foundPlan := false
	for _, line := range strings.Split(strings.TrimSpace(jsonDry), "\n") {
		var event struct {
			Event string
			Data  json.RawMessage
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		if event.Event == "resolution_plan" {
			foundPlan = true
			var plan packagemanager.ResolutionPlan
			if err := json.Unmarshal(event.Data, &plan); err != nil {
				t.Fatal(err)
			}
			for _, p := range plan.Packages {
				want := "keep"
				wantArchive := "none"
				if p.ID == "voice" {
					want = "check"
					wantArchive = "cache"
				}
				if p.Action != want || p.ArchiveSource != wantArchive || p.SourceKind == "remote" && len(p.DownloadSha512) != 128 {
					t.Fatal("The structured plan has an incorrect action or digest.", p)
				}
			}
			if len(plan.Roots) != 1 || len(plan.EffectiveEdges) != 4 {
				t.Fatal("The structured plan lost roots or dependency edges.", plan)
			}
		}
	}
	if !foundPlan {
		t.Fatal("The structured dry run omitted its installation plan.", jsonDry)
	}
}

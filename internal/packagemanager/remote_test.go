package packagemanager

import (
	"context"
	"github.com/diffscope/diffscope-package-manager/internal/config"
	"github.com/diffscope/diffscope-package-manager/internal/downloadcache"
	"github.com/diffscope/diffscope-package-manager/internal/testutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func remoteSettings(t *testing.T, urls ...string) config.Remote {
	t.Helper()
	r := config.Remote{DownloadConcurrency: 4, MaxDownloadBytes: 64 << 20, CacheDir: filepath.Join(t.TempDir(), "cache")}
	for _, u := range urls {
		r.Registries = append(r.Registries, config.Registry{URL: u + "/packages/"})
	}
	return r
}
func TestRemoteDependencyBarrierCacheAndInfo(t *testing.T) {
	ctx := context.Background()
	base := testutil.Package(t, "base", "2", "1", nil, nil)
	voice := testutil.Package(t, "foo/bar", "1", "1", []map[string]string{{"id": "base", "version": "1"}}, nil)
	server := testutil.ServeRepository(t, base, voice)
	settings := remoteSettings(t, server.Server.URL)
	manager := New(filepath.Join(t.TempDir(), "packages"))
	info, _, err := InfoRemote(ctx, "foo/bar", settings, true)
	if err != nil || info.Package.ID != "foo/bar" {
		t.Fatal(info, err)
	}
	if server.Count("/archives/foo/bar/1.0.0.0") != 0 {
		t.Fatal("info downloaded archive")
	}
	dry := RemoteRequest{Targets: []string{"foo/bar"}, Settings: settings, Unrestricted: true, DryRun: true}
	if _, err = manager.InstallRemote(ctx, dry, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(manager.Root); !os.IsNotExist(err) {
		t.Fatal("dry-run initialized database")
	}
	dry.DryRun = false
	commits := 0
	plans := 0
	result, err := manager.InstallRemote(ctx, dry, func(e Event) {
		if e.Event == "resolution_plan" {
			plans++
			plan := e.Data.(ResolutionPlan)
			if len(plan.Packages) != 2 || server.Count("/archives/base/2.0.0.0") != 0 || server.Count("/archives/foo/bar/1.0.0.0") != 0 {
				t.Error("The complete installation plan did not precede archive downloads.")
			}
			for _, p := range plan.Packages {
				if p.ArchiveSource != "download" {
					t.Error("The plan does not identify a required download.", p)
				}
			}
		}
		if e.Event == "progress" {
			if p, ok := e.Data.(Progress); ok && p.Phase == "commit" && p.State == "done" {
				commits++
			}
		}
	})
	if err != nil || len(result.Actions) != 2 || commits != 1 || plans != 1 {
		t.Fatal(result, commits, plans, err)
	}
	if server.Count("/archives/base/2.0.0.0") != 1 {
		t.Fatal("dependency not downloaded")
	}
	if _, err = manager.InstallRemote(ctx, dry, nil); err != nil {
		t.Fatal(err)
	}
	if server.Count("/archives/foo/bar/1.0.0.0") != 1 {
		t.Fatal("cache hit made archive GET")
	}
}

func TestRemotePlanCacheAndLocalArchiveSources(t *testing.T) {
	ctx := context.Background()
	base := testutil.Package(t, "base", "1", "1", nil, nil)
	voice := testutil.Package(t, "voice", "1", "1", []map[string]string{{"id": "base", "version": "1"}}, nil)
	server := testutil.ServeRepository(t, base)
	settings := remoteSettings(t, server.Server.URL)
	manager := New(filepath.Join(t.TempDir(), "packages"))
	request := RemoteRequest{Targets: []string{"base"}, Settings: settings, Unrestricted: true}
	if _, err := manager.InstallRemote(ctx, request, nil); err != nil {
		t.Fatal(err)
	}
	archivePath := "/archives/base/1.0.0.0"
	for _, corrupt := range []bool{false, true} {
		if corrupt {
			cache, err := downloadcache.Open(ctx, settings.CacheDir, manager.Root, false)
			if err != nil {
				t.Fatal(err)
			}
			for _, manifest := range server.Manifests {
				blob, _, _, err := cache.Paths(manifest.DownloadSha512)
				if err != nil {
					t.Fatal(err)
				}
				if err := cache.Root.WriteFile(blob, []byte("invalid archive"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			cache.Close()
		}
		manager = New(filepath.Join(t.TempDir(), "packages"))
		request.Targets, request.File = []string{voice}, true
		announced := false
		before := server.Count(archivePath)
		_, err := manager.InstallRemote(ctx, request, func(e Event) {
			if e.Event != "resolution_plan" {
				return
			}
			announced = true
			plan := e.Data.(ResolutionPlan)
			if len(plan.Packages) != 2 || server.Count(archivePath) != before {
				t.Error("The cache and local archive plan did not precede acquisition.")
			}
			for _, p := range plan.Packages {
				want := "cache"
				if p.ID == "voice" {
					want = "local"
				}
				if p.ArchiveSource != want {
					t.Error("The archive source is incorrect.", p)
				}
			}
		})
		wantRequests := before
		if corrupt {
			wantRequests++
		}
		if err != nil || !announced || server.Count(archivePath) != wantRequests {
			t.Fatal("Cache reuse or invalid cache recovery failed.", err, server.Count(archivePath), wantRequests)
		}
	}
}
func TestRemoteFailureHasNoPartialInstallation(t *testing.T) {
	base := testutil.Package(t, "base", "1", "1", nil, nil)
	voice := testutil.Package(t, "voice", "1", "1", []map[string]string{{"id": "base", "version": "1"}}, nil)
	server := testutil.ServeRepository(t, base, voice)
	server.ArchiveHandler = func(w http.ResponseWriter, r *http.Request, b []byte) {
		if strings.Contains(r.URL.Path, "/base/") {
			http.Error(w, "not found", 404)
			return
		}
		w.Write(b)
	}
	manager := New(filepath.Join(t.TempDir(), "packages"))
	_, err := manager.InstallRemote(context.Background(), RemoteRequest{Targets: []string{"voice"}, Settings: remoteSettings(t, server.Server.URL), Unrestricted: true}, nil)
	if err == nil {
		t.Fatal("failed archive accepted")
	}
	list, _, e := manager.List(context.Background())
	if e != nil || len(list) != 0 {
		t.Fatal(list, e)
	}
	if _, e = os.Stat(filepath.Join(manager.Root, ".dspm", "transactions")); !os.IsNotExist(e) {
		t.Fatal("entered staging before barrier")
	}
}
func TestLocalAndInstalledDependenciesAvoidRegistry(t *testing.T) {
	base := testutil.Package(t, "base", "1", "1", nil, nil)
	voice := testutil.Package(t, "voice", "1", "1", []map[string]string{{"id": "base", "version": "1"}}, nil)
	m := New(filepath.Join(t.TempDir(), "packages"))
	settings := remoteSettings(t, "http://127.0.0.1:1")
	if _, err := m.InstallRemote(context.Background(), RemoteRequest{Targets: []string{voice, base}, File: true, Settings: settings}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(settings.CacheDir); !os.IsNotExist(err) {
		t.Fatal("local path touched download cache")
	}
	if _, err := m.InstallRemote(context.Background(), RemoteRequest{Targets: []string{voice}, File: true, Settings: settings}, nil); err != nil {
		t.Fatal(err)
	}
}
func TestOrderedSourceAndDownloadFailover(t *testing.T) {
	f := testutil.Package(t, "foo", "1", "1", nil, nil)
	first := testutil.ServeRepository(t, f)
	second := testutil.ServeRepository(t, f)
	first.ArchiveHandler = func(w http.ResponseWriter, r *http.Request, b []byte) { http.Error(w, "missing", 404) }
	settings := remoteSettings(t, first.Server.URL, second.Server.URL)
	m := New(filepath.Join(t.TempDir(), "packages"))
	if _, err := m.InstallRemote(context.Background(), RemoteRequest{Targets: []string{"foo"}, Settings: settings, Unrestricted: true}, nil); err != nil {
		t.Fatal(err)
	}
	if first.Count("/archives/foo/1.0.0.0") != 1 || second.Count("/archives/foo/1.0.0.0") != 1 {
		t.Fatal("unexpected failover attempts")
	}
}

func TestDeprecatedRemoteDiagnosticsAndDuplicateResolvedRoots(t *testing.T) {
	f := testutil.Package(t, "foo", "1", "1", nil, nil)
	server := testutil.ServeRepository(t, f)
	for key, m := range server.Manifests {
		m.Deprecated = true
		m.DeprecationReason = "use a newer release"
		server.Manifests[key] = m
	}
	server.Reindex(t)
	settings := remoteSettings(t, server.Server.URL)
	info, diagnostics, err := InfoRemote(context.Background(), "foo", settings, true)
	if err != nil || len(diagnostics) != 1 || len(info.DiagnosticDetails) != 1 || info.DiagnosticDetails[0].Code != "PACKAGE_DEPRECATED" {
		t.Fatal(info, diagnostics, err)
	}
	m := New(filepath.Join(t.TempDir(), "packages"))
	_, err = m.InstallRemote(context.Background(), RemoteRequest{Targets: []string{"foo", "foo=1"}, Settings: settings, Unrestricted: true}, nil)
	if err == nil {
		t.Fatal("duplicate resolved identity accepted")
	}
	if server.Count("/archives/foo/1.0.0.0") != 0 {
		t.Fatal("duplicate roots downloaded")
	}
}

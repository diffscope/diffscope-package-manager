package packagemanager

import (
	"context"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/diffscope/diffscope-package-manager/internal/testutil"
)

func TestFinalAcquisitionSetExcludesUnusedCandidates(t *testing.T) {
	a := testutil.Package(t, "app-a", "1", "1", []map[string]string{{"id": "shared", "version": "1"}}, nil)
	c := testutil.Package(t, "app-c", "1", "1", []map[string]string{{"id": "shared", "version": "2"}}, nil)
	low := testutil.Package(t, "shared", "1", "1", nil, nil)
	high := testutil.Package(t, "shared", "3", "1", nil, nil)
	first := testutil.ServeRepository(t, a, c, low)
	second := testutil.ServeRepository(t, high)
	m := New(filepath.Join(t.TempDir(), "packages"))
	unused := []string{}
	result, err := m.InstallRemote(context.Background(), RemoteRequest{Targets: []string{"app-a", "app-c"}, Settings: remoteSettings(t, first.Server.URL, second.Server.URL), Unrestricted: true}, func(e Event) {
		if e.Event != "resolution_plan" {
			return
		}
		plan := e.Data.(ResolutionPlan)
		for _, decision := range plan.AcquisitionDecisions {
			if decision.Required.ID == "shared" && decision.Provider.Version.String() != "3.0.0.0" {
				t.Error("The acquisition decision differs from the final provider.", decision)
			}
		}
		reachable := map[string]bool{}
		queue := []string{}
		for _, root := range plan.Roots {
			reachable[root.String()] = true
			queue = append(queue, root.String())
		}
		for i := 0; i < len(queue); i++ {
			for _, edge := range plan.EffectiveEdges {
				if edge.Requester.String() == queue[i] && !reachable[edge.Provider.String()] {
					reachable[edge.Provider.String()] = true
					queue = append(queue, edge.Provider.String())
				}
			}
		}
		for _, p := range plan.Packages {
			if p.SourceKind == "remote" && !reachable[p.String()] {
				unused = append(unused, p.String())
			}
		}
	})
	if err != nil || len(unused) != 0 || first.Count("/archives/shared/1.0.0.0") != 0 || len(result.Actions) != 3 {
		t.Fatal(err, unused, result.Actions)
	}
}

func TestOfflineCachedInformationAndInstall(t *testing.T) {
	f := testutil.Package(t, "voice", "1", "1", nil, nil)
	server := testutil.ServeRepository(t, f)
	settings := remoteSettings(t, server.Server.URL)
	m := New(filepath.Join(t.TempDir(), "packages"))
	if _, err := m.InstallRemote(context.Background(), RemoteRequest{Targets: []string{"voice"}, Settings: settings, Unrestricted: true}, nil); err != nil {
		t.Fatal(err)
	}
	server.Server.Close()
	settings.Offline = true
	_, _, err := InfoRemote(context.Background(), "voice=1", settings, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.InstallRemote(context.Background(), RemoteRequest{Targets: []string{"voice=1"}, Settings: settings, Unrestricted: true}, nil); err != nil {
		t.Fatal(err)
	}
	if versions, err := VersionsRemote(context.Background(), "voice", settings, true); err != nil || len(versions.Versions) != 1 {
		t.Fatal(versions, err)
	}
}

func TestConfiguredSingleHostConcurrency(t *testing.T) {
	files := []string{}
	ids := []string{"a", "b", "c", "d"}
	for _, id := range ids {
		files = append(files, testutil.Package(t, id, "1", "1", nil, nil))
	}
	server := testutil.ServeRepository(t, files...)
	var active, peak atomic.Int32
	server.ArchiveHandler = func(w http.ResponseWriter, r *http.Request, data []byte) {
		current := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); current > old; old = peak.Load() {
			if peak.CompareAndSwap(old, current) {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
		w.Write(data)
	}
	m := New(filepath.Join(t.TempDir(), "packages"))
	settings := remoteSettings(t, server.Server.URL)
	_, err := m.InstallRemote(context.Background(), RemoteRequest{Targets: ids, Settings: settings, Unrestricted: true}, nil)
	if err != nil || peak.Load() != 4 {
		t.Fatal(err, peak.Load())
	}
}

package packagemanager

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/diffscope/diffscope-package-manager/internal/testutil"
)

func TestRequiredVersionsRemainReachable(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		name := "disjoint-compatibility"
		highCompat := "2"
		if explicit {
			name, highCompat = "explicit-roots", "1"
		}
		t.Run(name, func(t *testing.T) {
			a := testutil.Package(t, "app-a", "1", "1", []map[string]string{{"id": "shared", "version": "1"}}, nil)
			c := testutil.Package(t, "app-c", "1", "1", []map[string]string{{"id": "shared", "version": "2"}}, nil)
			low := testutil.Package(t, "shared", "1", "1", nil, nil)
			high := testutil.Package(t, "shared", "3", highCompat, nil, nil)
			first := testutil.ServeRepository(t, a, c, low)
			second := testutil.ServeRepository(t, high)
			targets := []string{"app-a", "app-c"}
			if explicit {
				targets = append(targets, "shared=1", "shared=3")
			}
			m := New(filepath.Join(t.TempDir(), "packages"))
			reachable := map[string]bool{}
			result, err := m.InstallRemote(context.Background(), RemoteRequest{Targets: targets, Settings: remoteSettings(t, first.Server.URL, second.Server.URL), Unrestricted: true}, func(e Event) {
				if e.Event != "resolution_plan" {
					return
				}
				plan := e.Data.(ResolutionPlan)
				queue := []string{}
				for _, root := range plan.Roots {
					reachable[root.String()] = true
					queue = append(queue, root.String())
				}
				for i := 0; i < len(queue); i++ {
					for _, edge := range plan.EffectiveEdges {
						key := edge.Provider.String()
						if edge.Requester.String() == queue[i] && !reachable[key] {
							reachable[key] = true
							queue = append(queue, key)
						}
					}
				}
			})
			if err != nil || len(result.Actions) != 4 || !reachable["shared=1.0.0.0"] || !reachable["shared=3.0.0.0"] {
				t.Fatal("A required version is absent from the operation closure.", err, result.Actions, reachable)
			}
		})
	}
}

func TestFinalAcquisitionSetPreservesExistingVersions(t *testing.T) {
	ctx := context.Background()
	old := testutil.Package(t, "shared", "1", "1", nil, nil)
	existing := testutil.Package(t, "existing", "1", "1", []map[string]string{{"id": "shared", "version": "1"}}, nil)
	high := testutil.Package(t, "shared", "3", "1", nil, nil)
	app := testutil.Package(t, "app", "1", "1", []map[string]string{{"id": "shared", "version": "2"}}, nil)
	server := testutil.ServeRepository(t, old, high, app)
	manager := New(filepath.Join(t.TempDir(), "packages"))
	if _, err := manager.Install(ctx, InstallRequest{Files: []string{old, existing}}, nil); err != nil {
		t.Fatal(err)
	}
	result, err := manager.InstallRemote(ctx, RemoteRequest{Targets: []string{"app"}, Settings: remoteSettings(t, server.Server.URL), Unrestricted: true}, nil)
	if err != nil || len(result.Actions) != 2 {
		t.Fatal(result, err)
	}
	records, _, err := manager.List(ctx)
	if err != nil || len(records) != 4 {
		t.Fatal(records, err)
	}
	found := false
	for _, record := range records {
		if record.String() == "shared=1.0.0.0" {
			found = true
		}
	}
	if !found {
		t.Fatal("An existing version was removed during acquisition pruning.")
	}
}

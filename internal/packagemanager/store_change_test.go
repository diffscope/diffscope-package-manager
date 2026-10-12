package packagemanager

import (
	"context"
	"github.com/diffscope/diffscope-package-manager/internal/download"
	"github.com/diffscope/diffscope-package-manager/internal/testutil"
	"path/filepath"
	"testing"
)

func TestStoreChangeReplansWithoutHoldingNetworkLock(t *testing.T) {
	ctx := context.Background()
	base := testutil.Package(t, "base", "1", "1", nil, nil)
	voice := testutil.Package(t, "voice", "1", "1", []map[string]string{{"id": "base", "version": "1"}}, nil)
	server := testutil.ServeRepository(t, base, voice)
	m := New(filepath.Join(t.TempDir(), "packages"))
	if _, err := m.Install(ctx, InstallRequest{Files: []string{base}}, nil); err != nil {
		t.Fatal(err)
	}
	changed := false
	updates := 0
	sequence := int64(0)
	terminals := map[string]int{}
	_, err := m.InstallRemote(ctx, RemoteRequest{Targets: []string{"voice"}, Settings: remoteSettings(t, server.Server.URL), Unrestricted: true}, func(e Event) {
		if p, ok := e.Data.(download.Progress); ok {
			if p.Sequence <= sequence {
				t.Errorf("non-increasing operation sequence %d after %d", p.Sequence, sequence)
			}
			sequence = p.Sequence
			if p.State == "done" || p.State == "failed" || p.State == "cancelled" {
				terminals[p.TaskID]++
			}
		}
		if e.Event == "resolution_plan" && !changed {
			changed = true
			if _, err := m.Remove(ctx, RemoveRequest{Targets: []string{"base"}}, nil); err != nil {
				t.Fatal(err)
			}
		}
		if e.Event == "plan_updated" {
			updates++
		}
	})
	if err != nil || updates == 0 {
		t.Fatal(updates, err)
	}
	for task, count := range terminals {
		if count != 1 {
			t.Fatalf("task %s has %d terminal events", task, count)
		}
	}
	all, _, err := m.List(ctx)
	if err != nil || len(all) != 2 {
		t.Fatal(all, err)
	}
}

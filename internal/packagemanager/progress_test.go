package packagemanager

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diffscope/diffscope-package-manager/internal/testutil"
)

func TestInstallProgressLifecycleAndCancellation(t *testing.T) {
	archive := testutil.Package(t, "progress", "1", "1", nil, map[string]string{"resource.bin": strings.Repeat("x", 2<<20)})
	m := New(filepath.Join(t.TempDir(), "packages"))
	ctx, cancel := context.WithCancel(context.Background())
	var events []Event
	_, err := m.Install(ctx, InstallRequest{Files: []string{archive}}, func(e Event) {
		events = append(events, e)
		if p, ok := e.Data.(Progress); ok && p.Phase == "extract" && p.Current > 128<<10 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Event == "result" || e.Event == "summary" {
			t.Fatal("cancelled operation reported success", e)
		}
		if p, ok := e.Data.(Progress); ok && p.Phase == "extract" && p.State == "done" {
			t.Fatal("cancelled extraction completed")
		}
	}
	installed, _, err := m.List(context.Background())
	if err != nil || len(installed) != 0 {
		t.Fatal("partial install persisted", installed, err)
	}
	for _, request := range []InstallRequest{{Files: []string{archive}, DryRun: true}, {Files: []string{archive}}, {Files: []string{archive}}} {
		events = nil
		result, err := m.Install(context.Background(), request, func(e Event) { events = append(events, e) })
		if err != nil {
			t.Fatal(err)
		}
		active := ""
		seen := map[string]bool{}
		for _, e := range events {
			if p, ok := e.Data.(Progress); ok {
				if p.State == "start" {
					if active != "" {
						t.Fatal("overlapping phases", active, p)
					}
					active = p.Phase
					seen[p.Phase] = true
				}
				if p.State == "done" {
					if active != p.Phase {
						t.Fatal("unmatched completion", p)
					}
					active = ""
				}
			} else if e.Event == "result" && active != "" {
				t.Fatal("result before phase completion")
			}
		}
		if active != "" || !seen["inspect"] {
			t.Fatal("incomplete lifecycle", seen, active)
		}
		if request.DryRun || result.Actions[0].Action == "skip" {
			if seen["extract"] || seen["verify"] {
				t.Fatal("unnecessary extraction", seen)
			}
		} else if !seen["extract"] || !seen["verify"] || !seen["commit"] {
			t.Fatal("missing stages", seen)
		}
	}
}

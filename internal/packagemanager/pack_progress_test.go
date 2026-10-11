package packagemanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackProgressCancellationDoesNotPublish(t *testing.T) {
	source := t.TempDir()
	for name, data := range map[string]string{"desc.json": `{"$version":"1","runtimeLevel":1,"id":"pack","version":"1"}`, "resource.bin": strings.Repeat("x", 2<<20)} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, phase := range []string{"pack", "verify"} {
		t.Run(phase, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "package.dspk")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var events []Event
			_, err := New("").Pack(ctx, source, output, false, false, func(e Event) {
				events = append(events, e)
				if p, ok := e.Data.(Progress); ok && p.Phase == phase && p.Current > 128<<10 {
					cancel()
				}
			})
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatal("cancelled archive was published", err)
			}
			entries, err := os.ReadDir(filepath.Dir(output))
			if err != nil || len(entries) != 0 {
				t.Fatal("temporary output survived", entries, err)
			}
			for _, e := range events {
				if e.Event == "result" || e.Event == "summary" {
					t.Fatal("cancelled pack reported success", e)
				}
				if p, ok := e.Data.(Progress); ok && p.Phase == phase && p.State == "done" {
					t.Fatal("cancelled phase reported success", p)
				}
			}
		})
	}
}

package packagemanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diffscope/diffscope-package-manager/internal/testutil"
)

func TestExtractionCancellationPreservesStoreAndRemovesStaging(t *testing.T) {
	manager := New(filepath.Join(t.TempDir(), "packages"))
	existing := testutil.Package(t, "existing", "1", "1", nil, map[string]string{"resource": "original"})
	if _, err := manager.Install(context.Background(), InstallRequest{Files: []string{existing}}, nil); err != nil {
		t.Fatal(err)
	}
	pending := testutil.Package(t, "pending", "1", "1", nil, map[string]string{"model.bin": strings.Repeat("payload", 1<<18)})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := manager.Install(ctx, InstallRequest{Files: []string{pending}}, func(e Event) {
		if p, ok := e.Data.(Progress); ok && p.Phase == "extract" && p.Current > 0 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	records, _, err := manager.List(context.Background())
	if err != nil || len(records) != 1 || records[0].ID != "existing" {
		t.Fatal(records, err)
	}
	contents, err := os.ReadFile(filepath.Join(manager.Root, records[0].Directory, "resource"))
	if err != nil || string(contents) != "original" {
		t.Fatal(string(contents), err)
	}
	entries, err := os.ReadDir(filepath.Join(manager.Root, ".dspm", "transactions"))
	if err != nil && !os.IsNotExist(err) || len(entries) != 0 {
		t.Fatal(entries, err)
	}
}

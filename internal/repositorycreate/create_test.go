package repositorycreate

import (
	"context"
	"encoding/json"
	"github.com/diffscope/diffscope-package-manager/internal/testutil"
	repo "github.com/diffscope/diffscope-package-manager/packagerepository"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestCreateIdempotenceDeprecationConflictDryRun(t *testing.T) {
	root := t.TempDir()
	f := testutil.Package(t, "foo/bar", "1", "1", nil, nil)
	ctx := context.Background()
	preview, err := Create(ctx, f, root, "https://example.org/f", true)
	if err != nil || !preview.Changed {
		t.Fatal(preview, err)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 0 {
		t.Fatal("dry run wrote files")
	}
	result, err := Create(ctx, f, root, "https://example.org/f", false)
	if err != nil || !result.Changed {
		t.Fatal(result, err)
	}
	b, _ := os.ReadFile(result.ManifestPath)
	b = []byte(strings.Replace(string(b), `"downloadUrl"`, `"deprecated": true, "deprecationReason": "test", "downloadUrl"`, 1))
	_ = os.WriteFile(result.ManifestPath, b, 0600)
	result, err = Create(ctx, f, root, "https://example.org/f", false)
	if err != nil || result.Changed {
		t.Fatal(result, err)
	}
	after, _ := os.ReadFile(result.ManifestPath)
	if string(after) != string(b) {
		t.Fatal("modified existing bytes")
	}
	if _, err = Create(ctx, f, root, "https://example.org/other", false); err == nil {
		t.Fatal("URL conflict accepted")
	}
	if _, err = os.Stat(filepath.Join(root, "packages", "index.json")); !os.IsNotExist(err) {
		t.Fatal("generated index")
	}
}

func TestKnownPublicationRecoveryAndConflictingTarget(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(map[bool]string{false: "resume", true: "target-modified"}[conflict], func(t *testing.T) {
			root := t.TempDir()
			ctx := context.Background()
			archive := testutil.Package(t, "foo", "1", "1", nil, nil)
			result, err := Create(ctx, archive, root, "https://example.org/archive", false)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(result.ManifestPath)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Remove(result.ManifestPath); err != nil {
				t.Fatal(err)
			}
			r, err := os.OpenRoot(root)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			tx := ".dspm-repo/transactions/known"
			if err = r.MkdirAll(tx, 0700); err != nil {
				t.Fatal(err)
			}
			asset := []byte("license\r\n")
			assetPath := "packages/foo/@assets/1.0.0.0/LICENSE"
			r.WriteFile(tx+"/0", asset, 0600)
			r.WriteFile(tx+"/1", raw, 0600)
			j := journal{Version: 1, Entries: []entry{{assetPath, "0", repo.Sha512(asset)}, {"packages/foo/@versions/1.0.0.0.json", "1", repo.Sha512(raw)}}}
			b, _ := json.Marshal(j)
			r.WriteFile(tx+"/journal.json", b, 0600)
			r.MkdirAll(".dspm-repo/transactions/unknown", 0700)
			r.WriteFile(".dspm-repo/transactions/unknown/personal", []byte("keep"), 0600)
			if conflict {
				r.MkdirAll("packages/foo/@assets/1.0.0.0", 0755)
				r.WriteFile(assetPath, []byte("external edit"), 0600)
			}
			_, err = Create(ctx, archive, root, "https://example.org/archive", false)
			if conflict {
				if err == nil || !strings.Contains(err.Error(), "REPOSITORY_RECOVERY_REQUIRED") {
					t.Fatal(err)
				}
				if _, e := os.Stat(result.ManifestPath); !os.IsNotExist(e) {
					t.Fatal("manifest visible despite conflicting asset")
				}
				if b, _ := r.ReadFile(assetPath); string(b) != "external edit" {
					t.Fatal("overwrote external edit")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if b, _ := os.ReadFile(result.ManifestPath); string(b) != string(raw) {
					t.Fatal("manifest bytes changed")
				}
				if b, _ := r.ReadFile(assetPath); string(b) != string(asset) {
					t.Fatal("asset bytes changed")
				}
			}
			if b, _ := r.ReadFile(".dspm-repo/transactions/unknown/personal"); string(b) != "keep" {
				t.Fatal("deleted unknown transaction")
			}
		})
	}
}

func TestConcurrentInitialPublicationIsIdempotent(t *testing.T) {
	root := t.TempDir()
	archive := testutil.Package(t, "foo", "1", "1", nil, nil)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	changed := make(chan bool, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := Create(context.Background(), archive, root, "https://example.org/archive", false)
			errs <- err
			changed <- result.Changed
		}()
	}
	wg.Wait()
	close(errs)
	close(changed)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	count := 0
	for yes := range changed {
		if yes {
			count++
		}
	}
	if count != 1 {
		t.Fatal("created identity", count, "times")
	}
}

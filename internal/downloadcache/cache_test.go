package downloadcache

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestUnmarkedRootAndManagedClean(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "personal.txt")
	os.WriteFile(file, []byte("keep"), 0600)
	if _, err := Open(context.Background(), root, "", true); err == nil {
		t.Fatal("claimed nonempty directory")
	}
	root = filepath.Join(t.TempDir(), "cache")
	c, err := Open(context.Background(), root, "", true)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Root.MkdirAll("partials", 0700)
	c.Root.WriteFile("partials/test", []byte("123"), 0600)
	preview, err := c.Clean(context.Background(), "partial", true)
	if err != nil || preview.RemovedFiles != 1 || preview.FreedBytes != 3 {
		t.Fatal(preview, err)
	}
	if err = c.Lease(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err = c.Clean(ctx, "all", false); err == nil || !strings.Contains(err.Error(), "CACHE_BUSY") {
		t.Fatal("clean ignored lease", err)
	}
	c.lease.Unlock()
	c.lease = nil
	result, err := c.Clean(context.Background(), "all", false)
	if err != nil || result.RemovedFiles != 1 {
		t.Fatal(result, err)
	}
	if _, err = c.Root.Stat("cache-format.json"); err != nil {
		t.Fatal("deleted identity")
	}
	if _, err = c.Root.Stat("locks/lifecycle"); err != nil {
		t.Fatal("deleted lock")
	}
}
func TestLinkEscapeRejected(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	c, err := Open(context.Background(), root, "", true)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "keep"), []byte("safe"), 0600)
	if err = os.Symlink(outside, filepath.Join(root, "blobs")); err != nil {
		t.Skip("symlink privilege unavailable")
	}
	if _, err = c.Clean(context.Background(), "all", false); err == nil {
		t.Fatal("followed cache link")
	}
	if b, _ := os.ReadFile(filepath.Join(outside, "keep")); string(b) != "safe" {
		t.Fatal("damaged external file")
	}
}

func TestConcurrentFirstUseKeepsOneCacheIdentity(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := Open(context.Background(), root, "", true)
			if err == nil {
				err = c.Lease(context.Background())
				c.Close()
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(filepath.Join(root, "cache-format.json"))
	if err != nil || string(b) != marker {
		t.Fatal(string(b), err)
	}
}

func TestParentAliasUsesCanonicalRootButRootLinkIsRejected(t *testing.T) {
	parent := t.TempDir()
	physical := filepath.Join(parent, "physical")
	if err := os.Mkdir(physical, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(parent, "alias")
	if err := os.Symlink(physical, alias); err != nil {
		t.Skip("symlink privilege unavailable")
	}
	c, err := Open(context.Background(), filepath.Join(alias, "cache"), "", true)
	if err != nil {
		t.Fatal(err)
	}
	expected, _ := filepath.EvalSymlinks(filepath.Join(physical, "cache"))
	if c.Root.Name() != expected {
		t.Fatal(c.Root.Name(), expected)
	}
	c.Close()
	rootLink := filepath.Join(parent, "root-link")
	if err := os.Symlink(expected, rootLink); err != nil {
		t.Fatal(err)
	}
	if c, err = Open(context.Background(), rootLink, "", false); err == nil {
		c.Close()
		t.Fatal("linked cache root accepted")
	}
}

func TestMarkedCacheRecoversLockDirectoryAndCleanCancellation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "cache-format.json"), []byte(marker), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Open(context.Background(), root, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = c.Lease(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = c.Clean(ctx, "all", false); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

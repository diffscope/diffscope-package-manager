package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/diffscope/diffscope-package-manager/internal/downloadcache"
	"github.com/diffscope/diffscope-package-manager/internal/netpolicy"
	repo "github.com/diffscope/diffscope-package-manager/packagerepository"
)

func Test304UpdatesCacheMetadata(t *testing.T) {
	ctx := context.Background()
	body, err := repo.Marshal(repo.PackagesIndex{RepositoryFormatVersion: 1, RepositoryRevision: strings.Repeat("a", 40), Packages: []repo.PackageEntry{}})
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("ETag", `"revision"`)
		w.Header().Set("Last-Modified", "Mon, 12 Oct 2026 00:00:00 GMT")
		if r.Header.Get("If-None-Match") != "" {
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Del("Last-Modified")
			w.Header().Set("ETag", `"updated"`)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Cache-Control", "max-age=600")
		w.Write(body)
	}))
	defer server.Close()
	cache, err := downloadcache.Open(ctx, filepath.Join(t.TempDir(), "cache"), "", true)
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	session, err := New(server.URL+"/packages/", "test", 0, netpolicy.Policy{Unrestricted: true}, cache, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	for range 2 {
		if _, err := session.fetch(ctx, "index.json", "", 1<<20); err != nil {
			t.Fatal(err)
		}
	}
	key := "metadata/" + session.Source.ID + "/" + netpolicy.Key(Join(session.Source.URL, "index.json"))
	var meta cached
	if err := cache.ReadJSON(key+".json", &meta); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 || meta.CacheControl != "no-cache" || meta.ETag != `"updated"` || meta.LastModified != "Mon, 12 Oct 2026 00:00:00 GMT" || time.Since(meta.FetchedAt) > time.Minute {
		t.Fatal(requests.Load(), meta.CacheControl)
	}
}

func Test304NoStoreRemovesCachedResponse(t *testing.T) {
	body, _ := repo.Marshal(repo.PackagesIndex{RepositoryFormatVersion: 1, RepositoryRevision: strings.Repeat("a", 40), Packages: []repo.PackageEntry{}})
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("ETag", `"version"`)
		if requests > 1 {
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(304)
			return
		}
		w.Write(body)
	}))
	defer server.Close()
	c, err := downloadcache.Open(context.Background(), filepath.Join(t.TempDir(), "cache"), "", true)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s, err := New(server.URL, "", 0, netpolicy.Policy{Unrestricted: true}, c, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for range 2 {
		actual, err := s.fetch(context.Background(), "index.json", "", 1<<20)
		if err != nil || string(actual) != string(body) {
			t.Fatal(err)
		}
	}
	key := "metadata/" + s.Source.ID + "/" + netpolicy.Key(Join(s.Source.URL, "index.json"))
	for _, suffix := range []string{".body", ".json"} {
		if _, err = c.Root.Stat(key + suffix); !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
}

type forbiddenTransport struct{ t *testing.T }

func (f forbiddenTransport) RoundTrip(*http.Request) (*http.Response, error) {
	f.t.Fatal("Offline metadata issued a network request.")
	return nil, nil
}
func TestOfflineMetadataMissDoesNotUseTransport(t *testing.T) {
	s, err := New("https://example.invalid/packages/", "", 0, netpolicy.Policy{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.Offline = true
	s.Client.Transport = forbiddenTransport{t}
	if _, err = s.fetch(context.Background(), "index.json", "", 1<<20); err == nil || !strings.Contains(err.Error(), "OFFLINE_CACHE_MISS") {
		t.Fatal(err)
	}
}

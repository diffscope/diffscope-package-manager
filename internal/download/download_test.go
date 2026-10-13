package download

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/diffscope/diffscope-package-manager/internal/downloadcache"
	"github.com/diffscope/diffscope-package-manager/internal/netpolicy"
	"github.com/diffscope/diffscope-package-manager/internal/registry"
	"github.com/diffscope/diffscope-package-manager/internal/testutil"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func cacheFor(t *testing.T) *downloadcache.Cache {
	t.Helper()
	c, err := downloadcache.Open(context.Background(), filepath.Join(t.TempDir(), "cache"), "", true)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Lease(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestConfiguredAcquisitionTimeout(t *testing.T) {
	file := testutil.Package(t, "voice", "1", "1", nil, nil)
	server := testutil.ServeRepository(t, file)
	server.ArchiveHandler = func(w http.ResponseWriter, r *http.Request, data []byte) { <-r.Context().Done() }
	engine := New(cacheFor(t), netpolicy.Policy{Unrestricted: true}, nil)
	defer engine.Close()
	engine.Timeout = 50 * time.Millisecond
	authority := registry.Resolved{Manifest: server.Manifests["voice=1.0.0.0"]}
	started := time.Now()
	_, _, err := engine.Acquire(context.Background(), authority, nil)
	var timeout *Error
	if !errors.As(err, &timeout) || timeout.Code != "DOWNLOAD_TIMEOUT" || time.Since(started) > 2*time.Second {
		t.Fatal(err)
	}
}

func TestArchiveSizeMismatchCannotPublishCacheBlob(t *testing.T) {
	file := testutil.Package(t, "voice", "1", "1", nil, nil)
	server := testutil.ServeRepository(t, file)
	authority := registry.Resolved{Manifest: server.Manifests["voice=1.0.0.0"]}
	authority.Manifest.DownloadSize++
	cache := cacheFor(t)
	engine := New(cache, netpolicy.Policy{Unrestricted: true}, nil)
	defer engine.Close()
	_, _, err := engine.Acquire(context.Background(), authority, nil)
	var failure *Error
	if !errors.As(err, &failure) || failure.Code != "DOWNLOAD_SIZE_MISMATCH" {
		t.Fatal(err)
	}
	blob, _, _, err := cache.Paths(authority.Manifest.DownloadSha512)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = cache.Root.Stat(blob); !os.IsNotExist(err) {
		t.Fatal("A mismatched archive was published.", err)
	}
}
func partialServer(w http.ResponseWriter, r *http.Request, b []byte) {
	w.Header().Set("Content-Length", fmt.Sprint(len(b)))
	w.Header().Set("ETag", `"source-A"`)
	w.Write(b[:min(100, len(b))])
}
func rangeServer(t *testing.T, seen *int64) func(http.ResponseWriter, *http.Request, []byte) {
	return func(w http.ResponseWriter, r *http.Request, b []byte) {
		offset := int64(0)
		if v := r.Header.Get("Range"); v != "" {
			offset, _ = strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(v, "bytes="), "-"), 10, 64)
			*seen = offset
			if r.Header.Get("If-Range") != "" {
				t.Error("cross-source validator leaked")
			}
			if offset == int64(len(b)) {
				w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", len(b)))
				w.WriteHeader(416)
				return
			}
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, len(b)-1, len(b)))
			w.Header().Set("Content-Length", fmt.Sprint(int64(len(b))-offset))
			w.WriteHeader(206)
		}
		w.Write(b[offset:])
	}
}
func TestCrossSourceResumeAndSingleTerminal(t *testing.T) {
	f := testutil.Package(t, "foo", "1", "1", nil, map[string]string{"data": strings.Repeat("a", 10000)})
	a := testutil.ServeRepository(t, f)
	b := testutil.ServeRepository(t, f)
	a.ArchiveHandler = partialServer
	var seen int64
	b.ArchiveHandler = rangeServer(t, &seen)
	ma := a.Manifests["foo=1.0.0.0"]
	mb := b.Manifests[ma.String()]
	authority := registry.Resolved{Manifest: ma, Source: registry.Source{ID: "a"}}
	replica := registry.Resolved{Manifest: mb, Source: registry.Source{ID: "b"}}
	terminals := 0
	switches := 0
	c := cacheFor(t)
	e := New(c, netpolicy.Policy{Unrestricted: true}, func(p Progress) {
		if p.State == "done" || p.State == "failed" || p.State == "cancelled" {
			terminals++
		}
		if p.State == "source-switch" {
			switches++
		}
	})
	defer e.Close()
	used := false
	file, out, err := e.Acquire(context.Background(), authority, func(context.Context) (*registry.Resolved, error) {
		if used {
			return nil, nil
		}
		used = true
		return &replica, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen != 100 || terminals != 1 || switches != 1 || out.ActualSourceID != "b" {
		t.Fatal(seen, terminals, switches, out)
	}
	digest, err := hash(context.Background(), c.Root, filepath.ToSlash(strings.TrimPrefix(file, c.Root.Name()+string(filepath.Separator))))
	if err != nil || digest != ma.DownloadSha512 {
		t.Fatal(digest, err)
	}
}
func TestCancellationPersistsAndNextInvocationResumes(t *testing.T) {
	f := testutil.Package(t, "foo", "1", "1", nil, map[string]string{"data": strings.Repeat("x", 1<<20)})
	server := testutil.ServeRepository(t, f)
	var seen int64
	server.ArchiveHandler = rangeServer(t, &seen)
	m := server.Manifests["foo=1.0.0.0"]
	r := registry.Resolved{Manifest: m, Source: registry.Source{ID: "a"}}
	c := cacheFor(t)
	ctx, cancel := context.WithCancel(context.Background())
	e := New(c, netpolicy.Policy{Unrestricted: true}, func(p Progress) {
		if p.Status == "downloading" && p.Current > 0 {
			cancel()
		}
	})
	_, _, err := e.Acquire(ctx, r, nil)
	e.Close()
	if err == nil {
		t.Fatal("cancel was ignored")
	}
	_, partial, sidecar, _ := c.Paths(m.DownloadSha512)
	st, err := c.Root.Stat(partial)
	if err != nil || st.Size() == 0 {
		t.Fatal("no checkpoint", st, err)
	}
	var cp checkpoint
	if err = c.ReadJSON(sidecar, &cp); err != nil || cp.Bytes <= 0 {
		t.Fatal(cp, err)
	}
	e = New(c, netpolicy.Policy{Unrestricted: true}, nil)
	defer e.Close()
	if _, _, err = e.Acquire(context.Background(), r, nil); err != nil {
		t.Fatal(err)
	}
	if seen != cp.Bytes {
		t.Fatal("did not resume checkpoint", seen, cp)
	}
}
func TestNoStore(t *testing.T) {
	f := testutil.Package(t, "foo", "1", "1", nil, nil)
	server := testutil.ServeRepository(t, f)
	server.ArchiveHandler = func(w http.ResponseWriter, r *http.Request, b []byte) {
		w.Header().Set("Cache-Control", "no-store")
		w.Write(b)
	}
	m := server.Manifests["foo=1.0.0.0"]
	c := cacheFor(t)
	e := New(c, netpolicy.Policy{Unrestricted: true}, nil)
	file, out, err := e.Acquire(context.Background(), registry.Resolved{Manifest: m, Source: registry.Source{ID: "a"}}, nil)
	if err != nil || out.CacheStatus != "bypass" {
		t.Fatal(out, err)
	}
	blob, partial, _, _ := c.Paths(m.DownloadSha512)
	if _, err = c.Root.Stat(blob); !os.IsNotExist(err) {
		t.Fatal("stored prohibited blob")
	}
	if _, err = c.Root.Stat(partial); !os.IsNotExist(err) {
		t.Fatal("stored prohibited partial")
	}
	if _, err = os.Stat(file); err != nil {
		t.Fatal(err)
	}
	e.Close()
	if _, err = os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("temporary file not cleaned")
	}
}

func TestProtocolRecoveryCacheAndRetries(t *testing.T) {
	for _, scenario := range []string{"ignore-range", "complete-416", "bad-range", "corrupt-blob", "unknown-length", "retry-503", "encoded-response", "hash-mismatch"} {
		t.Run(scenario, func(t *testing.T) {
			archive := testutil.Package(t, "foo", "1", "1", nil, map[string]string{"data": strings.Repeat("x", 4096)})
			server := testutil.ServeRepository(t, archive)
			manifest := server.Manifests["foo=1.0.0.0"]
			raw, _ := os.ReadFile(archive)
			cache := cacheFor(t)
			blob, partial, sidecar, _ := cache.Paths(manifest.DownloadSha512)
			prefix := 100
			switch scenario {
			case "complete-416":
				prefix = len(raw)
				fallthrough
			case "ignore-range", "bad-range":
				cache.Root.MkdirAll(filepath.ToSlash(filepath.Dir(partial)), 0700)
				cache.Root.WriteFile(partial, raw[:prefix], 0600)
				b, _ := json.Marshal(checkpoint{Expected: manifest.DownloadSha512, Bytes: int64(prefix), Total: int64(len(raw))})
				cache.Root.WriteFile(sidecar, b, 0600)
			case "corrupt-blob":
				cache.Root.MkdirAll(filepath.ToSlash(filepath.Dir(blob)), 0700)
				cache.Root.WriteFile(blob, []byte("corrupted"), 0600)
			}
			calls := 0
			restarts := 0
			server.ArchiveHandler = func(w http.ResponseWriter, r *http.Request, b []byte) {
				calls++
				switch scenario {
				case "complete-416":
					w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", len(b)))
					w.WriteHeader(416)
					return
				case "bad-range":
					if calls == 1 {
						w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(b)-1, len(b)))
						w.WriteHeader(206)
						w.Write(b)
						return
					}
				case "unknown-length":
					w.(http.Flusher).Flush()
				case "retry-503":
					if calls == 1 {
						w.Header().Set("Retry-After", "0")
						w.WriteHeader(503)
						return
					}
				case "encoded-response":
					w.Header().Set("Content-Encoding", "gzip")
					w.Write(b)
					return
				case "hash-mismatch":
					w.Write([]byte("bad archive bytes"))
					return
				}
				w.Write(b)
			}
			engine := New(cache, netpolicy.Policy{Unrestricted: true}, func(p Progress) {
				if p.State == "restart" {
					restarts++
				}
			})
			defer engine.Close()
			_, out, err := engine.Acquire(context.Background(), registry.Resolved{Manifest: manifest, Source: registry.Source{ID: "a"}}, nil)
			if scenario == "encoded-response" || scenario == "hash-mismatch" {
				if err == nil || calls > 2 {
					t.Fatal(calls, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if out.VerifiedDownloadSha512 != manifest.DownloadSha512 {
				t.Fatal("missing verified digest")
			}
			switch scenario {
			case "ignore-range":
				if calls != 1 || restarts != 1 {
					t.Fatal(calls, restarts)
				}
			case "complete-416":
				if calls != 1 || out.ReceivedBytes != 0 {
					t.Fatal(calls, out)
				}
			case "bad-range":
				if calls != 2 || restarts != 1 {
					t.Fatal(calls, restarts)
				}
			case "retry-503":
				if calls != 2 || len(out.Attempts) != 1 {
					t.Fatal(calls, out)
				}
			default:
				if calls != 1 {
					t.Fatal(calls)
				}
			}
		})
	}
}

func TestIdleTimeoutIsFailedWithBoundedRetries(t *testing.T) {
	archive := testutil.Package(t, "foo", "1", "1", nil, nil)
	server := testutil.ServeRepository(t, archive)
	server.ArchiveHandler = func(w http.ResponseWriter, r *http.Request, b []byte) {
		w.Header().Set("Content-Length", fmt.Sprint(len(b)))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}
	cache := cacheFor(t)
	terminal := ""
	count := 0
	engine := New(cache, netpolicy.Policy{Unrestricted: true}, func(p Progress) {
		if p.State == "done" || p.State == "failed" || p.State == "cancelled" {
			terminal = p.State
			count++
		}
	})
	defer engine.Close()
	engine.IdleTimeout = 30 * time.Millisecond
	_, out, err := engine.Acquire(context.Background(), registry.Resolved{Manifest: server.Manifests["foo=1.0.0.0"], Source: registry.Source{ID: "a"}}, nil)
	if err == nil || terminal != "failed" || count != 1 || len(out.Attempts) != 4 {
		t.Fatal(terminal, count, out, err)
	}
	for _, attempt := range out.Attempts {
		if attempt.Code != "DOWNLOAD_TIMEOUT" {
			t.Fatal(attempt)
		}
	}
}

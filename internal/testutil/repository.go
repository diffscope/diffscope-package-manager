package testutil

import (
	"context"
	"fmt"
	"github.com/diffscope/diffscope-package-manager/packagearchive"
	repo "github.com/diffscope/diffscope-package-manager/packagerepository"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
)

type Repository struct {
	Server         *httptest.Server
	Files          map[string][]byte
	Manifests      map[string]repo.RemoteManifest
	mu             sync.Mutex
	Requests       map[string]int
	ArchiveHandler func(http.ResponseWriter, *http.Request, []byte)
}

func ServeRepository(t *testing.T, archives ...string) *Repository {
	t.Helper()
	r := &Repository{Files: map[string][]byte{}, Manifests: map[string]repo.RemoteManifest{}, Requests: map[string]int{}}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.Requests[req.URL.Path]++
		data, ok := r.Files[req.URL.Path]
		r.mu.Unlock()
		if !ok {
			http.NotFound(w, req)
			return
		}
		if strings.HasPrefix(req.URL.Path, "/archives/") && r.ArchiveHandler != nil {
			r.ArchiveHandler(w, req, data)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		w.Write(data)
	}))
	t.Cleanup(r.Server.Close)
	for _, f := range archives {
		a, err := packagearchive.Open(f, packagearchive.DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		p, err := repo.ProjectFS(context.Background(), a.Files(), repo.ProjectionOptions{})
		a.Close()
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(f)
		m, _, err := repo.BuildManifest(p, repo.Publication{DownloadURL: "https://example.org/f", DownloadSha512: repo.Sha512(raw)}, nil)
		if err != nil {
			t.Fatal(err)
		}
		archivePath := "/archives/" + m.ID + "/" + m.Version.String()
		m.DownloadURL = r.Server.URL + archivePath
		r.Manifests[m.String()] = m
		r.Files[archivePath] = raw
	}
	r.Reindex(t)
	return r
}
func (r *Repository) Reindex(t *testing.T) {
	t.Helper()
	byID := map[string][]repo.RemoteManifest{}
	for _, m := range r.Manifests {
		byID[m.ID] = append(byID[m.ID], m)
	}
	root := repo.PackagesIndex{RepositoryFormatVersion: 1, RepositoryRevision: strings.Repeat("0", 40), Packages: []repo.PackageEntry{}}
	ids := []string{}
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		versions := byID[id]
		sort.Slice(versions, func(i, j int) bool { return versions[i].Version.Compare(versions[j].Version) > 0 })
		index := repo.VersionsIndex{RepositoryFormatVersion: 1, ID: id, LatestVersion: versions[0].Version, Versions: []repo.VersionEntry{}}
		for _, m := range versions {
			raw, _ := repo.Marshal(m)
			r.Files["/packages/"+id+"/@versions/"+m.Version.String()+".json"] = raw
			index.Versions = append(index.Versions, repo.VersionEntry{Version: m.Version, CompatVersion: m.CompatVersion, ManifestSha512: repo.Sha512(raw), Deprecated: m.Deprecated})
		}
		raw, _ := repo.Marshal(index)
		r.Files["/packages/"+id+"/@versions/index.json"] = raw
		root.Packages = append(root.Packages, repo.PackageEntry{ID: id, LatestVersion: index.LatestVersion, VersionsIndexSha512: repo.Sha512(raw)})
	}
	r.Files["/packages/index.json"], _ = repo.Marshal(root)
}
func (r *Repository) Count(path string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.Requests[path]
}

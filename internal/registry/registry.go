package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/diffscope/diffscope-package-manager/internal/downloadcache"
	"github.com/diffscope/diffscope-package-manager/internal/netpolicy"
	"github.com/diffscope/diffscope-package-manager/packageinfo"
	repo "github.com/diffscope/diffscope-package-manager/packagerepository"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"
)

var ErrInconsistent = errors.New("REPOSITORY_INCONSISTENT")

type Source struct {
	ID         string `json:"sourceId"`
	Name       string `json:"sourceName,omitempty"`
	URL        string `json:"registryUrl"`
	Priority   int    `json:"priority"`
	Revision   string `json:"repositoryRevision"`
	RootSha512 string `json:"rootIndexSha512"`
	Freshness  string `json:"freshness,omitempty"`
}
type Rule struct {
	Prefix  string   `json:"prefix"`
	Sources []string `json:"sources"`
}
type Resolved struct {
	Manifest       repo.RemoteManifest `json:"manifest"`
	ManifestSha512 string              `json:"manifestSha512"`
	Source         Source              `json:"repository"`
}
type Budget struct {
	mu    sync.Mutex
	bytes int64
}

func (b *Budget) add(n int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.bytes += int64(n)
	if b.bytes > 64<<20 {
		return fmt.Errorf("The cumulative metadata exceeds its byte budget.")
	}
	return nil
}

type Session struct {
	Source    Source
	Policy    netpolicy.Policy
	Cache     *downloadcache.Cache
	Budget    *Budget
	Client    *http.Client
	Offline   bool
	mu        sync.Mutex
	root      *repo.PackagesIndex
	versions  map[string]repo.VersionsIndex
	manifests map[string]Resolved
}

func Normalize(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("The registry base URL is invalid.")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(path.Clean("/"+u.Path), "/") + "/"
	u.RawPath = ""
	return u.String(), nil
}
func New(raw, name string, priority int, p netpolicy.Policy, c *downloadcache.Cache, b *Budget) (*Session, error) {
	u, err := Normalize(raw)
	if err != nil {
		return nil, err
	}
	if b == nil {
		b = &Budget{}
	}
	return &Session{Source: Source{ID: netpolicy.Key(u), Name: name, URL: u, Priority: priority}, Policy: p, Cache: c, Budget: b, Client: p.Client(true), versions: map[string]repo.VersionsIndex{}, manifests: map[string]Resolved{}}, nil
}
func (s *Session) Close() { s.Client.CloseIdleConnections() }
func Join(base, relative string) string {
	u, _ := url.Parse(base)
	u.Path = path.Join(u.Path, relative)
	u.RawPath = ""
	return u.String()
}

type cached struct {
	Sha512       string    `json:"sha512"`
	ETag         string    `json:"etag"`
	LastModified string    `json:"lastModified"`
	FetchedAt    time.Time `json:"fetchedAt"`
	CacheControl string    `json:"cacheControl,omitempty"`
}

func (s *Session) fetch(ctx context.Context, relative, expected string, max int) ([]byte, error) {
	rawURL := Join(s.Source.URL, relative)
	if err := s.Policy.Validate(rawURL); err != nil {
		return nil, err
	}
	if s.Cache != nil {
		lock, err := s.Cache.ObjectLock(ctx, repo.Sha512([]byte(rawURL)))
		if err != nil {
			return nil, err
		}
		defer lock.Unlock()
	}
	key := "metadata/" + s.Source.ID + "/" + netpolicy.Key(rawURL)
	if s.Cache != nil {
		for _, name := range []string{key + ".json", key + ".body"} {
			if err := s.Cache.CheckPath(name); err != nil {
				return nil, err
			}
		}
	}
	var meta cached
	var body []byte
	if s.Cache != nil {
		_ = s.Cache.ReadJSON(key+".json", &meta)
		b, e := s.Cache.Root.ReadFile(key + ".body")
		if e == nil && len(b) <= max && repo.Sha512(b) == meta.Sha512 {
			body = b
		}
		if expected != "" && body != nil && meta.Sha512 == expected && !strings.Contains(strings.ToLower(meta.CacheControl), "no-cache") {
			return body, s.Budget.add(len(body))
		}
	}
	if s.Offline {
		if body == nil || expected != "" && meta.Sha512 != expected {
			return nil, fmt.Errorf("OFFLINE_CACHE_MISS: Repository metadata is not cached: %s.", netpolicy.Redact(rawURL))
		}
		return body, s.Budget.add(len(body))
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Cache-Control", "no-cache")
	if expected == "" && body != nil {
		if meta.ETag != "" {
			req.Header.Set("If-None-Match", meta.ETag)
		} else if meta.LastModified != "" {
			req.Header.Set("If-Modified-Since", meta.LastModified)
		}
	}
	var resp *http.Response
	var finishRequest context.CancelFunc
	for attempt := 0; attempt < 3; attempt++ {
		requestCtx, stop := context.WithTimeout(ctx, 30*time.Second)
		resp, err = s.Client.Do(req.Clone(requestCtx))
		retryable := err != nil && !strings.Contains(safeError(err), "certificate") && !strings.Contains(safeError(err), "DOWNLOAD_POLICY_VIOLATION")
		if err == nil {
			retryable = resp.StatusCode == 408 || resp.StatusCode == 429 || resp.StatusCode == 500 || resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504
		}
		if !retryable || attempt == 2 {
			finishRequest = stop
			break
		}
		wait := time.Duration(attempt+1) * 200 * time.Millisecond
		if resp != nil {
			raw := resp.Header.Get("Retry-After")
			resp.Body.Close()
			if raw != "" {
				if t, e := time.ParseDuration(raw + "s"); e == nil && t >= 0 {
					wait = t
				} else if t, e := http.ParseTime(raw); e == nil {
					wait = time.Until(t)
					if wait < 0 {
						wait = 0
					}
				}
			}
		}
		stop()
		if deadline, ok := ctx.Deadline(); ok && wait > time.Until(deadline) {
			if resp != nil && (resp.StatusCode == 429 || resp.StatusCode == 503) {
				return nil, fmt.Errorf("RATE_LIMITED: The metadata retry delay exceeds the remaining time budget.")
			}
			return nil, fmt.Errorf("DOWNLOAD_TIMEOUT: Metadata retries exhausted the request deadline.")
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	if finishRequest != nil {
		defer finishRequest()
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("The metadata GET request to %s failed: %w", netpolicy.Redact(rawURL), context.DeadlineExceeded)
		}
		return nil, fmt.Errorf("The metadata GET request to %s failed: %s", netpolicy.Redact(rawURL), safeError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode == 304 {
		if body != nil {
			if expected != "" && repo.Sha512(body) != expected {
				return nil, ErrInconsistent
			}
			for _, field := range []struct {
				header string
				value  *string
			}{
				{"ETag", &meta.ETag}, {"Last-Modified", &meta.LastModified}, {"Cache-Control", &meta.CacheControl},
			} {
				if values, present := resp.Header[http.CanonicalHeaderKey(field.header)]; present {
					*field.value = strings.Join(values, ", ")
				}
			}
			meta.FetchedAt = time.Now().UTC()
			if s.Cache != nil {
				if strings.Contains(strings.ToLower(meta.CacheControl), "no-store") {
					if err = s.Cache.Root.Remove(key + ".body"); err != nil && !errors.Is(err, os.ErrNotExist) {
						return nil, err
					}
					if err = s.Cache.Root.Remove(key + ".json"); err != nil && !errors.Is(err, os.ErrNotExist) {
						return nil, err
					}
				} else {
					b, _ := json.Marshal(meta)
					if err = downloadcache.AtomicWrite(s.Cache.Root, key+".json", b); err != nil {
						return nil, err
					}
				}
			}
			return body, s.Budget.add(len(body))
		}
		return nil, fmt.Errorf("The HTTP 304 response has no cached body.")
	}
	if resp.StatusCode != 200 {
		if resp.StatusCode == 404 && expected != "" {
			return nil, fmt.Errorf("%w: A referenced child is missing.", ErrInconsistent)
		}
		return nil, fmt.Errorf("The repository returned HTTP status %d at %s.", resp.StatusCode, netpolicy.Redact(rawURL))
	}
	if v := resp.Header.Get("Content-Encoding"); v != "" && v != "identity" {
		return nil, fmt.Errorf("The metadata content encoding is unsupported.")
	}
	body, err = io.ReadAll(io.LimitReader(resp.Body, int64(max)+1))
	if err != nil {
		return nil, err
	}
	if len(body) > max {
		return nil, fmt.Errorf("The metadata exceeds its byte budget.")
	}
	if err = s.Budget.add(len(body)); err != nil {
		return nil, err
	}
	digest := repo.Sha512(body)
	if expected != "" && digest != expected {
		return nil, fmt.Errorf("%w: The expected digest is %s. The actual digest is %s.", ErrInconsistent, expected, digest)
	}
	if relative == "index.json" {
		_, err = repo.DecodePackagesIndex(body, repo.DecodeLimits{MaxBytes: max})
	} else if strings.HasSuffix(relative, "/@versions/index.json") {
		_, err = repo.DecodeVersionsIndex(body, repo.DecodeLimits{MaxBytes: max})
	} else {
		mode := repo.StrictPublication
		if s.Policy.Unrestricted {
			mode = repo.ClientUnrestrictedURLs
		}
		_, err = repo.DecodeManifest(body, repo.ManifestDecodeOptions{Limits: repo.DecodeLimits{MaxBytes: max}, DownloadURLMode: mode})
	}
	if err != nil {
		return nil, fmt.Errorf("INVALID_REMOTE_MANIFEST: %w", err)
	}
	if s.Cache != nil && strings.Contains(strings.ToLower(resp.Header.Get("Cache-Control")), "no-store") {
		_ = s.Cache.Root.Remove(key + ".body")
		_ = s.Cache.Root.Remove(key + ".json")
	}
	if s.Cache != nil && !strings.Contains(strings.ToLower(resp.Header.Get("Cache-Control")), "no-store") {
		if err = s.Cache.Root.MkdirAll(path.Dir(key), 0700); err != nil {
			return nil, err
		}
		if err = downloadcache.AtomicWrite(s.Cache.Root, key+".body", body); err != nil {
			return nil, err
		}
		meta = cached{Sha512: digest, ETag: resp.Header.Get("ETag"), LastModified: resp.Header.Get("Last-Modified"), FetchedAt: time.Now().UTC(), CacheControl: resp.Header.Get("Cache-Control")}
		b, _ := json.Marshal(meta)
		if err = downloadcache.AtomicWrite(s.Cache.Root, key+".json", b); err != nil {
			return nil, err
		}
	}
	return body, nil
}
func safeError(err error) string {
	var e *url.Error
	if errors.As(err, &e) {
		return e.Err.Error()
	}
	return err.Error()
}
func (s *Session) initialize(ctx context.Context) error {
	if s.root != nil {
		return nil
	}
	b, err := s.fetch(ctx, "index.json", "", 16<<20)
	if err != nil {
		return err
	}
	m, err := repo.DecodePackagesIndex(b, repo.DecodeLimits{MaxBytes: 16 << 20})
	if err != nil {
		return err
	}
	s.root = &m
	s.Source.Revision = m.RepositoryRevision
	s.Source.RootSha512 = repo.Sha512(b)
	s.Source.Freshness = "revalidated"
	if s.Offline {
		s.Source.Freshness = "cached"
	}
	return nil
}
func (s *Session) Resolve(ctx context.Context, id string, exact *packageinfo.Version, target *packageinfo.Version) (Resolved, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result Resolved
	if err := s.initialize(ctx); err != nil {
		return result, false, err
	}
	versions, ok, err := s.loadVersions(ctx, id)
	if err != nil || !ok {
		return result, false, err
	}
	var selected *repo.VersionEntry
	for i, v := range versions.Versions {
		if exact != nil && v.Version.Compare(*exact) != 0 {
			continue
		}
		if target != nil && (v.CompatVersion.Compare(*target) > 0 || v.Version.Compare(*target) < 0) {
			continue
		}
		selected = &versions.Versions[i]
		break
	}
	if selected == nil {
		return result, false, nil
	}
	key := id + "=" + selected.Version.String()
	if m, ok := s.manifests[key]; ok {
		return m, true, nil
	}
	b, err := s.fetch(ctx, id+"/@versions/"+selected.Version.String()+".json", selected.ManifestSha512, 1<<20)
	if err != nil {
		return result, false, err
	}
	mode := repo.StrictPublication
	if s.Policy.Unrestricted {
		mode = repo.ClientUnrestrictedURLs
	}
	m, err := repo.DecodeManifest(b, repo.ManifestDecodeOptions{DownloadURLMode: mode})
	if err != nil {
		return result, false, fmt.Errorf("INVALID_REMOTE_MANIFEST: %w", err)
	}
	if m.ID != id || m.Version.Compare(selected.Version) != 0 || m.CompatVersion.Compare(selected.CompatVersion) != 0 || m.Deprecated != selected.Deprecated {
		return result, false, fmt.Errorf("%w: Manifest and index metadata differ.", ErrInconsistent)
	}
	result = Resolved{m, selected.ManifestSha512, s.Source}
	s.manifests[key] = result
	return result, true, nil
}
func Allowed(id string, s Source, rules []Rule) bool {
	var matched *Rule
	for i, r := range rules {
		if (id == r.Prefix || strings.HasSuffix(r.Prefix, "/") && strings.HasPrefix(id, r.Prefix)) && (matched == nil || len(r.Prefix) > len(matched.Prefix)) {
			matched = &rules[i]
		}
	}
	if matched == nil {
		return true
	}
	for _, name := range matched.Sources {
		if name == s.Name || name == s.ID || name == s.URL {
			return true
		}
	}
	return false
}

// Returns the ordered versions index and whether the package is present.
func (s *Session) Versions(ctx context.Context, id string) (repo.VersionsIndex, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.initialize(ctx); err != nil {
		return repo.VersionsIndex{}, false, err
	}
	return s.loadVersions(ctx, id)
}
func (s *Session) loadVersions(ctx context.Context, id string) (repo.VersionsIndex, bool, error) {
	var entry *repo.PackageEntry
	for i := range s.root.Packages {
		if s.root.Packages[i].ID == id {
			entry = &s.root.Packages[i]
			break
		}
	}
	if entry == nil {
		return repo.VersionsIndex{}, false, nil
	}
	versions, ok := s.versions[id]
	if !ok {
		b, err := s.fetch(ctx, id+"/@versions/index.json", entry.VersionsIndexSha512, 8<<20)
		if err != nil {
			return repo.VersionsIndex{}, false, err
		}
		versions, err = repo.DecodeVersionsIndex(b, repo.DecodeLimits{MaxBytes: 8 << 20})
		if err != nil {
			return repo.VersionsIndex{}, false, err
		}
		if versions.ID != id || versions.LatestVersion.Compare(entry.LatestVersion) != 0 {
			return repo.VersionsIndex{}, false, fmt.Errorf("%w: Index identity or latest-version metadata differs.", ErrInconsistent)
		}
		s.versions[id] = versions
	}
	return versions, true, nil
}

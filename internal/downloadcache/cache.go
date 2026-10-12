package downloadcache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	repo "github.com/diffscope/diffscope-package-manager/packagerepository"
	"github.com/gofrs/flock"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const marker = `{"cacheFormatVersion":1}` + "\n"

type Cache struct {
	Root  *os.Root
	lease *flock.Flock
}

func validateRoot(root, packages string) (string, error) {
	a, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if st, e := os.Lstat(a); e == nil && st.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("INVALID_CACHE_DIRECTORY: The cache root is a symbolic link.")
	}
	a, err = canonicalPath(a)
	if err != nil {
		return "", err
	}
	home, _ := os.UserHomeDir()
	home, _ = canonicalPath(home)
	p, _ := filepath.Abs(packages)
	p, _ = canonicalPath(p)
	equal := func(x, y string) bool {
		if runtime.GOOS == "windows" {
			return strings.EqualFold(x, y)
		}
		return x == y
	}
	rel, _ := filepath.Rel(p, a)
	if equal(a, filepath.VolumeName(a)+string(filepath.Separator)) || equal(a, home) || equal(a, p) || equal(filepath.Base(a), ".dspm") || equal(rel, ".dspm") || strings.HasPrefix(strings.ToLower(filepath.ToSlash(rel)), ".dspm/") {
		return "", fmt.Errorf("INVALID_CACHE_DIRECTORY: The cache root is unsafe.")
	}
	if _, err := os.Stat(filepath.Join(a, ".git")); err == nil {
		return "", fmt.Errorf("INVALID_CACHE_DIRECTORY: The cache root is a Git project root.")
	}
	return a, nil
}

// Returns a physical path with existing parent aliases resolved or an error for failed path resolution.
// System aliases such as macOS /var and /tmp are permitted as parents.
// Cache validation separately rejects links at the cache root and inside it.
func canonicalPath(p string) (string, error) {
	cur := p
	for {
		_, err := os.Lstat(cur)
		if err == nil {
			physical, err := filepath.EvalSymlinks(cur)
			if err != nil {
				return "", err
			}
			rel, err := filepath.Rel(cur, p)
			if err != nil {
				return "", err
			}
			return filepath.Join(physical, rel), nil
		}
		if !os.IsNotExist(err) || cur == filepath.Dir(cur) {
			return "", err
		}
		cur = filepath.Dir(cur)
	}
}
func Open(ctx context.Context, root, packages string, create bool) (*Cache, error) {
	a, err := validateRoot(root, packages)
	if err != nil {
		return nil, err
	}
	if _, err = os.Stat(a); os.IsNotExist(err) {
		if !create {
			return nil, nil
		}
		if err = os.MkdirAll(a, 0700); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	r, err := os.OpenRoot(a)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			r.Close()
		}
	}()
	if err = prepareMarker(ctx, r, create); err != nil {
		return nil, err
	}
	if err = checkTree(r); err != nil {
		return nil, err
	}
	// A crash immediately after marker publication may leave no lock directory.
	// Only an identified cache permits recreation of the stable lock directory.
	if err = r.MkdirAll("locks", 0700); err != nil {
		return nil, err
	}
	ok = true
	return &Cache{Root: r}, nil
}
func checkTree(r *os.Root) error {
	return fs.WalkDir(r.FS(), ".", func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("INVALID_CACHE_DIRECTORY: The cache entry %s is a symbolic link.", p)
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			return fmt.Errorf("INVALID_CACHE_DIRECTORY: The cache contains a special file.")
		}
		return nil
	})
}
func (c *Cache) Lease(ctx context.Context) error {
	l := flock.New(filepath.Join(c.Root.Name(), "locks", "lifecycle"))
	ok, err := l.TryRLockContext(ctx, 100*time.Millisecond)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("CACHE_BUSY: The cache lifecycle or object lock is unavailable.")
	}
	c.lease = l
	return nil
}
func (c *Cache) Close() error {
	if c == nil {
		return nil
	}
	if c.lease != nil {
		c.lease.Unlock()
	}
	return c.Root.Close()
}
func (c *Cache) ObjectLock(ctx context.Context, digest string) (*flock.Flock, error) {
	if !repo.IsDigest(digest) {
		return nil, fmt.Errorf("The digest is invalid.")
	}
	l := flock.New(filepath.Join(c.Root.Name(), "locks", digest))
	ok, err := l.TryLockContext(ctx, 100*time.Millisecond)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("CACHE_BUSY: The cache lifecycle or object lock is unavailable.")
	}
	return l, nil
}
func AtomicWrite(r *os.Root, p string, b []byte) error {
	f, err := r.OpenFile(p+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	e := f.Close()
	if err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	return r.Rename(p+".tmp", p)
}
func (c *Cache) Paths(digest string) (blob, partial, sidecar string, errorValue error) {
	if !repo.IsDigest(digest) {
		return "", "", "", fmt.Errorf("The digest is invalid.")
	}
	blob = "blobs/sha512/" + digest[:2] + "/" + digest + ".dspk"
	partial = "partials/sha512/" + digest[:2] + "/" + digest + ".part"
	sidecar = strings.TrimSuffix(partial, ".part") + ".json"
	return
}

type Stats struct {
	Directory string           `json:"directory"`
	Files     map[string]int   `json:"files"`
	Bytes     map[string]int64 `json:"bytes"`
	Active    bool             `json:"active"`
}

func (c *Cache) Stats() (Stats, error) {
	s := Stats{Directory: c.Root.Name(), Files: map[string]int{}, Bytes: map[string]int64{}}
	err := fs.WalkDir(c.Root.FS(), ".", func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		kind := strings.SplitN(p, "/", 2)[0]
		if kind == "locks" || kind == "cache-format.json" {
			return nil
		}
		st, e := d.Info()
		if e != nil {
			return e
		}
		s.Files[kind]++
		s.Bytes[kind] += st.Size()
		return nil
	})
	l := flock.New(filepath.Join(c.Root.Name(), "locks", "lifecycle"))
	ok, _ := l.TryLock()
	if ok {
		l.Unlock()
	} else {
		s.Active = true
	}
	return s, err
}

type CleanResult struct {
	Scope        string `json:"scope"`
	RemovedFiles int    `json:"removedFiles"`
	FreedBytes   int64  `json:"freedBytes"`
	DryRun       bool   `json:"dryRun"`
}

func (c *Cache) Clean(ctx context.Context, scope string, dry bool) (CleanResult, error) {
	out := CleanResult{Scope: scope, DryRun: dry}
	caller := ctx
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	l := flock.New(filepath.Join(c.Root.Name(), "locks", "lifecycle"))
	ok, err := l.TryLockContext(ctx, 100*time.Millisecond)
	if err != nil || !ok {
		if errors.Is(caller.Err(), context.Canceled) {
			return out, caller.Err()
		}
		return out, fmt.Errorf("CACHE_BUSY: The cache lifecycle or object lock is unavailable.")
	}
	defer l.Unlock()
	if err = checkTree(c.Root); err != nil {
		return out, err
	}
	names := []string{"blobs", "partials", "metadata", "tmp"}
	for _, name := range names {
		selected := scope == "all" || scope == "downloads" && (name == "blobs" || name == "partials" || name == "tmp") || scope == "metadata" && name == "metadata" || scope == "partial" && name == "partials"
		if !selected {
			continue
		}
		err = fs.WalkDir(c.Root.FS(), name, func(p string, d fs.DirEntry, e error) error {
			if os.IsNotExist(e) {
				return nil
			}
			if e != nil {
				return e
			}
			if e = ctx.Err(); e != nil {
				return e
			}
			if !d.IsDir() {
				st, e := d.Info()
				if e != nil {
					return e
				}
				out.RemovedFiles++
				out.FreedBytes += st.Size()
			}
			return nil
		})
		if err != nil {
			return out, err
		}
		if !dry {
			if err = c.Root.RemoveAll(name); err != nil {
				return out, err
			}
		}
	}
	return out, nil
}
func (c *Cache) ReadJSON(p string, out any) error {
	b, err := c.Root.ReadFile(p)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func prepareMarker(ctx context.Context, r *os.Root, create bool) error {
	for attempt := 0; attempt < 30; attempt++ {
		b, err := r.ReadFile("cache-format.json")
		if err == nil && string(b) == marker {
			return nil
		}
		if err == nil {
			if !create || !strings.HasPrefix(marker, string(b)) {
				return fmt.Errorf("INVALID_CACHE_DIRECTORY: The cache identity marker is invalid.")
			}
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if os.IsNotExist(err) {
			if !create {
				return fmt.Errorf("INVALID_CACHE_DIRECTORY: The cache identity marker is missing.")
			}
			entries, e := fs.ReadDir(r.FS(), ".")
			if e != nil {
				return e
			}
			if len(entries) != 0 {
				// Concurrent initialization may publish the marker and lock directory
				// after the first read. Marker revalidation prevents a false rejection.
				if current, readErr := r.ReadFile("cache-format.json"); readErr == nil && string(current) == marker {
					return nil
				}
				if len(entries) != 1 || entries[0].Name() != "cache-format.json" {
					return fmt.Errorf("INVALID_CACHE_DIRECTORY: The nonempty directory has no cache identity marker.")
				}
			} else {
				f, e := r.OpenFile("cache-format.json", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
				if e == nil {
					_, e = f.Write([]byte(marker))
					if e == nil {
						e = f.Sync()
					}
					closeErr := f.Close()
					if e != nil {
						return e
					}
					return closeErr
				}
				if !os.IsExist(e) {
					return e
				}
			}
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return fmt.Errorf("INVALID_CACHE_DIRECTORY: The cache identity marker is incomplete.")
}

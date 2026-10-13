// Package repositorycreate publishes repository data without network or store I/O.
package repositorycreate

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/diffscope/diffscope-package-manager/packagearchive"
	"github.com/diffscope/diffscope-package-manager/packageinfo"
	repo "github.com/diffscope/diffscope-package-manager/packagerepository"
	"github.com/gofrs/flock"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

type Result struct {
	Identity       packageinfo.Identity `json:"identity"`
	ManifestPath   string               `json:"manifestPath"`
	DownloadSha512 string               `json:"downloadSha512"`
	Assets         []repo.AssetMapping  `json:"assets"`
	Changed        bool                 `json:"changed"`
	DryRun         bool                 `json:"dryRun"`
	RemoteVerified bool                 `json:"remoteVerified"`
}
type entry struct {
	Path   string `json:"path"`
	Stage  string `json:"stage"`
	Digest string `json:"digest"`
}
type journal struct {
	Version int     `json:"version"`
	Entries []entry `json:"entries"`
}

const marker = "dspm-repository-create-v1\n"

func initializeControl(ctx context.Context, r *os.Root) error {
	err := r.Mkdir(".dspm-repo", 0700)
	if err == nil {
		return syncedWrite(r, ".dspm-repo/format", []byte(marker))
	}
	if !os.IsExist(err) {
		return err
	}
	// Concurrent publishers may observe the directory before marker synchronization.
	// Initialization waits for a valid marker and rejects unrecognized data.
	for attempt := 0; attempt < 30; attempt++ {
		if err := checkPath(r, ".dspm-repo/format"); err != nil {
			return err
		}
		b, err := r.ReadFile(".dspm-repo/format")
		if err == nil && string(b) == marker {
			return nil
		}
		if err != nil && !os.IsNotExist(err) || err == nil && !strings.HasPrefix(marker, string(b)) {
			return fmt.Errorf("REPOSITORY_RECOVERY_REQUIRED: The repository control directory is invalid.")
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return fmt.Errorf("REPOSITORY_RECOVERY_REQUIRED: The repository control marker is incomplete.")
}

// Rejects links and case aliases at every existing prefix.
// Returns nil if all existing prefixes are safe.
func checkPath(r *os.Root, p string) error {
	if err := repo.PortablePath(p); err != nil {
		return err
	}
	parts := strings.Split(p, "/")
	parent := "."
	for _, part := range parts {
		dir, err := fs.ReadDir(r.FS(), parent)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, d := range dir {
			if strings.EqualFold(d.Name(), part) && d.Name() != part {
				return fmt.Errorf("The repository path %s has a case collision.", p)
			}
		}
		parent = path.Join(parent, part)
		st, err := r.Lstat(parent)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if st.Mode()&os.ModeSymlink != 0 || !st.Mode().IsRegular() && !st.IsDir() {
			return fmt.Errorf("The repository path %s is unsafe.", parent)
		}
	}
	return nil
}
func syncedWrite(r *os.Root, p string, b []byte) error {
	f, err := r.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
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
	return err
}
func publish(r *os.Root, tx string, j journal) error {
	if j.Version != 1 || len(j.Entries) == 0 {
		return fmt.Errorf("REPOSITORY_RECOVERY_REQUIRED: The publication journal is invalid.")
	}
	for i, e := range j.Entries {
		if !strings.HasPrefix(e.Path, "packages/") || e.Stage != fmt.Sprintf("%d", i) || !repo.IsDigest(e.Digest) {
			return fmt.Errorf("REPOSITORY_RECOVERY_REQUIRED: The publication journal entry is invalid.")
		}
		if err := checkPath(r, e.Path); err != nil {
			return err
		}
		if err := checkPath(r, path.Join(tx, e.Stage)); err != nil {
			return err
		}
		b, err := r.ReadFile(path.Join(tx, e.Stage))
		if err != nil || repo.Sha512(b) != e.Digest {
			return fmt.Errorf("REPOSITORY_RECOVERY_REQUIRED: Publication staging is damaged.")
		}
		existing, err := r.ReadFile(e.Path)
		if err == nil {
			if repo.Sha512(existing) != e.Digest {
				return fmt.Errorf("REPOSITORY_RECOVERY_REQUIRED: The publication target %s was modified externally.", e.Path)
			}
			continue
		}
		if !os.IsNotExist(err) {
			return err
		}
		if err = r.MkdirAll(path.Dir(e.Path), 0755); err != nil {
			return err
		}
		if err = r.Link(path.Join(tx, e.Stage), e.Path); err != nil {
			return err
		}
	}
	// Cleanup removes only private staged files. Published data is retained.
	return r.RemoveAll(tx)
}
func recoverTransactions(r *os.Root) error {
	dirs, err := fs.ReadDir(r.FS(), ".dspm-repo/transactions")
	if err != nil {
		return err
	}
	for _, d := range dirs {
		if !d.IsDir() || d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("REPOSITORY_RECOVERY_REQUIRED: The transaction evidence is unrecognized.")
		}
		tx := path.Join(".dspm-repo/transactions", d.Name())
		b, err := r.ReadFile(path.Join(tx, "journal.json"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		var j journal
		if err = json.Unmarshal(b, &j); err != nil {
			return err
		}
		if err = publish(r, tx, j); err != nil {
			return err
		}
	}
	return nil
}
func Create(ctx context.Context, filename, repository, downloadURL string, dry bool) (Result, error) {
	result := Result{DryRun: dry, Assets: []repo.AssetMapping{}}
	if err := repo.ValidateURL(downloadURL, false); err != nil {
		return result, err
	}
	st, err := os.Lstat(repository)
	if err != nil {
		return result, err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return result, fmt.Errorf("The repository must be an existing ordinary directory.")
	}
	r, err := os.OpenRoot(repository)
	if err != nil {
		return result, err
	}
	defer r.Close()
	f, err := os.Open(filename)
	if err != nil {
		return result, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() {
		return result, fmt.Errorf("The archive must be a regular file.")
	}
	sha, err := packagearchive.HashFileBytes(ctx, f, before.Size(), nil)
	if err != nil {
		return result, err
	}
	a, err := packagearchive.NewReaderAt(f, before.Size(), packagearchive.DefaultLimits())
	if err != nil {
		return result, err
	}
	defer a.Close()
	if _, err = a.Hash(ctx); err != nil {
		return result, err
	}
	p, err := repo.ProjectFS(ctx, a.Files(), repo.ProjectionOptions{Limits: repo.ProjectionLimits{MaxAssetBytes: 32 << 20}})
	if err != nil {
		return result, err
	}
	m, assets, err := repo.BuildManifest(p, repo.Publication{DownloadURL: downloadURL, DownloadSha512: sha, DownloadSize: before.Size()}, nil)
	if err != nil {
		return result, err
	}
	target := path.Join("packages", m.ID, "@versions", m.Version.String()+".json")
	result.Identity = m.Identity
	result.DownloadSha512 = sha
	result.ManifestPath, _ = filepath.Abs(filepath.Join(repository, filepath.FromSlash(target)))
	result.Assets = assets
	// Dry runs do not initialize generator state.
	if !dry {
		if err = checkPath(r, ".dspm-repo"); err != nil {
			return result, err
		}
		if err = initializeControl(ctx, r); err != nil {
			return result, err
		}
		if err = checkPath(r, ".dspm-repo/lock"); err != nil {
			return result, err
		}
		lock := flock.New(filepath.Join(r.Name(), ".dspm-repo", "lock"))
		locked, e := lock.TryLockContext(ctx, 100*time.Millisecond)
		if e != nil {
			return result, e
		}
		if !locked {
			return result, fmt.Errorf("The repository publication lock is unavailable.")
		}
		defer lock.Unlock()
		if err = r.MkdirAll(".dspm-repo/transactions", 0700); err != nil {
			return result, err
		}
		if err = recoverTransactions(r); err != nil {
			return result, err
		}
	}
	if err = checkPath(r, target); err != nil {
		return result, err
	}
	old, err := r.ReadFile(target)
	if err == nil {
		existing, e := repo.DecodeManifest(old, repo.ManifestDecodeOptions{})
		if e != nil {
			return result, e
		}
		diff, e := repo.CompareMetadata(existing, p)
		if e != nil {
			return result, e
		}
		if len(diff) > 0 || existing.DownloadURL != downloadURL || existing.DownloadSha512 != sha || existing.DownloadSize != before.Size() {
			return result, fmt.Errorf("CONFLICT: The published identity has different content or publication metadata.")
		}
		mapping, e := repo.ResourceMappings(existing, p)
		if e != nil {
			return result, e
		}
		assetFS, e := fs.Sub(r.FS(), path.Join("packages", m.ID, "@assets"))
		if e != nil {
			return result, e
		}
		for _, v := range mapping {
			if e = checkPath(r, path.Join("packages", m.ID, "@assets", v.RepositoryPath)); e != nil {
				return result, e
			}
		}
		diff, e = repo.CompareAssetBytes(ctx, mapping, assetFS)
		if e != nil || len(diff) > 0 {
			return result, fmt.Errorf("CONFLICT: Published asset verification failed: %v", e)
		}
		result.Assets = mapping
		return result, nil
	}
	if !os.IsNotExist(err) {
		return result, err
	}
	result.Changed = true
	raw, err := repo.MarshalManifest(m)
	if err != nil {
		return result, err
	}
	contents := map[string][]byte{}
	order := []string{}
	for _, v := range assets {
		dst := path.Join("packages", m.ID, "@assets", v.RepositoryPath)
		if err = checkPath(r, dst); err != nil {
			return result, err
		}
		b, e := fs.ReadFile(a.Files(), v.PackagePath)
		if e != nil {
			return result, e
		}
		if prior, ok := contents[dst]; ok {
			if repo.Sha512(prior) != repo.Sha512(b) {
				return result, fmt.Errorf("CONFLICT: The asset mapping has a content collision.")
			}
			continue
		}
		if old, e := r.ReadFile(dst); e == nil {
			if repo.Sha512(old) != repo.Sha512(b) {
				return result, fmt.Errorf("CONFLICT: The existing asset has different content.")
			}
		} else if !os.IsNotExist(e) {
			return result, e
		}
		contents[dst] = b
		order = append(order, dst)
	}
	contents[target] = raw
	order = append(order, target)
	after, e := f.Stat()
	if e != nil {
		return result, e
	}
	shaAfter, e := packagearchive.HashFileBytes(ctx, f, before.Size(), nil)
	if e != nil {
		return result, e
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || shaAfter != sha {
		return result, fmt.Errorf("INPUT_CHANGED: The archive input changed during publication.")
	}
	if dry {
		return result, nil
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	id := make([]byte, 16)
	if _, err = rand.Read(id); err != nil {
		return result, err
	}
	tx := path.Join(".dspm-repo/transactions", hex.EncodeToString(id))
	if err = r.Mkdir(tx, 0700); err != nil {
		return result, err
	}
	j := journal{Version: 1, Entries: []entry{}}
	for i, dst := range order {
		stage := fmt.Sprintf("%d", i)
		if err = syncedWrite(r, path.Join(tx, stage), contents[dst]); err != nil {
			return result, err
		}
		j.Entries = append(j.Entries, entry{dst, stage, repo.Sha512(contents[dst])})
	}
	b, err := json.Marshal(j)
	if err != nil {
		return result, err
	}
	if err = syncedWrite(r, path.Join(tx, "journal.json"), b); err != nil {
		return result, err
	}
	if err = publish(r, tx, j); err != nil {
		return result, errors.Join(fmt.Errorf("REPOSITORY_RECOVERY_REQUIRED: Repository publication requires recovery."), err)
	}
	return result, nil
}

package localstore

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/diffscope/diffscope-package-manager/packagearchive"
	"github.com/diffscope/diffscope-package-manager/packagedatabase"
	"github.com/diffscope/diffscope-package-manager/packageinfo"
	"github.com/gofrs/flock"
	"os"
	"path/filepath"
)

type Session struct {
	Root        string
	Diagnostics []string
	db          *packagedatabase.Store
	lock        *flock.Flock
	writable    bool
	transaction string
	stageCount  int
	// Fault is used for deterministic I/O and process-interruption testing.
	Fault func(string) error
}

func DirectoryName(id packageinfo.Identity) string {
	digest := sha256.Sum256([]byte(id.String()))
	return hex.EncodeToString(digest[:])
}

func Open(root string, write bool) (*Session, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	s := &Session{Root: absolute, writable: write}
	if _, err := os.Stat(filepath.Join(absolute, "packages.db")); err == nil {
		return nil, fmt.Errorf("legacy package database is unsupported")
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	state := filepath.Join(absolute, ".dspm")
	if _, err := os.Stat(state); os.IsNotExist(err) {
		if !write {
			return s, nil
		}
		if err := os.MkdirAll(state, 0o755); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	s.Root, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, err
	}
	state = filepath.Join(s.Root, ".dspm")
	// The persistent lock file is never removed, including when the store is empty.
	s.lock = flock.New(filepath.Join(state, "lock"))
	var locked bool
	if write {
		locked, err = s.lock.TryLock()
	} else {
		locked, err = s.lock.TryRLock()
	}
	if err != nil || !locked {
		if err == nil {
			err = fmt.Errorf("package store is locked by another process")
		}
		return nil, err
	}
	failed := true
	defer func() {
		if failed {
			s.Close()
		}
	}()
	s.db, err = packagedatabase.Open(filepath.Join(state, "packages.db"), false)
	if err != nil {
		return nil, err
	}
	if s.db == nil && write {
		ids, err := s.transactionIDs()
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			if _, err := os.Stat(filepath.Join(s.txRoot(), id, "journal.json")); err == nil {
				return nil, fmt.Errorf("package database is missing; cannot determine transaction commit state")
			} else if !os.IsNotExist(err) {
				return nil, err
			}
		}
		s.db, err = packagedatabase.Open(filepath.Join(state, "packages.db"), true)
		if err != nil {
			return nil, err
		}
	}
	pending, err := s.pending()
	if err != nil {
		return nil, err
	}
	if pending && !write {
		s.db.Close()
		s.db = nil
		if err = s.lock.Unlock(); err != nil {
			return nil, err
		}
		locked, err = s.lock.TryLock()
		if err != nil || !locked {
			if err == nil {
				err = fmt.Errorf("package store is locked during recovery")
			}
			return nil, err
		}
		s.db, err = packagedatabase.Open(filepath.Join(state, "packages.db"), false)
		if err != nil {
			return nil, err
		}
	}
	if pending {
		if err = s.recover(); err != nil {
			return nil, err
		}
	}
	failed = false
	return s, nil
}
func (s *Session) Close() error {
	var err error
	// Only private, unjournaled staging may be discarded on a normal early return.
	if s.transaction != "" {
		filename := filepath.Join(s.txRoot(), s.transaction, "journal.json")
		if _, e := os.Stat(filename); os.IsNotExist(e) {
			err = s.removeTransaction(s.transaction)
		}
	}
	if e := s.db.Close(); err == nil {
		err = e
	}
	if s.lock != nil {
		if e := s.lock.Unlock(); err == nil {
			err = e
		}
	}
	return err
}
func (s *Session) Records() ([]packagedatabase.Record, error) { return s.db.Records() }
func (s *Session) Load(record packagedatabase.Record) (packageinfo.PackageManifest, error) {
	if record.Directory != DirectoryName(record.Identity) {
		return packageinfo.PackageManifest{}, fmt.Errorf("invalid installed directory for %s", record.String())
	}
	root := filepath.Join(s.Root, record.Directory)
	d, err := packagearchive.OpenDirectory(root)
	if err != nil {
		return packageinfo.PackageManifest{}, err
	}
	defer d.Close()
	p, err := packageinfo.Load(d, root)
	if err == nil && p.Identity.String() != record.Identity.String() {
		err = fmt.Errorf("installed manifest identity disagrees with database")
	}
	return p, err
}

package localstore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/diffscope/diffscope-package-manager/packageinfo"
	"os"
	"path/filepath"
)

func (s *Session) transactionIDs() ([]string, error) {
	entries, err := os.ReadDir(s.txRoot())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, e := range entries {
		if !e.IsDir() || !validTx(e.Name()) {
			return nil, fmt.Errorf("unexpected transaction entry %q", e.Name())
		}
		ids = append(ids, e.Name())
	}
	return ids, nil
}
func (s *Session) pending() (bool, error) {
	ids, err := s.transactionIDs()
	if err != nil {
		return false, err
	}
	if len(ids) > 0 {
		return true, nil
	}
	if s.db != nil {
		ids, err = s.db.PendingCommits()
		return len(ids) > 0, err
	}
	return false, nil
}
func (s *Session) recover() error {
	ids, err := s.transactionIDs()
	if err != nil {
		return err
	}
	pendingCleanup := false
	for _, id := range ids {
		b, err := os.ReadFile(filepath.Join(s.txRoot(), id, "journal.json"))
		if os.IsNotExist(err) {
			if err := s.removeTransaction(id); err != nil {
				return err
			}
			s.Diagnostics = append(s.Diagnostics, "removed interrupted staging "+id)
			continue
		}
		if err != nil {
			return err
		}
		var j journal
		if len(b) > 16<<20 {
			return fmt.Errorf("transaction journal exceeds limit")
		}
		if err = json.Unmarshal(b, &j); err != nil {
			return fmt.Errorf("invalid transaction journal: %w", err)
		}
		if err = s.validateJournal(j, id); err != nil {
			return err
		}
		committed, err := s.db.Committed(id)
		if err != nil {
			return err
		}
		if committed {
			if err = s.cleanup(j); err != nil {
				s.Diagnostics = append(s.Diagnostics, "committed cleanup pending: "+err.Error())
				pendingCleanup = true
				continue
			}
			s.Diagnostics = append(s.Diagnostics, "completed committed transaction cleanup "+id)
		} else {
			if err = s.rollback(j); err != nil {
				return err
			}
			s.Diagnostics = append(s.Diagnostics, "rolled back interrupted transaction "+id)
		}
	}
	if !pendingCleanup && s.db != nil {
		if err = s.db.ClearOrphanCommits(); err != nil {
			s.Diagnostics = append(s.Diagnostics, "commit marker cleanup pending: "+err.Error())
		}
	}
	return nil
}
func (s *Session) validateJournal(j journal, id string) error {
	if j.ID != id || !validTx(id) {
		return fmt.Errorf("invalid transaction identity")
	}
	seen := map[string]bool{}
	for i, m := range j.Moves {
		if m.Directory != DirectoryName(m.Identity) || !packageinfo.IsValidPackageIdentifier(m.Identity.ID) || seen[m.Directory] || m.Backup != fmt.Sprintf("backup-%d", i) || m.Stage != "" && !validStage(m.Stage) {
			return fmt.Errorf("invalid transaction operation")
		}
		seen[m.Directory] = true
	}
	return nil
}
func (s *Session) rollback(j journal) error {
	base := filepath.Join(s.txRoot(), j.ID)
	for i := len(j.Moves) - 1; i >= 0; i-- {
		m := j.Moves[i]
		final := filepath.Join(s.Root, m.Directory)
		backup := filepath.Join(base, m.Backup)
		if _, err := os.Lstat(backup); err == nil {
			if err = s.removePackage(m.Directory); err != nil {
				return err
			}
			if err = os.Rename(backup, final); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		} else if m.Old {
			if _, err = os.Stat(final); err != nil {
				return fmt.Errorf("required original package is missing: %w", err)
			}
		} else {
			if err = s.removePackage(m.Directory); err != nil {
				return err
			}
		}
	}
	return s.removeTransaction(j.ID)
}
func (s *Session) cleanup(j journal) error {
	if err := s.checkpoint("cleanup"); err != nil {
		return err
	}
	if err := s.removeTransaction(j.ID); err != nil {
		return err
	}
	return s.db.Forget(j.ID)
}
func (s *Session) removePackage(directory string) error {
	b, err := hex.DecodeString(directory)
	if err != nil || len(b) != sha256.Size {
		return fmt.Errorf("unsafe package removal path")
	}
	target := filepath.Join(s.Root, directory)
	relative, err := filepath.Rel(s.Root, target)
	if err != nil || !filepath.IsLocal(relative) || relative == "." {
		return fmt.Errorf("unsafe package removal path")
	}
	return os.RemoveAll(target)
}
func (s *Session) removeTransaction(id string) error {
	if !validTx(id) {
		return fmt.Errorf("unsafe transaction removal path")
	}
	target := filepath.Join(s.txRoot(), id)
	relative, err := filepath.Rel(s.txRoot(), target)
	if err != nil || !filepath.IsLocal(relative) || relative == "." {
		return fmt.Errorf("unsafe transaction removal path")
	}
	return os.RemoveAll(target)
}

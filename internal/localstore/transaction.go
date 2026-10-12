package localstore

import (
	"context"
	"errors"
	"fmt"
	"github.com/diffscope/diffscope-package-manager/packagedatabase"
	"github.com/diffscope/diffscope-package-manager/packageinfo"
	"os"
	"path/filepath"
	"sort"
)

type Put struct {
	Record packagedatabase.Record
	Stage  string
}
type move struct {
	Identity  packageinfo.Identity `json:"identity"`
	Directory string               `json:"directory"`
	Old       bool                 `json:"old"`
	Stage     string               `json:"stage,omitempty"`
	Backup    string               `json:"backup"`
}
type journal struct {
	ID    string `json:"id"`
	Moves []move `json:"moves"`
}

func (s *Session) checkpoint(point string) error {
	if s.Fault != nil {
		return s.Fault(point)
	}
	return nil
}

func (s *Session) Commit(ctx context.Context, remove []packagedatabase.Record, put []Put) error {
	if len(remove) == 0 && len(put) == 0 {
		return nil
	}
	if err := s.ensureTransaction(); err != nil {
		return err
	}
	j := journal{ID: s.transaction}
	byID := map[string]*move{}
	for _, r := range remove {
		if r.Directory != DirectoryName(r.Identity) {
			return fmt.Errorf("The package directory is invalid.")
		}
		m := move{Identity: r.Identity, Directory: r.Directory, Old: true}
		byID[r.String()] = &m
	}
	for _, p := range put {
		if p.Record.Directory != DirectoryName(p.Record.Identity) || !validStage(p.Stage) {
			return fmt.Errorf("The staged package is invalid.")
		}
		m := byID[p.Record.String()]
		if m == nil {
			m = &move{Identity: p.Record.Identity, Directory: p.Record.Directory}
			byID[p.Record.String()] = m
		}
		m.Stage = p.Stage
	}
	keys := make([]string, 0, len(byID))
	for k := range byID {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		m := *byID[k]
		m.Backup = fmt.Sprintf("backup-%d", i)
		st, err := os.Lstat(filepath.Join(s.Root, m.Directory))
		if m.Old {
			if err != nil {
				return err
			}
			if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("The installed package is not an ordinary directory.")
			}
		} else if err == nil {
			return fmt.Errorf("An unregistered directory conflicts with package %s.", k)
		} else if !os.IsNotExist(err) {
			return err
		}
		j.Moves = append(j.Moves, m)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.writeJournal(j); err != nil {
		return err
	}
	rollback := func(primary error) error {
		if err := s.rollback(j); err != nil {
			return errors.Join(primary, fmt.Errorf("Rollback failed and requires recovery: %w", err))
		}
		return primary
	}
	if err := s.checkpoint("journal"); err != nil {
		return rollback(err)
	}
	base := filepath.Join(s.txRoot(), j.ID)
	for _, m := range j.Moves {
		if err := ctx.Err(); err != nil {
			return rollback(err)
		}
		if m.Old {
			if err := os.Rename(filepath.Join(s.Root, m.Directory), filepath.Join(base, m.Backup)); err != nil {
				return rollback(err)
			}
			if err := s.checkpoint("backup"); err != nil {
				return rollback(err)
			}
		}
		if m.Stage != "" {
			if err := os.Rename(filepath.Join(base, m.Stage), filepath.Join(s.Root, m.Directory)); err != nil {
				return rollback(err)
			}
			if err := s.checkpoint("promote"); err != nil {
				return rollback(err)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return rollback(err)
	}
	if err := s.checkpoint("before-commit"); err != nil {
		return rollback(err)
	}
	identities := make([]packageinfo.Identity, 0, len(remove))
	for _, r := range remove {
		identities = append(identities, r.Identity)
	}
	records := make([]packagedatabase.Record, 0, len(put))
	for _, p := range put {
		records = append(records, p.Record)
	}
	if err := s.db.Apply(j.ID, identities, records); err != nil {
		// A COMMIT I/O error can have an uncertain outcome. Recovery requires
		// commit-marker verification through a fresh connection.
		committed, checkErr := s.db.ConfirmCommitted(j.ID)
		if checkErr != nil {
			return errors.Join(err, fmt.Errorf("The commit state cannot be determined: %w", checkErr))
		}
		if !committed {
			return rollback(err)
		}
		s.Diagnostics = append(s.Diagnostics, "The database commit succeeded despite the reported error: "+err.Error())
	}
	if err := s.checkpoint("committed"); err != nil {
		s.Diagnostics = append(s.Diagnostics, "The transaction committed. Cleanup remains pending: "+err.Error())
		return nil
	}
	if err := s.cleanup(j); err != nil {
		s.Diagnostics = append(s.Diagnostics, "The transaction committed. Cleanup remains pending: "+err.Error())
	}
	return nil
}

// Package packagedatabase owns the SQLite index. ORM handles never escape this package.
package packagedatabase

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"

	"github.com/diffscope/diffscope-package-manager/packageinfo"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const SchemaVersion = 241

type Store struct {
	db       *gorm.DB
	filename string
}
type ContributionIndex struct {
	Category  string           `json:"category"`
	ID        string           `json:"id"`
	Interface string           `json:"interface,omitempty"`
	Level     string           `json:"level,omitempty"`
	Variant   string           `json:"variant,omitempty"`
	Name      packageinfo.Text `json:"name,omitempty"`
}
type Record struct {
	packageinfo.Identity
	CompatVersion packageinfo.Version      `json:"compatVersion"`
	Hash          string                   `json:"hash"`
	InstalledAt   int64                    `json:"installedAt"`
	Directory     string                   `json:"directory"`
	Dependencies  []packageinfo.Dependency `json:"dependencies"`
	Name          packageinfo.Text         `json:"name,omitempty"`
	Description   packageinfo.Text         `json:"description,omitempty"`
	Vendor        packageinfo.Text         `json:"vendor,omitempty"`
	Contributions []ContributionIndex      `json:"contributions"`
}

func Index(p packageinfo.PackageManifest, hash, dir string, at int64) Record {
	r := Record{Identity: p.Identity, CompatVersion: p.CompatVersion, Hash: hash, Directory: dir, InstalledAt: at, Dependencies: p.Dependencies, Name: p.Name, Description: p.Description, Vendor: p.Vendor, Contributions: []ContributionIndex{}}
	for _, c := range p.Contributions {
		index := ContributionIndex{Category: c.Category, ID: c.ID}
		if c.Category == "singer" && c.SingerDeclaration != nil {
			index.Interface, index.Level, index.Variant, index.Name = c.Interface, c.Level.String(), c.Variant, c.Name
		}
		r.Contributions = append(r.Contributions, index)
	}
	return r
}

// Open never initializes an existing unrecognized database. Missing read stores are empty.
func Open(filename string, create bool) (*Store, error) {
	_, err := os.Stat(filename)
	missing := os.IsNotExist(err)
	if err != nil && !missing {
		return nil, err
	}
	if missing && !create {
		return nil, nil
	}
	absolute, err := filepath.Abs(filename)
	if err != nil {
		return nil, err
	}
	uriPath := filepath.ToSlash(absolute)
	if uriPath[0] != '/' {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	q := u.Query()
	q.Set("_foreign_keys", "on")
	q.Set("_busy_timeout", "0")
	if missing {
		q.Set("mode", "rwc")
	} else {
		q.Set("mode", "rw")
	}
	u.RawQuery = q.Encode()
	db, err := gorm.Open(sqlite.Open(u.String()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), SkipDefaultTransaction: true})
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, filename: filename}
	failed := true
	defer func() {
		if failed {
			s.Close()
		}
	}()
	var version int
	if err = db.Raw("PRAGMA user_version").Scan(&version).Error; err != nil {
		return nil, err
	}
	if missing {
		if err = s.initialize(); err != nil {
			return nil, err
		}
	} else if version != SchemaVersion {
		return nil, fmt.Errorf("unsupported package database schema %d (expected %d)", version, SchemaVersion)
	}
	failed = false
	return s, nil
}
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	db, err := s.db.DB()
	if err != nil {
		return err
	}
	return db.Close()
}
func (s *Store) initialize() error {
	statements := []string{
		`CREATE TABLE packages (id TEXT COLLATE BINARY NOT NULL, version TEXT NOT NULL, compat_version TEXT NOT NULL, hash TEXT NOT NULL, installed_at INTEGER NOT NULL, directory TEXT NOT NULL UNIQUE, PRIMARY KEY(id,version))`,
		`CREATE TABLE dependencies (package_id TEXT NOT NULL, package_version TEXT NOT NULL, id TEXT NOT NULL, version TEXT NOT NULL, PRIMARY KEY(package_id,package_version,id), FOREIGN KEY(package_id,package_version) REFERENCES packages(id,version) ON DELETE CASCADE)`,
		`CREATE TABLE contributions (package_id TEXT NOT NULL, package_version TEXT NOT NULL, category TEXT NOT NULL, id TEXT NOT NULL, interface TEXT NOT NULL, level TEXT NOT NULL, variant TEXT NOT NULL, PRIMARY KEY(package_id,package_version,category,id), CHECK(category='singer' OR (interface='' AND level='' AND variant='')), FOREIGN KEY(package_id,package_version) REFERENCES packages(id,version) ON DELETE CASCADE)`,
		`CREATE TABLE package_texts (package_id TEXT NOT NULL, package_version TEXT NOT NULL, language TEXT NOT NULL, name TEXT, description TEXT, vendor TEXT, PRIMARY KEY(package_id,package_version,language), FOREIGN KEY(package_id,package_version) REFERENCES packages(id,version) ON DELETE CASCADE)`,
		`CREATE TABLE contribution_names (package_id TEXT NOT NULL, package_version TEXT NOT NULL, category TEXT NOT NULL, contribution_id TEXT NOT NULL, language TEXT NOT NULL, name TEXT NOT NULL, PRIMARY KEY(package_id,package_version,category,contribution_id,language), CHECK(category='singer'), FOREIGN KEY(package_id,package_version,category,contribution_id) REFERENCES contributions(package_id,package_version,category,id) ON DELETE CASCADE)`,
		`CREATE TABLE transaction_commits (id TEXT PRIMARY KEY NOT NULL)`,
		fmt.Sprintf("PRAGMA user_version = %d", SchemaVersion),
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		for _, statement := range statements {
			if err := tx.Exec(statement).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

type packageRow struct {
	ID, Version, CompatVersion, Hash, Directory string
	InstalledAt                                 int64
}
type dependencyRow struct{ PackageID, PackageVersion, ID, Version string }
type contributionRow struct{ PackageID, PackageVersion, Category, ID, Interface, Level, Variant string }
type textRow struct {
	PackageID, PackageVersion, Language string
	Name, Description, Vendor           *string
}
type nameRow struct{ PackageID, PackageVersion, Category, ContributionID, Language, Name string }

func (s *Store) Records() ([]Record, error) {
	result := []Record{}
	if s == nil {
		return result, nil
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var p []packageRow
		var d []dependencyRow
		var c []contributionRow
		var t []textRow
		var n []nameRow
		for _, query := range []struct {
			table string
			dest  any
		}{{"packages", &p}, {"dependencies", &d}, {"contributions", &c}, {"package_texts", &t}, {"contribution_names", &n}} {
			if err := tx.Table(query.table).Find(query.dest).Error; err != nil {
				return err
			}
		}
		indexes := map[string]int{}
		for _, row := range p {
			v, err := packageinfo.ParseVersion(row.Version)
			if err != nil {
				return err
			}
			compat, err := packageinfo.ParseVersion(row.CompatVersion)
			if err != nil {
				return err
			}
			r := Record{Identity: packageinfo.Identity{ID: row.ID, Version: v}, CompatVersion: compat, Hash: row.Hash, InstalledAt: row.InstalledAt, Directory: row.Directory, Dependencies: []packageinfo.Dependency{}, Contributions: []ContributionIndex{}}
			indexes[r.String()] = len(result)
			result = append(result, r)
		}
		owner := func(id, version string) (*Record, error) {
			i, ok := indexes[id+"="+version]
			if !ok {
				return nil, fmt.Errorf("orphaned package index")
			}
			return &result[i], nil
		}
		for _, row := range d {
			r, err := owner(row.PackageID, row.PackageVersion)
			if err != nil {
				return err
			}
			v, err := packageinfo.ParseVersion(row.Version)
			if err != nil {
				return err
			}
			r.Dependencies = append(r.Dependencies, packageinfo.Dependency{ID: row.ID, Version: v})
		}
		for _, row := range t {
			r, err := owner(row.PackageID, row.PackageVersion)
			if err != nil {
				return err
			}
			for _, field := range []struct {
				dest  *packageinfo.Text
				value *string
			}{{&r.Name, row.Name}, {&r.Description, row.Description}, {&r.Vendor, row.Vendor}} {
				if field.value != nil {
					if *field.dest == nil {
						*field.dest = packageinfo.Text{}
					}
					(*field.dest)[row.Language] = *field.value
				}
			}
		}
		for _, row := range c {
			r, err := owner(row.PackageID, row.PackageVersion)
			if err != nil {
				return err
			}
			r.Contributions = append(r.Contributions, ContributionIndex{Category: row.Category, ID: row.ID, Interface: row.Interface, Level: row.Level, Variant: row.Variant})
		}
		for _, row := range n {
			r, err := owner(row.PackageID, row.PackageVersion)
			if err != nil {
				return err
			}
			found := false
			for i := range r.Contributions {
				c := &r.Contributions[i]
				if c.Category == row.Category && c.ID == row.ContributionID {
					if c.Name == nil {
						c.Name = packageinfo.Text{}
					}
					c.Name[row.Language] = row.Name
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("orphaned contribution name")
			}
		}
		return nil
	})
	sort.Slice(result, func(i, j int) bool {
		if result[i].ID != result[j].ID {
			return result[i].ID < result[j].ID
		}
		return result[i].Version.Compare(result[j].Version) > 0
	})
	for i := range result {
		sort.Slice(result[i].Contributions, func(a, b int) bool {
			x, y := result[i].Contributions[a], result[i].Contributions[b]
			if x.Category != y.Category {
				return x.Category < y.Category
			}
			return x.ID < y.ID
		})
	}
	return result, err
}
func (s *Store) Apply(id string, remove []packageinfo.Identity, put []Record) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		for _, r := range remove {
			if err := tx.Exec("DELETE FROM packages WHERE id=? AND version=?", r.ID, r.Version.String()).Error; err != nil {
				return err
			}
		}
		for _, r := range put {
			if err := writeRecord(tx, r); err != nil {
				return err
			}
		}
		return tx.Exec("INSERT INTO transaction_commits(id) VALUES (?)", id).Error
	})
}
func writeRecord(tx *gorm.DB, r Record) error {
	id, version := r.ID, r.Version.String()
	if err := tx.Exec("INSERT INTO packages(id,version,compat_version,hash,installed_at,directory) VALUES (?,?,?,?,?,?)", id, version, r.CompatVersion.String(), r.Hash, r.InstalledAt, r.Directory).Error; err != nil {
		return err
	}
	for _, d := range r.Dependencies {
		if err := tx.Exec("INSERT INTO dependencies(package_id,package_version,id,version) VALUES (?,?,?,?)", id, version, d.ID, d.Version.String()).Error; err != nil {
			return err
		}
	}
	languages := map[string]bool{}
	for _, m := range []packageinfo.Text{r.Name, r.Description, r.Vendor} {
		for k := range m {
			languages[k] = true
		}
	}
	nullable := func(m packageinfo.Text, key string) any {
		if value, ok := m[key]; ok {
			return value
		}
		return nil
	}
	for k := range languages {
		if err := tx.Exec("INSERT INTO package_texts(package_id,package_version,language,name,description,vendor) VALUES (?,?,?,?,?,?)", id, version, k, nullable(r.Name, k), nullable(r.Description, k), nullable(r.Vendor, k)).Error; err != nil {
			return err
		}
	}
	for _, c := range r.Contributions {
		if err := tx.Exec("INSERT INTO contributions(package_id,package_version,category,id,interface,level,variant) VALUES (?,?,?,?,?,?,?)", id, version, c.Category, c.ID, c.Interface, c.Level, c.Variant).Error; err != nil {
			return err
		}
		for lang, name := range c.Name {
			if err := tx.Exec("INSERT INTO contribution_names(package_id,package_version,category,contribution_id,language,name) VALUES (?,?,?,?,?,?)", id, version, c.Category, c.ID, lang, name).Error; err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *Store) Committed(id string) (bool, error) {
	if s == nil {
		return false, fmt.Errorf("transaction journal has no package database")
	}
	var count int64
	err := s.db.Table("transaction_commits").Where("id=?", id).Count(&count).Error
	return count != 0, err
}
func (s *Store) Forget(id string) error {
	return s.db.Exec("DELETE FROM transaction_commits WHERE id=?", id).Error
}
func (s *Store) ClearOrphanCommits() error { return s.db.Exec("DELETE FROM transaction_commits").Error }
func (s *Store) PendingCommits() ([]string, error) {
	var ids []string
	err := s.db.Table("transaction_commits").Pluck("id", &ids).Error
	return ids, err
}

// ConfirmCommitted checks a possibly uncertain COMMIT on a fresh connection.
func (s *Store) ConfirmCommitted(id string) (bool, error) {
	fresh, err := Open(s.filename, false)
	if err != nil {
		return false, err
	}
	if fresh == nil {
		return false, fmt.Errorf("database disappeared while confirming commit")
	}
	defer fresh.Close()
	return fresh.Committed(id)
}

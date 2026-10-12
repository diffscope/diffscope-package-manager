package packagedatabase

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/diffscope/diffscope-package-manager/packageinfo"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func v(s string) packageinfo.Version { r, _ := packageinfo.ParseVersion(s); return r }
func TestSchemaAndMinimalIndex(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "index.db")
	store, err := Open(filename, false)
	if err != nil || store != nil {
		t.Fatal(store, err)
	}
	if _, err := os.Stat(filename); !os.IsNotExist(err) {
		t.Fatal("read initialized database")
	}
	store, err = Open(filename, true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	record := Record{Identity: packageinfo.Identity{ID: "Voice", Version: v("1")}, CompatVersion: v("0"), Hash: "hash", Directory: "dir", Name: packageinfo.Text{"_": "", "zh-CN": "V", "ZH-cn": "W"}, Dependencies: []packageinfo.Dependency{{ID: "unresolved-at-index-layer", Version: v("1")}}, Contributions: []ContributionIndex{{Category: "singer", ID: "main", Name: packageinfo.Text{"_": ""}}, {Category: "vendor.custom", ID: "data"}}}
	if err := store.Apply("tx", nil, []Record{record}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Records()
	if err != nil || len(got) != 1 || got[0].Name["_"] != "" || got[0].Name["ZH-cn"] != "W" || got[0].Contributions[0].Name["_"] != "" || len(got[0].Contributions[1].Name) != 0 {
		t.Fatal(got, err)
	}
	if ok, err := store.Committed("tx"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	// Every pool connection requires foreign key enforcement.
	sqlDB, _ := store.db.DB()
	sqlDB.SetMaxIdleConns(0)
	if err := store.db.Exec("INSERT INTO dependencies VALUES ('missing','1.0.0.0','dep','1.0.0.0')").Error; err == nil {
		t.Fatal("foreign keys disabled on new connection")
	}
	if err := store.Apply("remove", []packageinfo.Identity{record.Identity}, nil); err != nil {
		t.Fatal(err)
	}
	var count int64
	store.db.Table("contribution_names").Count(&count)
	if count != 0 {
		t.Fatal("cascade failed")
	}
}

func TestOpaqueIndexAndConstraints(t *testing.T) {
	p := packageinfo.PackageManifest{Identity: packageinfo.Identity{ID: "voice", Version: v("1")}, Contributions: []packageinfo.Contribution{{
		Category: "inference", ID: "note", Entry: map[string]any{"name": "EntryName"},
		// Caller-supplied declarations must satisfy the opaque category constraints.
		SingerDeclaration: &packageinfo.SingerDeclaration{Name: packageinfo.Text{"_": "Hidden"}, ModuleMetadata: &packageinfo.ModuleMetadata{Interface: "hidden.Contract", Level: 1, Variant: "hidden"}},
	}}}
	r := Index(p, "hash", "dir", 123)
	if !reflect.DeepEqual(r.Contributions, []ContributionIndex{{Category: "inference", ID: "note"}}) {
		t.Fatal("opaque index contains declaration fields", r.Contributions)
	}
	s, err := Open(filepath.Join(t.TempDir(), "index.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Apply("install", nil, []Record{r}); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"interface", "level", "variant"} {
		if err := s.db.Exec(fmt.Sprintf("UPDATE contributions SET %s='hidden' WHERE category='inference'", field)).Error; err == nil {
			t.Fatalf("opaque %s accepted", field)
		}
	}
	if err := s.db.Exec("INSERT INTO contribution_names VALUES ('voice','1.0.0.0','inference','note','_','Hidden')").Error; err == nil {
		t.Fatal("opaque name accepted")
	}
	bad := r
	bad.ID, bad.Directory = "bad", "bad-dir"
	bad.Contributions = []ContributionIndex{{Category: "vendor.custom", ID: "data", Name: packageinfo.Text{"_": "Hidden"}}}
	if err := s.Apply("bad-install", nil, []Record{bad}); err == nil {
		t.Fatal("invalid record accepted")
	}
	got, err := s.Records()
	if err != nil || len(got) != 1 || got[0].ID != "voice" {
		t.Fatal("failed write was not rolled back", got, err)
	}
	if committed, err := s.Committed("bad-install"); err != nil || committed {
		t.Fatal(committed, err)
	}
}

// Executes documented scripts on one connection and returns the first error.
// A failed statement triggers rollback before execution returns.
func executeScript(db *sql.DB, script string) error {
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var lines []string
	for _, line := range strings.Split(script, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "--") {
			lines = append(lines, line)
		}
	}
	for _, statement := range strings.Split(strings.Join(lines, "\n"), ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			conn.ExecContext(ctx, "ROLLBACK")
			return err
		}
	}
	return nil
}

func TestDocumentedSchema(t *testing.T) {
	ddl, err := os.ReadFile("../docs/sql/installed-packages.sql")
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(t.TempDir(), "index.db")
	raw, err := sql.Open("sqlite3", filename)
	if err != nil {
		t.Fatal(err)
	}
	if err = executeScript(raw, string(ddl)); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	s, err := Open(filename, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	record := Record{Identity: packageinfo.Identity{ID: "voice", Version: v("2")}, CompatVersion: v("1"), Hash: "hash", Directory: "dir", InstalledAt: 123,
		Contributions: []ContributionIndex{{Category: "singer", ID: "main", Interface: "Singer", Level: 2, Variant: "v"}, {Category: "vendor.custom", ID: "inline"}}}
	if err = s.Apply("install", nil, []Record{record}); err != nil {
		t.Fatal(err)
	}
	var storage string
	if err = s.db.Raw("SELECT typeof(level) FROM contributions WHERE category='singer'").Scan(&storage).Error; err != nil || storage != "integer" {
		t.Fatal(storage, err)
	}
	records, err := s.Records()
	if err != nil || len(records) != 1 || records[0].Contributions[0].Level != 2 || records[0].Contributions[1].Level != 0 {
		t.Fatal(records, err)
	}
	encoded, err := json.Marshal(records[0].Contributions)
	if err != nil || !strings.Contains(string(encoded), `"level":2`) || strings.Count(string(encoded), `"level"`) != 1 {
		t.Fatal(string(encoded), err)
	}
}
func TestRejectOldDatabaseWithoutMigration(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "old.db")
	db, err := gorm.Open(sqlite.Open(filename), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	db.Exec("CREATE TABLE old_table (id TEXT)")
	raw, _ := db.DB()
	raw.Close()
	if s, err := Open(filename, true); err == nil {
		s.Close()
		t.Fatal("old schema accepted")
	}
	db, err = gorm.Open(sqlite.Open(filename), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { r, _ := db.DB(); r.Close() }()
	if db.Migrator().HasTable("packages") {
		t.Fatal("old database mutated")
	}
}

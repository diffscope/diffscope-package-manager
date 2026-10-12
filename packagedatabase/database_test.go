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
	// Verify FK configuration on newly-created pool connections, not just the first one.
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
		// Even a caller-supplied declaration must not cross the category boundary.
		SingerDeclaration: &packageinfo.SingerDeclaration{Name: packageinfo.Text{"_": "Hidden"}, ModuleMetadata: &packageinfo.ModuleMetadata{Interface: "hidden.Contract", Level: "1", Variant: "hidden"}},
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

// Execute the documented scripts on one connection, stopping and rolling back
// on the first error, including failures before the final COMMIT.
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

func TestDocumentedOpaqueIndexMigration(t *testing.T) {
	ddl, err := os.ReadFile("../docs/sql/installed-packages.sql")
	if err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../docs/sql/migrate-240-to-241.sql")
	if err != nil {
		t.Fatal(err)
	}
	// Schema 240 had the same columns, without opaque-category constraints.
	var oldDDL []string
	for _, line := range strings.Split(string(ddl), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "CHECK") {
			oldDDL = append(oldDDL, strings.ReplaceAll(line, "241", "240"))
		}
	}
	filename := filepath.Join(t.TempDir(), "index.db")
	db, err := gorm.Open(sqlite.Open(filename+"?_foreign_keys=on"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if err := executeScript(raw, strings.Join(oldDDL, "\n")); err != nil {
		t.Fatal(err)
	}
	old := &Store{db: db}
	record := Record{Identity: packageinfo.Identity{ID: "voice", Version: v("2")}, CompatVersion: v("1"), Hash: "hash", Directory: "dir", InstalledAt: 123,
		Name: packageinfo.Text{"_": "", "zh-CN": "Voice"}, Dependencies: []packageinfo.Dependency{{ID: "dependency", Version: v("1")}},
		Contributions: []ContributionIndex{
			{Category: "inference", ID: "note", Interface: "Note", Level: "1", Variant: "old", Name: packageinfo.Text{"_": "OldName", "zh-CN": "OldTranslation"}},
			{Category: "singer", ID: "main", Interface: "Singer", Level: "123456789012345678901234567890", Variant: "v", Name: packageinfo.Text{"_": "", "en": "Singer"}},
			{Category: "vendor.custom", ID: "inline", Name: packageinfo.Text{"_": "inline"}},
		}}
	if err := old.Apply("pending-commit", nil, []Record{record}); err != nil {
		t.Fatal(err)
	}
	wantRecords, err := old.Records()
	if err != nil {
		t.Fatal(err)
	}
	if err := executeScript(raw, string(migration)); err != nil {
		t.Fatal(err)
	}
	s, err := Open(filename, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Records()
	wantRecords[0].Contributions[0] = ContributionIndex{Category: "inference", ID: "note"}
	wantRecords[0].Contributions[2] = ContributionIndex{Category: "vendor.custom", ID: "inline"}
	if err != nil || !reflect.DeepEqual(got, wantRecords) {
		actual, _ := json.Marshal(got)
		want, _ := json.Marshal(wantRecords)
		t.Fatalf("migration changed preserved data: got %s, want %s, error %v", actual, want, err)
	}
	if committed, err := s.Committed("pending-commit"); err != nil || !committed {
		t.Fatal("commit marker changed", committed, err)
	}
	var violations []map[string]any
	if err := s.db.Raw("PRAGMA foreign_key_check").Scan(&violations).Error; err != nil || len(violations) != 0 {
		t.Fatal(violations, err)
	}
	if err := s.db.Exec("UPDATE contributions SET interface='Hidden' WHERE category='inference'").Error; err == nil {
		t.Fatal("migration did not add constraint")
	}
	if err := s.db.Exec("INSERT INTO contribution_names VALUES ('voice','2.0.0.0','inference','note','_','Hidden')").Error; err == nil {
		t.Fatal("migration did not restrict names")
	}
	if err := executeScript(raw, string(migration)); err == nil {
		t.Fatal("migration accepted wrong source version")
	}
	after, err := s.Records()
	if err != nil || !reflect.DeepEqual(after, got) {
		t.Fatal("failed repeat migration mutated database", after, err)
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

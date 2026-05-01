package packagedatabase

import (
	"testing"

	"diffscope-package-manager/packagedatabase/model"
)

func TestOpenMigratesDatabase(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	var foreignKeys int
	if err := db.Raw("PRAGMA foreign_keys").Scan(&foreignKeys).Error; err != nil {
		t.Fatalf("query foreign_keys pragma: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys pragma = %d, want 1", foreignKeys)
	}

	for _, table := range model.Tables() {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("missing migrated table for %T", table)
		}
	}
}

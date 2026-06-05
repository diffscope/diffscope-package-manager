package packagedatabase

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/diffscope/diffscope-package-manager/packagedatabase/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// Open opens the SQLite package database at databasePath and migrates the schema.
func Open(databasePath string) (*gorm.DB, error) {
	if databasePath == "" {
		return nil, fmt.Errorf("packagedatabase: database path is required")
	}

	if databasePath != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(databasePath), 0o755); err != nil {
			return nil, fmt.Errorf("packagedatabase: create database directory: %w", err)
		}
	}

	db, err := gorm.Open(sqlite.Open(databasePath), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("packagedatabase: open sqlite database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("packagedatabase: get sqlite database handle: %w", err)
	}

	if _, err := sqlDB.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return nil, fmt.Errorf("packagedatabase: enable foreign keys: %w", err)
	}

	if err := db.AutoMigrate(model.Tables()...); err != nil {
		return nil, fmt.Errorf("packagedatabase: migrate database: %w", err)
	}

	return db, nil
}

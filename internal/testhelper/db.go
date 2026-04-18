// Package testhelper provides shared utilities for integration tests that
// require a real PostgreSQL database. Tests that call OpenTestDB will be
// skipped automatically when TEST_DATABASE_URL is not set, so the unit-test
// suite remains runnable without any database infrastructure.
package testhelper

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
)

// OpenTestDB opens a connection to the integration-test database.
// If TEST_DATABASE_URL is not set the test is skipped — integration tests are opt-in.
func OpenTestDB(t *testing.T) *sqlx.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping integration test")
	}
	db, err := sqlx.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("testhelper: open db: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("testhelper: ping db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// MustRunMigrations applies all SQL migrations up to the latest version.
// Uses os.DirFS + iofs source to avoid file:// URL parsing issues on Windows.
func MustRunMigrations(t *testing.T, db *sqlx.DB) {
	t.Helper()
	driver, err := postgres.WithInstance(db.DB, &postgres.Config{})
	if err != nil {
		t.Fatalf("testhelper: migrate driver: %v", err)
	}

	dir := migrationsDir()
	fsys := os.DirFS(dir)
	src, err := iofs.New(fsys, ".")
	if err != nil {
		t.Fatalf("testhelper: migrate iofs source: %v", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, "postgres", driver)
	if err != nil {
		t.Fatalf("testhelper: migrate init: %v", err)
	}
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("testhelper: migrate up: %v", err)
	}
}

// TruncateAll removes all rows from every application table between tests.
// The schema is preserved; only data is deleted.
func TruncateAll(t *testing.T, db *sqlx.DB) {
	t.Helper()
	// Order respects foreign-key constraints (children before parents).
	tables := []string{
		"merge_records",
		"snapshots",
		"jobs",
		"duplicate_groups",
		"customer_cache",
		"merchant_settings",
		"merchants",
	}
	for _, tbl := range tables {
		if _, err := db.Exec("TRUNCATE " + tbl + " CASCADE"); err != nil {
			t.Fatalf("testhelper: truncate %s: %v", tbl, err)
		}
	}
}

// migrationsDir returns the absolute path to the SQL migrations directory.
// runtime.Caller(0) gives the compile-time path of this file, so the result
// is always correct regardless of the working directory at test time.
func migrationsDir() string {
	_, thisFile, _, _ := runtime.Caller(0)
	// thisFile = .../internal/testhelper/db.go
	// migrations = .../internal/db/migrations
	dir := filepath.Join(filepath.Dir(thisFile), "..", "db", "migrations")
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	return abs
}

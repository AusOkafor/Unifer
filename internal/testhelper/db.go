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
	_ "github.com/golang-migrate/migrate/v4/source/file"
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
// Uses the runtime file path of this helper to locate the migrations directory,
// so it works regardless of where `go test` is invoked from.
func MustRunMigrations(t *testing.T, db *sqlx.DB) {
	t.Helper()
	driver, err := postgres.WithInstance(db.DB, &postgres.Config{})
	if err != nil {
		t.Fatalf("testhelper: migrate driver: %v", err)
	}
	m, err := migrate.NewWithDatabaseInstance(
		migrationsDirURL(),
		"postgres",
		driver,
	)
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

// migrationsDirURL returns the migrations directory as a file:// URL that
// golang-migrate can parse on all platforms, including Windows where a bare
// "file://C:\..." is mis-parsed as host:port. The returned form is
// "file:///C:/path/to/migrations" with forward slashes.
func migrationsDirURL() string {
	_, thisFile, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(thisFile), "..", "db", "migrations")
	// Convert backslashes to forward slashes for the URL path component.
	dir = filepath.ToSlash(dir)
	// On Windows the path starts with a drive letter (e.g. "D:/...") which
	// requires an extra leading slash to form a valid file URL: "file:///D:/...".
	if len(dir) > 1 && dir[1] == ':' {
		return "file:///" + dir
	}
	return "file://" + dir
}

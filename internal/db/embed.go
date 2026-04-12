package db

import "embed"

// MigrationFiles holds all SQL migration files, embedded at compile time.
// The embed directive must live in the same package as the migrations directory
// is reachable from (i.e. internal/db, which has a "migrations" subdirectory).
//
//go:embed migrations/*.sql
var MigrationFiles embed.FS

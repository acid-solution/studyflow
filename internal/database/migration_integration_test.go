package database

import (
	"database/sql"
	"os"
	"testing"

	"github.com/pressly/goose/v3"
)

// This test is intentionally opt-in because it rolls the entire schema down.
// Point TEST_MIGRATION_DSN at an empty, disposable MySQL database.
func TestMigrationsFromEmptyAndRollback(t *testing.T) {
	dsn := os.Getenv("TEST_MIGRATION_DSN")
	if dsn == "" {
		t.Skip("TEST_MIGRATION_DSN is not set")
	}
	db, err := OpenMySQL(dsn, "../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := goose.SetDialect("mysql"); err != nil {
		t.Fatal(err)
	}
	if err := goose.DownTo(sqlDB, "../../migrations", 0); err != nil {
		t.Fatalf("roll back all migrations: %v", err)
	}
	if err := goose.Up(sqlDB, "../../migrations"); err != nil {
		t.Fatalf("migrate an empty database: %v", err)
	}
	if err := goose.DownTo(sqlDB, "../../migrations", 4); err != nil {
		t.Fatalf("roll back the plan semantics migration: %v", err)
	}
	if !columnExists(t, sqlDB, "plans", "mode") || columnExists(t, sqlDB, "plan_versions", "mode") {
		t.Fatal("rollback must restore plans.mode and remove plan_versions.mode")
	}
	if err := goose.Up(sqlDB, "../../migrations"); err != nil {
		t.Fatalf("reapply the plan semantics migration: %v", err)
	}
	for table, column := range map[string]string{
		"plans":         "objective",
		"plan_versions": "mode",
		"goals":         "excluded_from_parent_completion",
	} {
		if !columnExists(t, sqlDB, table, column) {
			t.Fatalf("expected %s.%s after migration", table, column)
		}
	}
}

func columnExists(t *testing.T, db interface {
	QueryRow(query string, args ...any) *sql.Row
}, table, column string) bool {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?", table, column).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count == 1
}

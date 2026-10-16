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
	const preservedGoal = "00000000-0000-4000-8000-000000000007"
	if _, err := sqlDB.Exec("INSERT INTO goals (id,user_id,title,description,success_criteria,status,version,created_at,updated_at) VALUES (?,?,'preserved','','','active',1,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))", preservedGoal, preservedGoal); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec("INSERT INTO user_preferences (user_id,timezone,week_start,default_plan_mode,show_completed_today,default_abandon_parent_effect,created_at,updated_at) VALUES (?,'Asia/Shanghai','sunday','sequence',true,'exclude',UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))", preservedGoal); err != nil {
		t.Fatal(err)
	}
	if err := goose.DownTo(sqlDB, "../../migrations", 7); err != nil {
		t.Fatal(err)
	}
	if columnExists(t, sqlDB, "user_preferences", "default_abandon_parent_effect") {
		t.Fatal("abandon preference column survived rollback")
	}
	if err := goose.Up(sqlDB, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	var abandonDefault, weekStart, planMode string
	if err := sqlDB.QueryRow("SELECT default_abandon_parent_effect,week_start,default_plan_mode FROM user_preferences WHERE user_id = ?", preservedGoal).Scan(&abandonDefault, &weekStart, &planMode); err != nil || abandonDefault != "ask" || weekStart != "sunday" || planMode != "sequence" {
		t.Fatalf("existing preferences changed during upgrade: %s %s %s %v", abandonDefault, weekStart, planMode, err)
	}
	if err := goose.DownTo(sqlDB, "../../migrations", 6); err != nil {
		t.Fatalf("roll back notifications: %v", err)
	}
	if columnExists(t, sqlDB, "notification_streams", "last_sequence") {
		t.Fatal("notification tables survived rollback")
	}
	if err := goose.Up(sqlDB, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	var title string
	if err := sqlDB.QueryRow("SELECT title FROM goals WHERE id = ?", preservedGoal).Scan(&title); err != nil || title != "preserved" {
		t.Fatalf("existing business data changed during notification upgrade: %q %v", title, err)
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
		"plans":                "objective",
		"plan_versions":        "mode",
		"goals":                "excluded_from_parent_completion",
		"notification_streams": "pruned_through",
		"notification_outbox":  "sequence",
		"user_preferences":     "default_abandon_parent_effect",
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

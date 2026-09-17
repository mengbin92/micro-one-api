package data

import (
	"context"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func openSystemOptionsTestDB(t *testing.T, name string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+name+"?mode=memory&cache=shared&_busy_timeout=5000"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	return db
}

func createSystemOptionsTable(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Exec("CREATE TABLE system_options (option_key TEXT PRIMARY KEY, option_value TEXT)").Error; err != nil {
		t.Fatalf("create system_options: %v", err)
	}
}

// Production runs with schema isolation: identity points at oneapi_identity
// (no system_options table) while the admin-owned options live in the shared
// oneapi database. The repo must fall back across candidate table names.
func TestGetSystemOptionDBFallsBackAcrossSchemas(t *testing.T) {
	db := openSystemOptionsTestDB(t, "identity-schema")
	if err := db.Exec("ATTACH DATABASE ':memory:' AS oneapi_admin").Error; err != nil {
		t.Fatalf("attach oneapi_admin: %v", err)
	}
	if err := db.Exec("CREATE TABLE oneapi_admin.system_options (option_key TEXT PRIMARY KEY, option_value TEXT)").Error; err != nil {
		t.Fatalf("create oneapi_admin.system_options: %v", err)
	}
	if err := db.Exec("INSERT INTO oneapi_admin.system_options (option_key, option_value) VALUES ('AmountForNewUser', '50000')").Error; err != nil {
		t.Fatalf("seed option: %v", err)
	}
	repo := &Repository{db: db}

	got, err := repo.GetSystemOption(context.Background(), "AmountForNewUser")
	if err != nil {
		t.Fatalf("GetSystemOption() error = %v", err)
	}
	if got != "50000" {
		t.Fatalf("GetSystemOption() = %q, want 50000 from oneapi_admin fallback", got)
	}
}

func TestGetSystemOptionDBPrefersOwnSchemaTable(t *testing.T) {
	db := openSystemOptionsTestDB(t, "shared-db")
	createSystemOptionsTable(t, db)
	if err := db.Exec("INSERT INTO system_options (option_key, option_value) VALUES ('AmountForNewUser', '20000')").Error; err != nil {
		t.Fatalf("seed option: %v", err)
	}
	repo := &Repository{db: db}

	got, err := repo.GetSystemOption(context.Background(), "AmountForNewUser")
	if err != nil {
		t.Fatalf("GetSystemOption() error = %v", err)
	}
	if got != "20000" {
		t.Fatalf("GetSystemOption() = %q, want 20000 from own-schema table", got)
	}
}

func TestGetSystemOptionDBMissingTableEverywhereErrors(t *testing.T) {
	db := openSystemOptionsTestDB(t, "identity-no-options")
	t.Setenv("ADMIN_SCHEMA", "missing_admin")
	repo := &Repository{db: db}

	if _, err := repo.GetSystemOption(context.Background(), "AmountForNewUser"); err == nil {
		t.Fatal("expected error when no system_options table exists")
	}
}

func TestGetSystemOptionDBMissingKeyReturnsEmpty(t *testing.T) {
	db := openSystemOptionsTestDB(t, "identity-empty-options")
	createSystemOptionsTable(t, db)
	repo := &Repository{db: db}

	got, err := repo.GetSystemOption(context.Background(), "AmountForNewUser")
	if err != nil {
		t.Fatalf("GetSystemOption() error = %v", err)
	}
	if got != "" {
		t.Fatalf("GetSystemOption() = %q, want empty", got)
	}
}

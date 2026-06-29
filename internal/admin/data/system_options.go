package data

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// SystemOption represents a system configuration entry.
type SystemOption struct {
	Key   string
	Value string
}

// SystemOptionsRepo provides CRUD for system_options table.
type SystemOptionsRepo struct {
	db     *sql.DB
	driver string // canonical driver name; used by Set to pick upsert dialect
	pgBind bool   // true when driver is Postgres; rebind ? → $N
}

// NewSystemOptionsRepo creates a new repo from a database connection.
//
// The driver is pinned to "sqlite3" by default (the lite deployment is
// the dominant case for the admin-api). Callers that need a different
// dialect should use NewSystemOptionsRepoWithDriver, which selects
// the right placeholder/upsert syntax.
func NewSystemOptionsRepo(db *sql.DB) *SystemOptionsRepo {
	return NewSystemOptionsRepoWithDriver(db, "sqlite3")
}

// NewSystemOptionsRepoWithDriver is like NewSystemOptionsRepo but pins
// the driver explicitly. MySQL and SQLite use "?" placeholders + ON
// DUPLICATE KEY UPDATE; Postgres uses "$N" placeholders + ON CONFLICT
// DO UPDATE. Pass "mysql", "sqlite3", or "postgres" (case-insensitive).
func NewSystemOptionsRepoWithDriver(db *sql.DB, driver string) *SystemOptionsRepo {
	d := strings.ToLower(strings.TrimSpace(driver))
	pg := d == "postgres" || d == "postgresql" || d == "pgx"
	return &SystemOptionsRepo{db: db, driver: d, pgBind: pg}
}

// Get returns the value for a given key, or empty string if not found.
func (r *SystemOptionsRepo) Get(key string) (string, error) {
	var value string
	// Postgres pgx uses $1 instead of ?; other drivers accept ?.
	query := "SELECT option_value FROM system_options WHERE option_key = ?"
	if r.pgBind {
		query = "SELECT option_value FROM system_options WHERE option_key = $1"
	}
	err := r.db.QueryRow(query, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get system option %s: %w", key, err)
	}
	return value, nil
}

// Set upserts a key-value pair.
//
// MySQL:    INSERT ... ON DUPLICATE KEY UPDATE
// SQLite3:  INSERT ... ON CONFLICT (option_key) DO UPDATE SET ...
// Postgres: INSERT ... ON CONFLICT (option_key) DO UPDATE SET ...
func (r *SystemOptionsRepo) Set(key, value string) error {
	var query string
	if r.pgBind {
		query = `INSERT INTO system_options (option_key, option_value, updated_at)
		         VALUES ($1, $2, $3)
		         ON CONFLICT (option_key) DO UPDATE
		         SET option_value = EXCLUDED.option_value,
		             updated_at = EXCLUDED.updated_at`
	} else if r.driver == "sqlite3" || r.driver == "sqlite" {
		query = `INSERT INTO system_options (option_key, option_value, updated_at)
		         VALUES (?, ?, ?)
		         ON CONFLICT (option_key) DO UPDATE SET
		             option_value = excluded.option_value,
		             updated_at = excluded.updated_at`
	} else {
		query = `INSERT INTO system_options (option_key, option_value, updated_at)
		         VALUES (?, ?, ?)
		         ON DUPLICATE KEY UPDATE option_value = VALUES(option_value),
		                             updated_at = VALUES(updated_at)`
	}
	_, err := r.db.Exec(query, key, value, time.Now())
	if err != nil {
		return fmt.Errorf("set system option %s: %w", key, err)
	}
	return nil
}

// GetAll returns all system options.
func (r *SystemOptionsRepo) GetAll() ([]SystemOption, error) {
	rows, err := r.db.Query("SELECT option_key, option_value FROM system_options")
	if err != nil {
		return nil, fmt.Errorf("list system options: %w", err)
	}
	defer rows.Close()

	var opts []SystemOption
	for rows.Next() {
		var o SystemOption
		if err := rows.Scan(&o.Key, &o.Value); err != nil {
			return nil, fmt.Errorf("scan system option: %w", err)
		}
		opts = append(opts, o)
	}
	return opts, rows.Err()
}

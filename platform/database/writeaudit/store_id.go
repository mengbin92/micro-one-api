package writeaudit

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"gorm.io/gorm"
)

// Pool addresses can coincide across processes with separate in-memory stores.
var memoryNamespace = rand.Text()

// StoreID identifies the physical audit store without exposing its location.
func StoreID(ctx context.Context, db *gorm.DB) (string, error) {
	unavailable := errors.New("writeaudit: store identity unavailable")
	if db == nil || db.Dialector == nil {
		return "", unavailable
	}
	query := db.WithContext(ctx)
	var parts []string
	switch db.Dialector.Name() {
	case "mysql":
		var instance, database string
		if err := query.Raw("SELECT @@server_uuid, DATABASE()").Row().Scan(&instance, &database); err != nil || instance == "" || database == "" {
			return "", unavailable
		}
		parts = []string{"mysql", instance, database}
	case "postgres":
		var address, port, started, database, schema string
		if err := query.Raw(`SELECT COALESCE(inet_server_addr()::text, 'local'),
			COALESCE(inet_server_port()::text, 'local'),
			EXTRACT(EPOCH FROM pg_postmaster_start_time())::text, current_database(), n.nspname
			FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE c.oid = to_regclass('resource_write_audits')`).Row().Scan(&address, &port, &started, &database, &schema); err != nil || database == "" || schema == "" {
			return "", unavailable
		}
		parts = []string{"postgres", address, port, started, database, schema}
	case "sqlite":
		var files []struct{ Name, File string }
		if err := query.Raw("PRAGMA database_list").Scan(&files).Error; err != nil {
			return "", unavailable
		}
		for _, file := range files {
			if file.Name != "main" {
				continue
			}
			if file.File == "" {
				sqlDB, err := db.DB()
				if err != nil {
					return "", unavailable
				}
				parts = []string{"sqlite-memory", memoryNamespace, fmt.Sprintf("%p", sqlDB)}
			} else {
				path, err := filepath.EvalSymlinks(file.File)
				if err != nil {
					return "", unavailable
				}
				parts = []string{"sqlite", path}
			}
			break
		}
		if len(parts) == 0 {
			return "", unavailable
		}
	default:
		return "", unavailable
	}
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(digest[:]), nil
}

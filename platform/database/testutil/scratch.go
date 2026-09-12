// Package testutil creates isolated schemas for storage-boundary regression tests.
package testutil

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"micro-one-api/platform/database/migrate"
)

// RoutingContextDB always uses a scratch file/schema. External drivers are
// opt-in and restricted to a local disposable server; deployment DSNs are
// never read. All repository migrations run, including 091 and 092.
func RoutingContextDB(t *testing.T, driver string) *gorm.DB {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "context.db") + "?_foreign_keys=on&_busy_timeout=5000"
	sqlDriver := "sqlite3"
	if driver != "sqlite" {
		dsn = os.Getenv("GROUP_CONTEXT_TEST_" + strings.ToUpper(driver) + "_DSN")
		if dsn == "" {
			t.Skip("disposable local database DSN not supplied")
		}
		sqlDriver = driver
		if driver == "postgres" {
			sqlDriver = "pgx"
		}
		var host string
		if driver == "mysql" {
			cfg, err := mysqldriver.ParseDSN(dsn)
			require.NoError(t, err)
			host, _, err = net.SplitHostPort(cfg.Addr)
			require.NoError(t, err)
		} else {
			u, err := url.Parse(dsn)
			require.NoError(t, err)
			host = u.Hostname()
		}
		require.Contains(t, []string{"127.0.0.1", "localhost", "::1"}, host)
		admin, err := sql.Open(sqlDriver, dsn)
		require.NoError(t, err)
		schema := fmt.Sprintf("routing_context_test_%d", time.Now().UnixNano())
		if driver == "mysql" {
			_, err = admin.Exec("CREATE DATABASE " + schema + " CHARACTER SET utf8mb4")
			require.NoError(t, err)
			t.Cleanup(func() { _, _ = admin.Exec("DROP DATABASE " + schema); _ = admin.Close() })
			cfg, err := mysqldriver.ParseDSN(dsn)
			require.NoError(t, err)
			cfg.DBName = schema
			cfg.ParseTime = true
			cfg.MultiStatements = true
			dsn = cfg.FormatDSN()
		} else {
			_, err = admin.Exec("CREATE SCHEMA " + schema)
			require.NoError(t, err)
			t.Cleanup(func() { _, _ = admin.Exec("DROP SCHEMA " + schema + " CASCADE"); _ = admin.Close() })
			u, err := url.Parse(dsn)
			require.NoError(t, err)
			q := u.Query()
			q.Set("search_path", schema)
			u.RawQuery = q.Encode()
			dsn = u.String()
		}
	}
	db, err := sql.Open(sqlDriver, dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(file), "../../../migrations")
	if driver != "mysql" {
		dir = filepath.Join(dir, driver)
	}
	r := migrate.NewWithDriver(db, dir, driver)
	_, err = r.Apply(context.Background())
	require.NoError(t, err)
	again, err := r.Apply(context.Background())
	require.NoError(t, err)
	require.Empty(t, again)
	var dialect gorm.Dialector = sqlite.New(sqlite.Config{Conn: db})
	if driver == "mysql" {
		dialect = mysql.New(mysql.Config{Conn: db})
	}
	if driver == "postgres" {
		dialect = postgres.New(postgres.Config{Conn: db})
	}
	gdb, err := gorm.Open(dialect, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	return gdb
}

package writeaudit

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func openSQLite(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func storeID(t *testing.T, db *gorm.DB) string {
	t.Helper()
	id, err := StoreID(t.Context(), db)
	require.NoError(t, err)
	require.Len(t, id, 64)
	return id
}

func TestStoreIDRejectsMissingOrUnavailableDB(t *testing.T) {
	id, err := StoreID(t.Context(), nil)
	require.Error(t, err)
	require.Empty(t, id)
	db := openSQLite(t, ":memory:")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = StoreID(ctx, db)
	require.Error(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	_, err = StoreID(t.Context(), db)
	require.Error(t, err)
}

func TestStoreIDSQLiteFileIdentity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.db")
	first := openSQLite(t, path)
	id := storeID(t, first)
	require.Equal(t, id, storeID(t, openSQLite(t, path+"?cache=private")))
	alias := filepath.Join(dir, "alias.db")
	require.NoError(t, os.Symlink(path, alias))
	require.Equal(t, id, storeID(t, openSQLite(t, alias)))
	require.NotEqual(t, id, storeID(t, openSQLite(t, filepath.Join(dir, "other.db"))))
	require.NotContains(t, id, path)
}

func TestStoreIDSQLiteMemoryIdentity(t *testing.T) {
	first := openSQLite(t, ":memory:")
	id := storeID(t, first)
	require.Equal(t, id, storeID(t, first.WithContext(t.Context())))
	sqlDB, err := first.DB()
	require.NoError(t, err)
	wrapped, err := gorm.Open(sqlite.New(sqlite.Config{Conn: sqlDB}), &gorm.Config{})
	require.NoError(t, err)
	require.Equal(t, id, storeID(t, wrapped))
	require.NotEqual(t, id, storeID(t, openSQLite(t, ":memory:")))
}

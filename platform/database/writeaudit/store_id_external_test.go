package writeaudit

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	dbtest "micro-one-api/platform/database/testutil"
	"micro-one-api/platform/database/xdb"

	"gorm.io/gorm"
)

func openExternal(t *testing.T, driver, schema string) *gorm.DB {
	t.Helper()
	db, err := xdb.Open(xdb.DatabaseConfig{
		Driver: driver, DSN: os.Getenv("GROUP_CONTEXT_TEST_" + strings.ToUpper(driver) + "_DSN"), Schema: schema,
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func TestStoreIDExternalIdentity(t *testing.T) {
	for _, driver := range []string{"mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			first := dbtest.RoutingContextDB(t, driver)
			other := dbtest.RoutingContextDB(t, driver)
			var schema string
			query := "SELECT DATABASE()"
			if driver == "postgres" {
				query = "SELECT current_schema()"
			}
			require.NoError(t, first.Raw(query).Row().Scan(&schema))
			id := storeID(t, first)
			require.Equal(t, id, storeID(t, openExternal(t, driver, schema)))
			require.NotEqual(t, id, storeID(t, other))
		})
	}
}

func TestStoreIDPostgresResolvedTableAndRestrictedRole(t *testing.T) {
	db := dbtest.RoutingContextDB(t, "postgres")
	var schema string
	require.NoError(t, db.Raw("SELECT current_schema()").Row().Scan(&schema))
	id := storeID(t, db)
	owner := fmt.Sprintf("writeaudit_empty_%d", time.Now().UnixNano())
	require.NoError(t, db.Exec("CREATE SCHEMA "+owner).Error)
	t.Cleanup(func() { _ = db.Exec("DROP SCHEMA " + owner).Error })
	u, err := url.Parse(os.Getenv("GROUP_CONTEXT_TEST_POSTGRES_DSN"))
	require.NoError(t, err)
	q := u.Query()
	q.Set("search_path", owner+","+schema)
	u.RawQuery = q.Encode()
	fallback, err := xdb.Open(xdb.DatabaseConfig{Driver: "postgres", DSN: u.String()})
	require.NoError(t, err)
	sqlDB, err := fallback.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	var current string
	require.NoError(t, fallback.Raw("SELECT current_schema()").Row().Scan(&current))
	require.Equal(t, owner, current)
	require.Equal(t, id, storeID(t, fallback))
	_, err = StoreID(t.Context(), openExternal(t, "postgres", owner))
	require.Error(t, err)

	role := fmt.Sprintf("writeaudit_reader_%d", time.Now().UnixNano())
	require.NoError(t, db.Exec("CREATE ROLE "+role).Error)
	t.Cleanup(func() {
		_ = db.Exec("REVOKE USAGE ON SCHEMA " + schema + " FROM " + role).Error
		_ = db.Exec("DROP ROLE " + role).Error
	})
	require.NoError(t, db.Exec("GRANT USAGE ON SCHEMA "+schema+" TO "+role).Error)
	require.NoError(t, db.Connection(func(conn *gorm.DB) error {
		if err := conn.Exec("SET ROLE " + role).Error; err != nil {
			return err
		}
		defer func() { _ = conn.Exec("RESET ROLE").Error }()
		var superuser bool
		if err := conn.Raw("SELECT rolsuper FROM pg_roles WHERE rolname = current_user").Row().Scan(&superuser); err != nil {
			return err
		}
		require.False(t, superuser)
		actual, err := StoreID(t.Context(), conn)
		require.NoError(t, err)
		require.Equal(t, id, actual)
		return nil
	}))
}

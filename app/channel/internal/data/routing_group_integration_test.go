package data

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Opt-in only: these DSNs must point at disposable local database servers.
// Tests create and drop uniquely named schemas, never use deployment env vars.
func TestRoutingGroupRealDatabase(t *testing.T) {
	for _, driver := range []string{"mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			dsn := os.Getenv("GROUP_BACKFILL_TEST_" + strings.ToUpper(driver) + "_DSN")
			if dsn == "" {
				t.Skip("disposable database DSN not supplied")
			}
			sqlDriver := driver
			if driver == "postgres" {
				sqlDriver = "pgx"
			}
			admin, err := sql.Open(sqlDriver, dsn)
			require.NoError(t, err)
			defer admin.Close()
			schema := fmt.Sprintf("group_backfill_test_%d", time.Now().UnixNano())
			if driver == "mysql" {
				_, err = admin.Exec("CREATE DATABASE " + schema + " CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci")
				require.NoError(t, err)
				defer admin.Exec("DROP DATABASE " + schema)
				cfg, err := mysqldriver.ParseDSN(dsn)
				require.NoError(t, err)
				cfg.DBName = schema
				dsn = cfg.FormatDSN()
			} else {
				_, err = admin.Exec("CREATE SCHEMA " + schema)
				require.NoError(t, err)
				defer admin.Exec("DROP SCHEMA " + schema + " CASCADE")
				parsed, err := url.Parse(dsn)
				require.NoError(t, err)
				require.Contains(t, []string{"postgres", "postgresql"}, parsed.Scheme)
				q := parsed.Query()
				q.Set("search_path", schema)
				parsed.RawQuery = q.Encode()
				dsn = parsed.String()
			}
			db, err := sql.Open(sqlDriver, dsn)
			require.NoError(t, err)
			defer db.Close()
			groupSQLFile(t, db, "testdata/routing_group_legacy.sql", driver)
			prefix := "../../../../migrations/"
			if driver == "postgres" {
				prefix += "postgres/"
			}
			groupSQLFile(t, db, prefix+"092_create_routing_groups.sql", driver)
			groupSQLFile(t, db, prefix+"095_create_routing_change_outbox.sql", driver)
			var dialect gorm.Dialector = mysql.New(mysql.Config{Conn: db})
			if driver == "postgres" {
				dialect = postgres.New(postgres.Config{Conn: db})
			}
			gdb, err := gorm.Open(dialect, &gorm.Config{})
			require.NoError(t, err)
			verifyGroupBackfill(t, db, gdb, driver)
			// Restore the deliberate stale-CSV edit made by the backfill check.
			require.NoError(t, gdb.Table("channels").Where("id = 1").Update("group", "default,vip").Error)
			verifyRoutingGroupDualWrites(t, gdb, driver)
			verifyRoutingGroupExactKeys(t, gdb)
			verifyRoutingGroupShadowRejectsLikeExpansion(t, db, gdb, driver)
		})
	}
}

func verifyRoutingGroupExactKeys(t *testing.T, db *gorm.DB) {
	t.Helper()
	seen := map[int64]bool{}
	for _, key := range []string{"Case", "case", "case ", "a_b", "a%b", "axb", strings.Repeat("长", 300)} {
		row := routingGroupModel{Key: key, DisplayName: key, Status: "disabled", AccessMode: "restricted", ModelAccessMode: "all_authorized", Revision: 1}
		require.NoError(t, db.Create(&row).Error)
		id, err := routingGroupID(db, key)
		require.NoError(t, err)
		require.False(t, seen[id])
		seen[id] = true
		require.Equal(t, row.ID, id)
	}
	require.Error(t, db.Create(&routingGroupModel{Key: "Case", DisplayName: "duplicate"}).Error)
}
func TestRoutingGroupExactKeysSQLite(t *testing.T) {
	_, db := routingGroupFixture(t)
	verifyRoutingGroupExactKeys(t, db)
}

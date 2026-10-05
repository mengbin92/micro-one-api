package data

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	mysqlconfig "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	dbtest "micro-one-api/platform/database/testutil"
)

// The actual billing repository retains settlement writes with an independent
// column-restricted credential, which cannot change identity or resurrect users.
func TestIAMDMySQLFinanceColumnIsolation(t *testing.T) {
	db := dbtest.RoutingContextDB(t, "mysql")
	var schema string
	require.NoError(t, db.Raw("SELECT DATABASE()").Scan(&schema).Error)
	name := fmt.Sprintf("rbac_d_%d", time.Now().UnixNano())
	password := "isolated-d0-finance-channel"
	require.NoError(t, db.Exec("CREATE USER '"+name+"'@'%' IDENTIFIED BY '"+password+"'").Error)
	t.Cleanup(func() { _ = db.Exec("DROP USER '" + name + "'@'%'").Error })
	require.NoError(t, db.Exec("GRANT SELECT, UPDATE (balance, frozen_amount, used_amount, request_count) ON `"+schema+"`.users TO '"+name+"'@'%'").Error)
	cfg, err := mysqlconfig.ParseDSN(os.Getenv("GROUP_CONTEXT_TEST_MYSQL_DSN"))
	require.NoError(t, err)
	cfg.User, cfg.Passwd, cfg.DBName = name, password, schema
	limited, err := gorm.Open(mysql.Open(cfg.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := limited.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.Table("users").Create(map[string]any{"id": 700, "username": "d-finance", "aff_code": "d-finance", "role": 1, "status": 1, "balance": 100}).Error)
	oldName := name + "_old"
	require.NoError(t, db.Exec("CREATE USER '"+oldName+"'@'%' IDENTIFIED BY '"+password+"'").Error)
	t.Cleanup(func() { _ = db.Exec("DROP USER '" + oldName + "'@'%'").Error })
	require.NoError(t, db.Exec("GRANT SELECT, UPDATE, INSERT, DELETE ON `"+schema+"`.users TO '"+oldName+"'@'%'").Error)
	cfg.User = oldName
	oldDB, err := gorm.Open(mysql.Open(cfg.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	oldSQL, err := oldDB.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = oldSQL.Close() })
	// Complete the old in-flight transaction before revoking its write channel.
	oldTx := oldDB.Begin()
	require.NoError(t, oldTx.Error)
	require.NoError(t, oldTx.Table("users").Where("id=700").Update("display_name", "old writer drained").Error)
	require.NoError(t, oldTx.Commit().Error)
	require.NoError(t, db.Exec("REVOKE UPDATE, INSERT, DELETE ON `"+schema+"`.users FROM '"+oldName+"'@'%'").Error)
	require.Error(t, oldDB.Table("users").Where("id=700").Update("role", 100).Error)
	require.Error(t, oldDB.Table("users").Where("id=700").Update("password_hash", "old takeover").Error)
	require.Error(t, oldDB.Table("users").Create(map[string]any{"id": 702, "username": "old-resurrection", "role": 100}).Error)
	repo := NewAccountRepo(&Data{db: limited})
	balance, err := repo.UpdateBalance(context.Background(), "700", 25, "d0_rehearsal")
	require.NoError(t, err)
	require.EqualValues(t, 125, balance)
	require.NoError(t, repo.UpdateUsage(context.Background(), "700", 3, 1))
	require.NoError(t, repo.UpdateFrozenAmount(context.Background(), "700", 5))
	for _, column := range []string{"role", "status", "password_changed_at", "authorization_revision"} {
		require.Error(t, limited.Table("users").Where("id=700").Update(column, 100).Error, column)
	}
	require.Error(t, limited.Table("users").Where("id=700").Update("password_hash", "takeover").Error)
	require.Error(t, limited.Table("users").Where("id=700").Delete(map[string]any{}).Error)
	require.Error(t, limited.Table("users").Create(map[string]any{"id": 701, "username": "resurrected", "role": 100}).Error)
	var row struct {
		Role, Status                                    int32
		Balance, FrozenAmount, UsedAmount, RequestCount int64
	}
	require.NoError(t, db.Table("users").Where("id=700").Take(&row).Error)
	require.EqualValues(t, 1, row.Role)
	require.EqualValues(t, 1, row.Status)
	require.EqualValues(t, 125, row.Balance)
	require.EqualValues(t, 5, row.FrozenAmount)
	require.EqualValues(t, 3, row.UsedAmount)
	require.EqualValues(t, 1, row.RequestCount)
}

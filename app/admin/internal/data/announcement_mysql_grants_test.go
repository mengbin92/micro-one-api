package data

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"micro-one-api/app/admin/internal/biz"
	dbtest "micro-one-api/platform/database/testutil"
	"micro-one-api/platform/database/xdb"
)

func TestAnnouncementMySQLRuntimeReceiptGrants(t *testing.T) {
	db := dbtest.RoutingContextDB(t, "mysql")
	ctx := context.Background()
	seed, err := NewAnnouncementRepo(&Data{DB: db}).Save(ctx, biz.Announcement{Title: "runtime grant", Body: "receipt", Status: "published", Audience: "all"}, "1", "runtime grant fixture")
	require.NoError(t, err)
	var schema string
	require.NoError(t, db.Raw("SELECT DATABASE()").Scan(&schema).Error)
	// All identifiers come from this test and the local scratch-schema helper.
	account := "b4_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	require.NoError(t, db.Exec("CREATE USER '"+account+"'@'%'").Error)
	t.Cleanup(func() { require.NoError(t, db.Exec("DROP USER '"+account+"'@'%'").Error) })
	for table, privileges := range map[string]string{"announcements": "SELECT, UPDATE", "announcement_reads": "SELECT, INSERT"} {
		require.NoError(t, db.Exec(fmt.Sprintf("GRANT %s ON `%s`.`%s` TO '%s'@'%%'", privileges, schema, table, account)).Error)
	}
	cfg, err := mysqldriver.ParseDSN(os.Getenv("GROUP_CONTEXT_TEST_MYSQL_DSN"))
	require.NoError(t, err)
	cfg.User = account
	cfg.Passwd = ""
	cfg.DBName = schema
	runtimeDB, err := xdb.Open(xdb.DatabaseConfig{Driver: "mysql", DSN: cfg.FormatDSN()})
	require.NoError(t, err)
	pool, err := runtimeDB.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = pool.Close() })
	repo := NewAnnouncementRepo(&Data{DB: runtimeDB})
	subject := biz.AnnouncementSubject{UserID: 7, GroupID: 2}
	require.ErrorIs(t, repo.MarkRead(ctx, seed.ID, seed.Revision, subject, time.Now().Unix()), biz.ErrAnnouncementUnavailable, "GORM MySQL upsert requires UPDATE even for a new receipt")
	_, file, _, _ := runtime.Caller(0)
	template, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../../../migrations/manual/148_announcement_runtime_grants.example.sql"))
	require.NoError(t, err)
	grants := strings.ReplaceAll(string(template), "oneapi_admin.", "`"+schema+"`.")
	grants = strings.ReplaceAll(grants, "'oneapi_admin'", "'"+account+"'")
	require.NoError(t, db.Exec(grants).Error)
	require.NoError(t, repo.MarkRead(ctx, seed.ID, seed.Revision, subject, time.Now().Unix()))
	require.NoError(t, repo.MarkRead(ctx, seed.ID, seed.Revision, subject, time.Now().Unix()))
	var count int64
	require.NoError(t, db.Model(&announcementRead{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	updated, err := repo.Save(ctx, seed, "7", "runtime save")
	require.NoError(t, err)
	require.EqualValues(t, 2, updated.Revision)
	rows, total, err := repo.List(ctx, &subject, time.Now().Unix(), 1, 20)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.False(t, rows[0].Read)
}

package data

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"micro-one-api/app/channel/internal/biz"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func groupSQLFile(t *testing.T, db *sql.DB, path, driver string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	lines := []string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "--") {
			lines = append(lines, line)
		}
	}
	for _, s := range strings.Split(strings.Join(lines, "\n"), ";") {
		if strings.TrimSpace(s) == "" {
			continue
		}
		if driver == "sqlite3" {
			s = strings.ReplaceAll(s, "id BIGINT PRIMARY KEY", "id INTEGER PRIMARY KEY")
		}
		if driver == "postgres" {
			s = strings.ReplaceAll(s, "`", `"`)
		}
		_, err := db.Exec(s)
		require.NoError(t, err)
	}
}
func routingGroupFixture(t *testing.T) (*sql.DB, *gorm.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "groups.db")+"?_foreign_keys=on")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	groupSQLFile(t, db, "testdata/routing_group_legacy.sql", "sqlite3")
	groupSQLFile(t, db, "../../../../migrations/sqlite/090_create_routing_groups.sql", "sqlite3")
	gdb, err := gorm.Open(sqlite.New(sqlite.Config{Conn: db}), &gorm.Config{})
	require.NoError(t, err)
	return db, gdb
}
func groupBaseline(t *testing.T, db *sql.DB, driver string) *biz.GroupAuditReport {
	t.Helper()
	report := uncheckedGroupBaseline(t, db, driver)
	require.True(t, report.Migration.ReadyForBackfill, "%+v", report.Issues)
	return report
}
func uncheckedGroupBaseline(t *testing.T, db *sql.DB, driver string) *biz.GroupAuditReport {
	t.Helper()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelSerializable})
	require.NoError(t, err)
	defer tx.Rollback()
	c, r, i, err := NewGroupAuditRepositories(tx, driver, GroupAuditSchemas{})
	require.NoError(t, err)
	uc := biz.NewChannelUsecase(c, nil)
	uc.SetModelRoutingRepo(r)
	report, err := biz.NewGroupAuditUsecase(i, uc).Run(ctx, []string{"legacy-chat", "unknown"}, map[string]float64{"default": 1, "vip": 1, "draft": 1}, 10000)
	require.NoError(t, err)
	return report
}
func applyGroupBaseline(t *testing.T, db *sql.DB, driver string, report *biz.GroupAuditReport, commit bool) error {
	t.Helper()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	require.NoError(t, err)
	defer tx.Rollback()
	repo, err := NewRoutingGroupBackfillRepo(tx, driver, GroupAuditSchemas{})
	require.NoError(t, err)
	_, err = biz.NewRoutingGroupBackfillUsecase(repo).Apply(ctx, report)
	if err != nil {
		return err
	}
	if commit {
		return tx.Commit()
	}
	return tx.Rollback()
}

func verifyGroupBackfill(t *testing.T, db *sql.DB, gdb *gorm.DB, driver string) {
	t.Helper()
	ctx := context.Background()
	report := groupBaseline(t, db, driver)
	require.ErrorIs(t, routingGroupSchemaReady(gdb), biz.ErrRoutingGroupMigrationRequired)
	require.NoError(t, applyGroupBaseline(t, db, driver, report, false))
	var count int64
	require.NoError(t, gdb.Model(&routingGroupModel{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, applyGroupBaseline(t, db, driver, report, true))
	require.NoError(t, routingGroupSchemaReady(gdb))
	var before []routingGroupModel
	require.NoError(t, gdb.Order("id ASC").Find(&before).Error)
	require.Len(t, before, 3)
	require.NoError(t, applyGroupBaseline(t, db, driver, report, true))
	var after []routingGroupModel
	require.NoError(t, gdb.Order("id ASC").Find(&after).Error)
	require.Equal(t, before, after)
	require.NoError(t, gdb.Table("routing_group_backfills").Count(&count).Error)
	require.EqualValues(t, 1, count)
	vip, err := routingGroupID(gdb, "vip")
	require.NoError(t, err)
	require.NoError(t, gdb.Table("account_routing_groups").Where("routing_group_id = ?", vip).Count(&count).Error)
	require.Zero(t, count, "extra model grant must not become account membership")
	detail, err := NewRoutingGroupRepo(&Repository{db: gdb}).GetRoutingGroup(ctx, vip)
	require.NoError(t, err)
	require.Len(t, detail.ModelGrants, 1)
	require.True(t, detail.ModelGrants[0].ExtraAuthorization)
	require.Equal(t, int32(13), detail.ModelGrants[0].Priority)
	require.Len(t, detail.Resources, 1)
	require.EqualValues(t, 7, detail.Resources[0].Priority)
	require.EqualValues(t, 5, detail.Resources[0].Weight)
	// Rebuild a lost projection using the same IDs and original model-scoped grants.
	require.NoError(t, gdb.Exec("DELETE FROM channel_routing_groups").Error)
	require.NoError(t, applyGroupBaseline(t, db, driver, report, true))
	again, err := routingGroupID(gdb, "vip")
	require.NoError(t, err)
	require.Equal(t, vip, again)
	// An edit after the audit must fail before it can rewrite the baseline.
	require.NoError(t, gdb.Table("channels").Where("id = ?", 1).Update("group", "default").Error)
	require.Error(t, applyGroupBaseline(t, db, driver, report, true))
	require.NoError(t, gdb.Table("channel_routing_groups").Where("channel_id = ? AND routing_group_id = ?", 1, vip).Count(&count).Error)
	require.EqualValues(t, 1, count)
}
func TestRoutingGroupBackfillSQLite(t *testing.T) {
	db, gdb := routingGroupFixture(t)
	verifyGroupBackfill(t, db, gdb, "sqlite3")
}

func TestRoutingGroupDualWriteRollsBackWithAbilities(t *testing.T) {
	db, gdb := routingGroupFixture(t)
	report := groupBaseline(t, db, "sqlite3")
	require.NoError(t, applyGroupBaseline(t, db, "sqlite3", report, true))
	verifyRoutingGroupDualWrites(t, gdb, "sqlite3")
}

func verifyRoutingGroupDualWrites(t *testing.T, gdb *gorm.DB, driver string) {
	t.Helper()
	r := &Repository{db: gdb, routingGroupDualWrite: true}
	update := func(group string) error {
		return gdb.Transaction(func(tx *gorm.DB) error {
			if err := tx.Table("channels").Where("id = ?", 1).Update("group", group).Error; err != nil {
				return err
			}
			return r.syncAbilitiesTx(tx, &biz.Channel{ID: 1, Group: group, Models: []string{"legacy-*"}, Status: biz.ChannelStatusEnabled, Priority: 7})
		})
	}
	require.Error(t, update("does-not-exist"))
	var old string
	require.NoError(t, gdb.Table("channels").Select(quoteGroupColumnSQL(gdb, "`group`")).Where("id = 1").Scan(&old).Error)
	require.Equal(t, "default,vip", old)
	switch driver {
	case "mysql":
		require.NoError(t, gdb.Exec("CREATE TRIGGER fail_abilities BEFORE INSERT ON abilities FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'fixture failure'").Error)
	case "postgres":
		require.NoError(t, gdb.Exec("CREATE FUNCTION fail_abilities_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture failure'; END $$").Error)
		require.NoError(t, gdb.Exec("CREATE TRIGGER fail_abilities BEFORE INSERT ON abilities FOR EACH ROW EXECUTE FUNCTION fail_abilities_fn()").Error)
	default:
		require.NoError(t, gdb.Exec("CREATE TRIGGER fail_abilities BEFORE INSERT ON abilities BEGIN SELECT RAISE(ABORT, 'fixture failure'); END").Error)
	}
	require.Error(t, update("vip"))
	var count int64
	require.NoError(t, gdb.Table("channel_routing_groups").Where("channel_id = 1").Count(&count).Error)
	require.EqualValues(t, 2, count)
	drop := "DROP TRIGGER fail_abilities"
	if driver == "postgres" {
		drop += " ON abilities"
	}
	require.NoError(t, gdb.Exec(drop).Error)
	require.NoError(t, update("vip,vip"))
	require.NoError(t, gdb.Table("channel_routing_groups").Where("channel_id = 1").Count(&count).Error)
	require.EqualValues(t, 1, count)
	// Mapping updates retain their independent ID reference, including extra grants.
	require.NoError(t, r.UpsertSubscriptionMapping(context.Background(), &biz.ModelSubscriptionMapping{SubscriptionAccountID: 1, ModelPK: 1, GroupName: "vip", Priority: 21}))
	var groupID int64
	require.NoError(t, gdb.Table("model_subscription_mapping").Select("routing_group_id").Where("id = 1").Scan(&groupID).Error)
	vip, err := routingGroupID(gdb, "vip")
	require.NoError(t, err)
	require.Equal(t, vip, groupID)
	require.NoError(t, gdb.Table("account_routing_groups").Where("routing_group_id = ?", vip).Count(&count).Error)
	require.Zero(t, count)
}

func TestRoutingGroupImportMappingAtomicity(t *testing.T) {
	db, gdb := routingGroupFixture(t)
	report := groupBaseline(t, db, "sqlite3")
	require.NoError(t, applyGroupBaseline(t, db, "sqlite3", report, true))
	repo := &Repository{db: gdb, routingGroupDualWrite: true}
	run := func(key string) error {
		return gdb.Transaction(func(tx *gorm.DB) error {
			if err := tx.Where("id = 1").Delete(&modelSubscriptionMappingModel{}).Error; err != nil {
				return err
			}
			return repo.applyImportSubscriptionMappings(tx, 1, []*biz.ModelSubscriptionMapping{{SubscriptionAccountID: 1, GroupName: key, Enabled: true, Priority: 42, UpstreamModelID: "import-model"}}, 0)
		})
	}
	require.Error(t, run("unknown"))
	var rows []modelSubscriptionMappingModel
	require.NoError(t, gdb.Find(&rows).Error)
	require.Len(t, rows, 1)
	require.EqualValues(t, 13, rows[0].Priority)
	require.NoError(t, run("vip"))
	vip, err := routingGroupID(gdb, "vip")
	require.NoError(t, err)
	detail, err := NewRoutingGroupRepo(repo).GetRoutingGroup(context.Background(), vip)
	require.NoError(t, err)
	require.Len(t, detail.ModelGrants, 1)
	require.True(t, detail.ModelGrants[0].ExtraAuthorization)
	require.Equal(t, "import-model", detail.ModelGrants[0].UpstreamModelID)
}

func verifyRoutingGroupShadowRejectsLikeExpansion(t *testing.T, db *sql.DB, gdb *gorm.DB, driver string) {
	t.Helper()
	for _, table := range []string{"channel_routing_groups", "account_routing_groups", "routing_group_backfills", "routing_groups"} {
		require.NoError(t, gdb.Exec("DELETE FROM "+table).Error)
	}
	require.NoError(t, gdb.Table("channels").Where("id = 2").Update("group", "axb,other").Error)
	require.NoError(t, gdb.Table("users").Create(map[string]any{"id": 3, "group": "a_b"}).Error)
	report := uncheckedGroupBaseline(t, db, driver)
	require.False(t, report.Migration.ReadyForBackfill)
	// Even a manually cleared audit disposition cannot bypass candidate parity.
	report.Migration.ReadyForBackfill = true
	report.Migration.BlockingIssues = 0
	report.Issues = nil
	require.ErrorIs(t, applyGroupBaseline(t, db, driver, report, true), biz.ErrRoutingGroupBaselineConflict)
	var count int64
	require.NoError(t, gdb.Model(&routingGroupModel{}).Count(&count).Error)
	require.Zero(t, count, "shadow difference must roll back all group inserts")
}
func TestRoutingGroupShadowRejectsLikeExpansionSQLite(t *testing.T) {
	db, gdb := routingGroupFixture(t)
	verifyRoutingGroupShadowRejectsLikeExpansion(t, db, gdb, "sqlite3")
}

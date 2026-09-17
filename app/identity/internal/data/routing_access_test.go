package data

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"

	sqlite3 "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/domain/routing"
	"micro-one-api/platform/database/testutil"
	"micro-one-api/platform/database/xdb"
	"micro-one-api/platform/routingoutbox"
)

func TestDefaultRoutingPreferenceDoesNotGrantAccess(t *testing.T) {
	db := testutil.RoutingContextDB(t, "sqlite")
	r := NewRoutingBackfillRepository(db)
	ctx := context.Background()
	require.NoError(t, db.Table("users").Create(map[string]any{"id": 1, "username": "preference", "group": "default", "status": 1}).Error)
	_, err := r.BackfillRoutingGroups(ctx, []*routing.Group{{ID: 10, Key: "default"}}, true)
	require.NoError(t, err)
	require.NoError(t, r.UpdateRoutingAccess(ctx, biz.RoutingAccessChange{UserID: 1, ExpectedRevision: 1, Operation: "grant", GroupID: 20, SourceType: "admin", SourceRef: "temporary", StartsAt: 100, ExpiresAt: 200}))
	before, err := r.UserRoutingFacts(ctx, 1)
	require.NoError(t, err)
	require.NoError(t, r.UpdateRoutingAccess(ctx, biz.RoutingAccessChange{UserID: 1, ExpectedRevision: 2, Operation: "default", GroupID: 20, GroupKey: "vip"}))
	after, err := r.UserRoutingFacts(ctx, 1)
	require.NoError(t, err)
	require.EqualValues(t, 20, after.DefaultGroupID)
	require.Equal(t, before.Grants, after.Grants, "a preference must not add or remove any grant")
	require.Empty(t, routing.AccessSources(after, &routing.Group{ID: 20, Status: "enabled"}, 200), "expired access must not survive a default change")
}

func TestRoutingAccessAndFixedTokens(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			t.Setenv("IDENTITY_ROUTING_V2", "true")
			db := testutil.RoutingContextDB(t, driver)
			r := NewRoutingBackfillRepository(db)
			ctx := context.Background()
			require.NoError(t, db.Table("users").Create(map[string]any{"id": 1, "username": "multi", "group": "default", "status": 1}).Error)
			_, err := r.BackfillRoutingGroups(ctx, []*routing.Group{{ID: 10, Key: "default"}}, true)
			require.NoError(t, err)
			token := &biz.Token{UserID: 1, Name: "fixed", Key: "sk-test-key", KeyHash: "test-hash", Status: 1, RoutingMode: "fixed", RoutingGroupID: 20, RoutingRevision: 1, UnlimitedQuota: true}
			require.NoError(t, r.CreateToken(ctx, token))
			read, err := r.FindTokenByID(ctx, 1, token.ID)
			require.NoError(t, err)
			require.EqualValues(t, 20, read.RoutingGroupID)
			c := biz.RoutingAccessChange{UserID: 1, ExpectedRevision: 1, Operation: "grant", GroupID: 20, SourceType: "admin", SourceRef: "a", StartsAt: 100, ExpiresAt: 200}
			require.NoError(t, r.UpdateRoutingAccess(ctx, c))
			require.ErrorIs(t, r.UpdateRoutingAccess(ctx, c), biz.ErrRoutingAccessConflict)
			f, err := r.GetRoutingFacts(ctx, 1, token.ID, "default")
			require.NoError(t, err)
			require.Equal(t, "fixed", f.TokenMode)
			require.Len(t, f.Grants, 2)
			g := &routing.Group{ID: 20, Key: "vip", Status: "enabled", Revision: 1}
			_, err = routing.Resolve(1, token.ID, f, g, 150)
			require.NoError(t, err)
			c.ExpectedRevision = 2
			c.SourceRef = "b"
			c.ExpiresAt = 0
			require.NoError(t, r.UpdateRoutingAccess(ctx, c))
			c.ExpectedRevision = 3
			c.SourceRef = "a"
			c.Operation = "revoke"
			require.NoError(t, r.UpdateRoutingAccess(ctx, c))
			f, err = r.UserRoutingFacts(ctx, 1)
			require.NoError(t, err)
			require.Len(t, routing.AccessSources(f, g, 300), 1)
			require.Len(t, f.TokenReferences, 1)
			// Changing the preference keeps every independent source, including
			// the original migration grant.
			require.NoError(t, r.UpdateRoutingAccess(ctx, biz.RoutingAccessChange{UserID: 1, ExpectedRevision: 4, Operation: "default", GroupID: 20, GroupKey: "vip"}))
			f, err = r.UserRoutingFacts(ctx, 1)
			require.NoError(t, err)
			require.Len(t, f.Grants, 3)
			require.EqualValues(t, 20, f.DefaultGroupID)
			// Inject a failure after the revision CAS and prove the transaction rolls back.
			require.NoError(t, db.Callback().Create().Before("gorm:create").Register("fail_grant", func(tx *gorm.DB) {
				if tx.Statement.Table == "user_routing_group_grants" {
					tx.AddError(fmt.Errorf("injected"))
				}
			}))
			c.ExpectedRevision = 5
			c.SourceRef = "b"
			require.Error(t, r.UpdateRoutingAccess(ctx, c))
			require.NoError(t, db.Callback().Create().Remove("fail_grant"))
			f, err = r.UserRoutingFacts(ctx, 1)
			require.NoError(t, err)
			require.EqualValues(t, 5, f.AccessRevision)
			require.Len(t, routing.AccessSources(f, g, 300), 1)
			oldGroup := &routing.Group{ID: 10, Key: "default", Status: "enabled", Revision: 1}
			require.Len(t, routing.AccessSources(f, oldGroup, 300), 1, "changing a preference must not revoke an independent grant")
			require.NoError(t, r.UpdateRoutingAccess(ctx, c))
			f, err = r.GetRoutingFacts(ctx, 1, token.ID, "vip")
			require.NoError(t, err)
			// Revoking the last explicit grant denies access even if the group
			// is still the user's default preference.
			_, err = routing.Resolve(1, token.ID, f, g, 300)
			require.Error(t, err)
			_, err = r.SetTokenRouting(ctx, 999, token.ID, "inherit", 0, 1, nil)
			require.ErrorIs(t, err, biz.ErrRoutingAccessConflict)
			rev, err := r.SetTokenRouting(ctx, 1, token.ID, "inherit", 0, 1, nil)
			require.NoError(t, err)
			require.EqualValues(t, 2, rev)
			_, err = r.SetTokenRouting(ctx, 1, token.ID, "fixed", 10, 1, nil)
			require.ErrorIs(t, err, biz.ErrRoutingAccessConflict)
			read, err = r.FindTokenByID(ctx, 1, token.ID)
			require.NoError(t, err)
			require.Zero(t, read.RoutingGroupID)
			var pending int64
			require.NoError(t, db.Table("routing_change_outbox").Where("owner = ? AND delivered_at = 0", "identity").Count(&pending).Error)
			require.EqualValues(t, 6, pending, "five access mutations and one token change; failed writes create no event")
		})
	}
}

func TestOrderedTokenGroupOrders(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			t.Setenv("IDENTITY_ROUTING_V2", "true")
			db := testutil.RoutingContextDB(t, driver)
			r := NewRoutingBackfillRepository(db)
			ctx := context.Background()
			require.NoError(t, db.Table("users").Create(map[string]any{"id": 1, "username": "ord", "group": "default", "status": 1}).Error)
			_, err := r.BackfillRoutingGroups(ctx, []*routing.Group{{ID: 10, Key: "default"}}, true)
			require.NoError(t, err)

			token := &biz.Token{UserID: 1, Name: "ordered", Key: "sk-ordered-key", KeyHash: "ordered-hash", Status: 1, RoutingMode: "ordered", RoutingGroupIDs: []int64{20, 30}, RoutingRevision: 1, UnlimitedQuota: true}
			require.NoError(t, r.CreateToken(ctx, token))
			for i, gid := range []int64{20, 30} {
				require.NoError(t, r.UpdateRoutingAccess(ctx, biz.RoutingAccessChange{UserID: 1, ExpectedRevision: int64(i + 1), Operation: "grant", GroupID: gid, SourceType: "admin", SourceRef: "ord"}))
			}
			f, err := r.GetRoutingFacts(ctx, 1, token.ID, "default")
			require.NoError(t, err)
			require.Equal(t, "ordered", f.TokenMode)
			require.Equal(t, []int64{20, 30}, f.TokenGroupIDs)
			g := &routing.Group{ID: 20, Key: "g20", Status: "enabled", Revision: 1}
			_, err = routing.ResolveOrdered(1, token.ID, f, g, 0, 100)
			require.NoError(t, err)
			_, err = routing.ResolveOrdered(1, token.ID, f, &routing.Group{ID: 30, Key: "g30", Status: "enabled", Revision: 1}, 1, 100)
			require.NoError(t, err)

			// Admin facts expose the ordered list per token.
			uf, err := r.UserRoutingFacts(ctx, 1)
			require.NoError(t, err)
			require.Len(t, uf.TokenReferences, 1)
			require.Equal(t, []int64{20, 30}, uf.TokenReferences[0].GroupIDs)

			// Replace the list (order matters) with a CAS revision bump.
			rev, err := r.SetTokenRouting(ctx, 1, token.ID, "ordered", 0, 1, []int64{30, 20})
			require.NoError(t, err)
			require.EqualValues(t, 2, rev)
			f, err = r.GetRoutingFacts(ctx, 1, token.ID, "default")
			require.NoError(t, err)
			require.Equal(t, []int64{30, 20}, f.TokenGroupIDs)

			// Switching back to inherit clears the ordered rows.
			_, err = r.SetTokenRouting(ctx, 1, token.ID, "inherit", 0, 2, nil)
			require.NoError(t, err)
			f, err = r.GetRoutingFacts(ctx, 1, token.ID, "default")
			require.NoError(t, err)
			require.Equal(t, "inherit", f.TokenMode)
			require.Nil(t, f.TokenGroupIDs)
			var rows int64
			require.NoError(t, db.Table("token_routing_group_orders").Where("token_id = ?", token.ID).Count(&rows).Error)
			require.Zero(t, rows)

			// Deleting the token cascades the ordered rows.
			_, err = r.SetTokenRouting(ctx, 1, token.ID, "ordered", 0, 3, []int64{20})
			require.NoError(t, err)
			require.NoError(t, r.DeleteToken(ctx, 1, token.ID))
			require.NoError(t, db.Table("token_routing_group_orders").Where("token_id = ?", token.ID).Count(&rows).Error)
			require.Zero(t, rows)
		})
	}
}

// TestRoutingAccessRevokeIsIdempotent pins the no-op contract of a revoke that
// targets a grant which is not active: it must not mint a dead 'revoked' row,
// bump the revision, or emit an outbox event.
func TestRoutingAccessRevokeIsIdempotent(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			t.Setenv("IDENTITY_ROUTING_V2", "true")
			db := testutil.RoutingContextDB(t, driver)
			r := NewRoutingBackfillRepository(db)
			ctx := context.Background()
			require.NoError(t, db.Table("users").Create(map[string]any{"id": 1, "username": "rev", "group": "default", "status": 1}).Error)
			_, err := r.BackfillRoutingGroups(ctx, []*routing.Group{{ID: 10, Key: "default"}}, true)
			require.NoError(t, err)

			grant := biz.RoutingAccessChange{UserID: 1, ExpectedRevision: 1, Operation: "grant", GroupID: 20, SourceType: "admin", SourceRef: "a"}
			require.NoError(t, r.UpdateRoutingAccess(ctx, grant))

			revoke := grant
			revoke.ExpectedRevision = 2
			revoke.Operation = "revoke"
			require.NoError(t, r.UpdateRoutingAccess(ctx, revoke))
			f, err := r.UserRoutingFacts(ctx, 1)
			require.NoError(t, err)
			require.EqualValues(t, 3, f.AccessRevision)

			// Replaying the revoke is a no-op: no revision bump, no new event.
			replay := revoke
			replay.ExpectedRevision = 3
			require.NoError(t, r.UpdateRoutingAccess(ctx, replay))
			f, err = r.UserRoutingFacts(ctx, 1)
			require.NoError(t, err)
			require.EqualValues(t, 3, f.AccessRevision, "a replayed revoke must not bump the revision")

			// Revoking a grant that never existed is a no-op too.
			unknown := revoke
			unknown.ExpectedRevision = 3
			unknown.SourceRef = "missing"
			require.NoError(t, r.UpdateRoutingAccess(ctx, unknown))
			f, err = r.UserRoutingFacts(ctx, 1)
			require.NoError(t, err)
			require.EqualValues(t, 3, f.AccessRevision, "revoking an unknown grant must not bump the revision")

			var pending int64
			require.NoError(t, db.Table("routing_change_outbox").Where("owner = ? AND delivered_at = 0", "identity").Count(&pending).Error)
			require.EqualValues(t, 2, pending, "grant and revoke each emit one event; neither no-op revoke emits any")
		})
	}
}

// Regression for release run 35197009912 (sqlite3 sessions phase): on the
// shared-file topology a concurrent cross-service commit between the
// transaction's snapshot read and its first write stales the WAL snapshot,
// and SQLite fails the write with "database is locked"
// (SQLITE_BUSY_SNAPSHOT). The transaction must retry on a fresh snapshot
// instead of surfacing a 503.
//
// The poison is deterministic — a second pool commits exactly once, after
// the first attempt's snapshot read and before its CAS write — so this test
// is red without RetryTxOnBusy and green with it. The poison write needs
// its own pool because xdb clamps SQLite to MaxOpenConns=1 and the running
// transaction already holds that connection.
func TestUpdateRoutingAccessRetriesStaleSnapshot(t *testing.T) {
	dbFile := filepath.Join(t.TempDir(), "identity.db")
	dsn := "file:" + dbFile + "?_busy_timeout=50&_journal_mode=WAL&_foreign_keys=on"
	open := func() *gorm.DB {
		db, err := xdb.Open(xdb.DatabaseConfig{Driver: xdb.DriverSQLite3, DSN: dsn})
		require.NoError(t, err)
		return db
	}
	db := open()
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })

	require.NoError(t, db.Exec("CREATE TABLE users (id INTEGER PRIMARY KEY, group_key TEXT, routing_access_revision INTEGER, default_routing_group_id INTEGER, public_group_access TEXT, status INTEGER)").Error)
	require.NoError(t, db.Exec("CREATE TABLE user_routing_group_grants (user_id INTEGER, routing_group_id INTEGER, source_type TEXT, source_ref TEXT, starts_at INTEGER, expires_at INTEGER, status TEXT, PRIMARY KEY (user_id, routing_group_id, source_type, source_ref))").Error)
	require.NoError(t, db.Exec("CREATE TABLE routing_change_outbox (id TEXT PRIMARY KEY, owner TEXT, kind TEXT, aggregate_id INTEGER, revision INTEGER, created_at INTEGER, delivered_at INTEGER)").Error)
	require.NoError(t, db.Exec("INSERT INTO users (id, group_key, routing_access_revision, default_routing_group_id, public_group_access, status) VALUES (1, 'default', 1, 10, 'explicit_only', 1)").Error)

	r := NewRoutingBackfillRepository(db)
	var attempts atomic.Int32
	poisoned := false
	poison := func(tx *gorm.DB) {
		attempts.Add(1)
		if poisoned || tx.Statement == nil || tx.Statement.Table != "user_routing_group_grants" {
			return
		}
		poisoned = true
		// Concurrent cross-service commit, sequenced right after the snapshot
		// read and before the revision CAS: stales the first attempt's WAL
		// snapshot so the CAS hits SQLITE_BUSY_SNAPSHOT exactly like the
		// production race. Only poisons the first attempt; the retried
		// transaction replays on a fresh snapshot.
		writer := open()
		ws, _ := writer.DB()
		defer func() { _ = ws.Close() }()
		err := writer.Exec("UPDATE users SET public_group_access = 'all' WHERE id = 1").Error
		require.NoError(t, err)
	}
	require.NoError(t, db.Callback().Query().After("gorm:after_query").Register("poison_routing_snapshot", poison))
	defer func() { _ = db.Callback().Query().Remove("poison_routing_snapshot") }()

	change := biz.RoutingAccessChange{UserID: 1, ExpectedRevision: 1, Operation: "grant", GroupID: 20, SourceType: "admin", SourceRef: "fixture"}
	require.NoError(t, r.UpdateRoutingAccess(context.Background(), change))
	require.True(t, poisoned, "the poison hook must have fired")
	require.EqualValues(t, 2, attempts.Load(), "first attempt poisoned, second replays on a fresh snapshot")

	var revision int64
	require.NoError(t, db.Table("users").Select("routing_access_revision").Where("id = 1").Scan(&revision).Error)
	require.EqualValues(t, 2, revision, "exactly one committed revision bump")
	var pending int64
	require.NoError(t, db.Table("routing_change_outbox").Where("delivered_at = 0").Count(&pending).Error)
	require.EqualValues(t, 1, pending, "exactly one outbox event; the failed attempt must commit nothing")
}

// Companion red-without assertion: with a single attempt the same poison
// sequence surfaces SQLITE_BUSY, proving the retry above (not luck) is what
// makes the stale snapshot recoverable.
func TestUpdateRoutingAccessStaleSnapshotFailsWithoutRetry(t *testing.T) {
	dbFile := filepath.Join(t.TempDir(), "identity.db")
	dsn := "file:" + dbFile + "?_busy_timeout=50&_journal_mode=WAL&_foreign_keys=on"
	open := func() *gorm.DB {
		db, err := xdb.Open(xdb.DatabaseConfig{Driver: xdb.DriverSQLite3, DSN: dsn})
		require.NoError(t, err)
		return db
	}
	db := open()
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })

	require.NoError(t, db.Exec("CREATE TABLE users (id INTEGER PRIMARY KEY, group_key TEXT, routing_access_revision INTEGER, default_routing_group_id INTEGER, public_group_access TEXT, status INTEGER)").Error)
	require.NoError(t, db.Exec("CREATE TABLE user_routing_group_grants (user_id INTEGER, routing_group_id INTEGER, source_type TEXT, source_ref TEXT, starts_at INTEGER, expires_at INTEGER, status TEXT, PRIMARY KEY (user_id, routing_group_id, source_type, source_ref))").Error)
	require.NoError(t, db.Exec("CREATE TABLE routing_change_outbox (id TEXT PRIMARY KEY, owner TEXT, kind TEXT, aggregate_id INTEGER, revision INTEGER, created_at INTEGER, delivered_at INTEGER)").Error)
	require.NoError(t, db.Exec("INSERT INTO users (id, group_key, routing_access_revision, default_routing_group_id, public_group_access, status) VALUES (1, 'default', 1, 10, 'explicit_only', 1)").Error)

	poisoned := false
	poison := func(tx *gorm.DB) {
		if poisoned || tx.Statement == nil || tx.Statement.Table != "user_routing_group_grants" {
			return
		}
		poisoned = true
		writer := open()
		ws, _ := writer.DB()
		defer func() { _ = ws.Close() }()
		err := writer.Exec("UPDATE users SET public_group_access = 'all' WHERE id = 1").Error
		require.NoError(t, err)
	}
	require.NoError(t, db.Callback().Query().After("gorm:after_query").Register("poison_routing_snapshot", poison))
	defer func() { _ = db.Callback().Query().Remove("poison_routing_snapshot") }()

	change := biz.RoutingAccessChange{UserID: 1, ExpectedRevision: 1, Operation: "grant", GroupID: 20, SourceType: "admin", SourceRef: "fixture"}
	// attempts=1 reproduces the pre-fix shape (plain Transaction, no retry).
	err := xdb.RetryTxOnBusy(context.Background(), db, 1, func(tx *gorm.DB) error {
		var noop int64
		if err := tx.Model(&routingGrantModel{}).Where("user_id = ?", change.UserID).Limit(1).Count(&noop).Error; err != nil {
			return err
		}
		result := tx.Table("users").Where("id = ? AND routing_access_revision = ? AND status = ?", change.UserID, change.ExpectedRevision, biz.UserStatusEnabled).Updates(map[string]any{"routing_access_revision": change.ExpectedRevision + 1})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return biz.ErrRoutingAccessConflict
		}
		g := routingGrantModel{UserID: change.UserID, RoutingGroupID: change.GroupID, SourceType: change.SourceType, SourceRef: change.SourceRef, StartsAt: change.StartsAt, ExpiresAt: change.ExpiresAt, Status: "active"}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}, {Name: "routing_group_id"}, {Name: "source_type"}, {Name: "source_ref"}}, DoUpdates: clause.AssignmentColumns([]string{"starts_at", "expires_at", "status"})}).Create(&g).Error; err != nil {
			return err
		}
		return routingoutbox.Enqueue(tx, "identity", "user", change.UserID, change.ExpectedRevision+1)
	})
	require.True(t, poisoned, "the poison hook must have fired")
	var sqliteErr sqlite3.Error
	require.True(t, errors.As(err, &sqliteErr) && sqliteErr.Code == sqlite3.ErrBusy, "without whole-transaction retry the stale snapshot must surface SQLITE_BUSY: %v", err)

	var revision int64
	require.NoError(t, db.Table("users").Select("routing_access_revision").Where("id = 1").Scan(&revision).Error)
	require.EqualValues(t, 1, revision, "the failed attempt must roll back the revision bump")
}

package data

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/domain/routing"
	"micro-one-api/platform/database/testutil"
	"testing"
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

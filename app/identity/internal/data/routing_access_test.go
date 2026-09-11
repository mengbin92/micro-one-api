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
			// Changing the preference keeps every independent source, including migration.
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
			require.NoError(t, r.UpdateRoutingAccess(ctx, c))
			f, err = r.GetRoutingFacts(ctx, 1, token.ID, "vip")
			require.NoError(t, err)
			_, err = routing.Resolve(1, token.ID, f, g, 300)
			require.Error(t, err)
			_, err = r.SetTokenRouting(ctx, 999, token.ID, "inherit", 0, 1)
			require.ErrorIs(t, err, biz.ErrRoutingAccessConflict)
			rev, err := r.SetTokenRouting(ctx, 1, token.ID, "inherit", 0, 1)
			require.NoError(t, err)
			require.EqualValues(t, 2, rev)
			_, err = r.SetTokenRouting(ctx, 1, token.ID, "fixed", 10, 1)
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

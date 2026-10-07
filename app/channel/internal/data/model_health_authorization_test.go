package data

import (
	"context"
	"testing"
	"time"

	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/authorization"
	authztest "micro-one-api/domain/authorization/testutil"
	dbtest "micro-one-api/platform/database/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression: /api/admin/model-health returned 403 "authorization denied" in
// production IAM mode even for admins holding monitor.health.model.read. The
// serving constructor leaves routingGroupRelations disabled, so
// sourceHealthQuery short-circuited to ErrDenied for every scoped read. The
// legacy CSV fallback must now serve both All-scope reads and group-scoped
// reads without the relation tables.
func TestRepository_ListModelHealthWithIAMReadScope(t *testing.T) {
	repo := setupModelTestDB(t)
	ctx := context.Background()
	require.NoError(t, repo.RecordModelHealth(ctx, &biz.ModelHealthOutcome{
		SourceKind: "channel", SourceID: 9, ModelID: "kimi-k3",
		UpstreamModelID: "kimi-k3-prod", Error: "no healthy model",
		ResponseTimeMs: 100, CheckedAt: 1,
	}))

	scoped := authorization.WithQueryScope(ctx, "monitor.health.model.read", authztest.All())
	states, total, err := repo.ListModelHealth(scoped, 1, 10, biz.ListModelHealthFilter{})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, states, 1)
}

// Without the relation projection (routingGroupRelations=false), a group-scoped read must filter through the legacy CSV
// group column of the source rows: the allowed group sees its rows, a
// non-matching group sees none, and no scope still bypasses filtering.
func TestRepository_ListModelHealthGroupScopeLegacyCSVFilter(t *testing.T) {
	repo := setupModelTestDB(t)
	db := repo.db
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS routing_groups (id INTEGER PRIMARY KEY, key TEXT NOT NULL)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO routing_groups (id, key) VALUES (1, 'vip'), (2, 'standard')`).Error)
	require.NoError(t, db.Exec(`ALTER TABLE channels ADD COLUMN "group" TEXT NOT NULL DEFAULT ''`).Error)
	require.NoError(t, db.Exec(`INSERT INTO channels (id, "group") VALUES (9, 'vip,public'), (10, 'standard')`).Error)
	require.NoError(t, db.Exec(`ALTER TABLE subscription_accounts ADD COLUMN "group" TEXT NOT NULL DEFAULT ''`).Error)

	ctx := context.Background()
	record := func(sourceID int64, modelID string) {
		require.NoError(t, repo.RecordModelHealth(ctx, &biz.ModelHealthOutcome{
			SourceKind: "channel", SourceID: sourceID, ModelID: modelID,
			UpstreamModelID: modelID, Error: "no healthy model",
			ResponseTimeMs: 100, CheckedAt: 1,
		}))
	}
	record(9, "vip-model")
	record(10, "standard-model")

	states, total, err := repo.ListModelHealth(ctx, 1, 10, biz.ListModelHealthFilter{})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)

	vip := authorization.WithQueryScope(ctx, "monitor.health.model.read", authztest.Groups(1))
	states, total, err = repo.ListModelHealth(vip, 1, 10, biz.ListModelHealthFilter{})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, states, 1)
	assert.Equal(t, "vip-model", states[0].ModelID)

	standard := authorization.WithQueryScope(ctx, "monitor.health.model.read", authztest.Groups(2))
	_, total, err = repo.ListModelHealth(standard, 1, 10, biz.ListModelHealthFilter{})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)

	unknown := authorization.WithQueryScope(ctx, "monitor.health.model.read", authztest.Groups(999))
	_, total, err = repo.ListModelHealth(unknown, 1, 10, biz.ListModelHealthFilter{})
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
}

func TestRepository_ListModelHealthGroupKeysAreLiteral(t *testing.T) {
	for _, key := range []string{"%", "vip_", "VIP"} {
		t.Run(key, func(t *testing.T) {
			repo := setupModelTestDB(t)
			require.NoError(t, repo.db.Exec(`CREATE TABLE routing_groups (id INTEGER PRIMARY KEY, key TEXT NOT NULL)`).Error)
			require.NoError(t, repo.db.Exec(`INSERT INTO routing_groups (id, key) VALUES (1, ?)`, key).Error)
			require.NoError(t, repo.db.Exec(`ALTER TABLE channels ADD COLUMN "group" TEXT NOT NULL DEFAULT ''`).Error)
			require.NoError(t, repo.db.Exec(`ALTER TABLE subscription_accounts ADD COLUMN "group" TEXT NOT NULL DEFAULT ''`).Error)
			require.NoError(t, repo.db.Exec(`INSERT INTO channels (id, "group") VALUES (9, ?), (10, 'vip1'), (11, 'vip')`, key).Error)
			for _, id := range []int64{9, 10, 11} {
				require.NoError(t, repo.RecordModelHealth(context.Background(), &biz.ModelHealthOutcome{SourceKind: "channel", SourceID: id, ModelID: "model", UpstreamModelID: "model", CheckedAt: 1}))
			}
			ctx := authorization.WithQueryScope(context.Background(), "monitor.health.model.read", authztest.Groups(1))
			states, total, err := repo.ListModelHealth(ctx, 1, 10, biz.ListModelHealthFilter{})
			require.NoError(t, err)
			require.EqualValues(t, 1, total, "group keys must not act as LIKE patterns or ignore case")
			require.Len(t, states, 1)
			require.EqualValues(t, 9, states[0].SourceID)
		})
	}
}

func TestRepository_ListModelHealthAllowDenyAndPagination(t *testing.T) {
	for _, relations := range []bool{false, true} {
		name := "legacy"
		if relations {
			name = "relations"
		}
		t.Run(name, func(t *testing.T) {
			repo := setupModelTestDB(t)
			repo.routingGroupRelations = relations
			db := repo.db
			require.NoError(t, db.Exec(`CREATE TABLE routing_groups (id INTEGER PRIMARY KEY, key TEXT NOT NULL)`).Error)
			require.NoError(t, db.Exec(`INSERT INTO routing_groups (id, key) VALUES (1, 'vip'), (2, 'standard')`).Error)
			require.NoError(t, db.Exec(`ALTER TABLE channels ADD COLUMN "group" TEXT NOT NULL DEFAULT ''`).Error)
			require.NoError(t, db.Exec(`ALTER TABLE subscription_accounts ADD COLUMN "group" TEXT NOT NULL DEFAULT ''`).Error)
			require.NoError(t, db.Exec(`INSERT INTO channels (id, "group") VALUES (9, 'vip,standard'), (10, 'standard')`).Error)
			require.NoError(t, db.Exec(`INSERT INTO subscription_accounts (id, "group") VALUES (9, 'vip'), (10, 'standard')`).Error)
			if relations {
				require.NoError(t, db.Exec(`CREATE TABLE channel_routing_groups (channel_id INTEGER, routing_group_id INTEGER)`).Error)
				require.NoError(t, db.Exec(`CREATE TABLE account_routing_groups (subscription_account_id INTEGER, routing_group_id INTEGER)`).Error)
				require.NoError(t, db.Exec(`INSERT INTO channel_routing_groups VALUES (9, 1), (9, 2), (10, 2)`).Error)
				require.NoError(t, db.Exec(`INSERT INTO account_routing_groups VALUES (9, 1), (10, 2)`).Error)
				// Serving relation reads must not accidentally consult stale CSVs.
				require.NoError(t, db.Exec(`UPDATE channels SET "group" = 'stale'`).Error)
				require.NoError(t, db.Exec(`UPDATE subscription_accounts SET "group" = 'stale'`).Error)
			}
			for _, kind := range []string{"channel", "subscription"} {
				for _, id := range []int64{9, 10, 99} {
					require.NoError(t, repo.RecordModelHealth(context.Background(), &biz.ModelHealthOutcome{SourceKind: kind, SourceID: id, ModelID: "model", UpstreamModelID: "model", CheckedAt: id}))
				}
			}
			uc := biz.NewModelUsecase(repo)
			resolver := &authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{"monitor.health.model.read": authztest.Groups(1)}}
			uc.SetAuthorization(resolver)
			request := authztest.Context()
			states, total, err := uc.ListModelHealth(request, 1, 1, biz.ListModelHealthFilter{})
			require.NoError(t, err)
			require.EqualValues(t, 2, total)
			require.Len(t, states, 1)
			require.Equal(t, "subscription", states[0].SourceKind)
			states, total, err = uc.ListModelHealth(request, 2, 1, biz.ListModelHealthFilter{})
			require.NoError(t, err)
			require.EqualValues(t, 2, total)
			require.Len(t, states, 1)
			require.Equal(t, "channel", states[0].SourceKind)
			states, total, err = uc.ListModelHealth(request, 1, 10, biz.ListModelHealthFilter{SourceKind: "subscription", Keyword: "model"})
			require.NoError(t, err)
			require.EqualValues(t, 1, total)
			require.Len(t, states, 1)
			require.EqualValues(t, 9, states[0].SourceID)
			scope := authztest.All()
			scope.Deny = authztest.Groups(2).Allow
			resolver.Scopes["monitor.health.model.read"] = scope
			states, total, err = uc.ListModelHealth(request, 1, 10, biz.ListModelHealthFilter{})
			require.NoError(t, err)
			require.EqualValues(t, 3, total) // vip subscription plus two orphan observations
			for _, state := range states {
				require.False(t, state.SourceID == 10 || (state.SourceID == 9 && state.SourceKind == "channel"))
			}
			scope = authztest.Resources(states[0].ID)
			resolver.Scopes["monitor.health.model.read"] = scope
			states, total, err = uc.ListModelHealth(request, 1, 10, biz.ListModelHealthFilter{})
			require.NoError(t, err)
			require.EqualValues(t, 1, total)
			require.Len(t, states, 1)
			resolver.Scopes["monitor.health.model.read"] = authorization.QueryScope{ActorID: 1}
			_, total, err = uc.ListModelHealth(request, 1, 10, biz.ListModelHealthFilter{})
			require.ErrorIs(t, err, authorization.ErrDenied)
			require.Zero(t, total)
			scope = authztest.All()
			scope.ValidUntil = time.Now().Add(-time.Minute)
			resolver.Scopes["monitor.health.model.read"] = scope
			_, _, err = uc.ListModelHealth(request, 1, 10, biz.ListModelHealthFilter{})
			require.ErrorIs(t, err, authorization.ErrDenied)
		})
	}
}

// Exercise the production schema and dialects with the serving constructor's
// legacy read shape. External databases are opt-in disposable local fixtures.
func TestRepository_ListModelHealthLegacyMembershipDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := dbtest.RoutingContextDB(t, driver)
			repo := &Repository{db: db, encKey: []byte("0123456789abcdef0123456789abcdef")}
			ctx := context.Background()
			for _, key := range []string{"%", "vip_", "VIP", "组!\\名"} {
				t.Run(key, func(t *testing.T) {
					group := routingGroupModel{Key: key, DisplayName: key, Status: "enabled", AccessMode: "restricted", Revision: 1}
					require.NoError(t, db.Create(&group).Error)
					var ids []int64
					for _, csv := range []string{key + ",other", "vip1", "vip"} {
						channel, err := repo.channelToModel(&biz.Channel{Name: "health", Group: csv, Key: "unused", Status: 1})
						require.NoError(t, err)
						require.NoError(t, db.Create(channel).Error)
						ids = append(ids, channel.ID)
						require.NoError(t, repo.RecordModelHealth(ctx, &biz.ModelHealthOutcome{SourceKind: "channel", SourceID: channel.ID, ModelID: "model", UpstreamModelID: "model", CheckedAt: 1}))
					}
					scope := authztest.Groups(group.ID)
					states, total, err := repo.ListModelHealth(authorization.WithQueryScope(ctx, "monitor.health.model.read", scope), 1, 100, biz.ListModelHealthFilter{})
					require.NoError(t, err)
					require.EqualValues(t, 1, total)
					require.Len(t, states, 1)
					require.Equal(t, ids[0], states[0].SourceID)
					// The same membership predicate is used by the channel owner's list.
					query, err := repo.channelScope(authorization.WithQueryScope(ctx, "channel.channel.list", scope), db.Model(&channelModel{}), "channels")
					require.NoError(t, err)
					var count int64
					require.NoError(t, query.Count(&count).Error)
					require.EqualValues(t, 1, count)
				})
			}
		})
	}
}

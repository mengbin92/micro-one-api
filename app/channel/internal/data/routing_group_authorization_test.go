package data

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/authorization"
	authztest "micro-one-api/domain/authorization/testutil"
	"micro-one-api/domain/routing"
	dbtest "micro-one-api/platform/database/testutil"
)

func TestIAMB2RoutingGroupOwnerDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := dbtest.RoutingContextDB(t, driver)
			store := &Repository{db: db, routingGroupDualWrite: true, routingGroupRelations: true, encKey: []byte("0123456789abcdef0123456789abcdef")}
			repo := NewRoutingGroupRepo(store)
			uc := biz.NewRoutingGroupUsecase(repo)
			raw := context.Background()
			first, err := uc.Create(raw, "first", "First", "", "restricted")
			require.NoError(t, err)
			second, err := uc.Create(raw, "second", "Second", "", "restricted")
			require.NoError(t, err)
			id := first.Group.ID
			channel := &biz.Channel{Name: "member", Key: "hidden", Group: "first", Status: biz.ChannelStatusEnabled, RestrictModels: true}
			require.NoError(t, store.CreateChannel(raw, channel))
			policy := &authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{
				"channel.routing_group.list":                     authztest.Resources(id),
				"channel.routing_group.read":                     authztest.Resources(id),
				"channel.routing_group.update":                   authztest.Resources(id),
				"channel.routing_group.resource_override.update": authztest.Resources(id),
			}}
			uc.SetAuthorization(policy)
			request := authztest.Context()
			rows, err := uc.List(request, biz.RoutingGroupListOptions{Limit: 1})
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Equal(t, id, rows[0].ID)
			rows, err = uc.List(request, biz.RoutingGroupListOptions{Offset: 1, Limit: 1})
			require.NoError(t, err)
			require.Empty(t, rows)
			_, err = uc.Get(request, second.Group.ID)
			require.ErrorIs(t, err, authorization.ErrDenied)
			detail, err := uc.Get(request, id)
			require.NoError(t, err)
			require.False(t, detail.MembersVisible)
			require.Empty(t, detail.Resources)
			require.Empty(t, detail.ModelGrants)
			policy.Scopes["channel.routing_group.members.read"] = authztest.Resources(id)
			detail, err = uc.Get(request, id)
			require.NoError(t, err)
			require.True(t, detail.MembersVisible)
			require.Len(t, detail.Resources, 1)
			q := authztest.All()
			q.Deny = authztest.Resources(second.Group.ID).Allow
			policy.Scopes["channel.routing_group.list"] = q
			rows, err = uc.List(request, biz.RoutingGroupListOptions{Limit: 201})
			require.NoError(t, err)
			require.Len(t, rows, 1)
			// Create has no existing resource ID; resource-limited authority cannot
			// invent new groups. A global create grants no independent read authority.
			policy.Scopes["channel.routing_group.create"] = authztest.Resources(id)
			_, err = uc.Create(request, "forbidden", "", "", "restricted")
			require.ErrorIs(t, err, authorization.ErrDenied)
			policy.Scopes["channel.routing_group.create"] = authztest.All()
			created, err := uc.Create(request, "created", "", "", "restricted")
			require.NoError(t, err)
			require.Equal(t, "disabled", created.Group.Status)
			require.True(t, created.MembersVisible)
			_, err = uc.Get(request, created.Group.ID)
			require.ErrorIs(t, err, authorization.ErrDenied)
			// Actual enable/disable is independent of metadata update. Neither a
			// missing grant nor stale CAS may change state or append an outbox event.
			before, err := repo.GetRoutingGroup(raw, id)
			require.NoError(t, err)
			var outbox int64
			require.NoError(t, db.Table("routing_change_outbox").Count(&outbox).Error)
			_, err = uc.SetState(request, id, before.Group.Revision, "enabled", "restricted")
			require.ErrorIs(t, err, authorization.ErrDenied)
			current, err := repo.GetRoutingGroup(raw, id)
			require.NoError(t, err)
			require.Equal(t, before.Group.Revision, current.Group.Revision)
			var after int64
			require.NoError(t, db.Table("routing_change_outbox").Count(&after).Error)
			require.Equal(t, outbox, after)
			policy.Scopes["channel.routing_group.enable"] = authztest.Resources(id)
			delete(policy.Scopes, "channel.routing_group.read")
			delete(policy.Scopes, "channel.routing_group.members.read")
			updated, err := uc.SetState(request, id, current.Group.Revision, "enabled", "restricted")
			require.NoError(t, err, "write-only authority does not require a postcommit read")
			require.Equal(t, "enabled", updated.Group.Status)
			require.False(t, updated.MembersVisible)
			_, err = uc.SetState(request, id, current.Group.Revision, "enabled", "restricted")
			require.True(t, errors.Is(err, biz.ErrRoutingGroupBaselineConflict))
			priority := int64(12)
			updated, err = uc.SetResourceOverrides(request, id, routing.Source{Kind: routing.Channel, ID: channel.ID}, &priority, nil)
			require.NoError(t, err, "override write does not require independent member read")
			require.False(t, updated.MembersVisible)
			actual, err := repo.GetRoutingGroup(raw, id)
			require.NoError(t, err)
			require.EqualValues(t, 12, actual.Resources[0].Priority)
			_, err = uc.SetResourceOverrides(request, second.Group.ID, routing.Source{Kind: routing.Channel, ID: channel.ID}, &priority, nil)
			require.ErrorIs(t, err, authorization.ErrDenied)
			// Expiry is checked again at the transaction edge, not only in transport.
			expired := authztest.Resources(id)
			expired.ValidUntil = time.Now().Add(-time.Second)
			stale := authorization.WithQueryScope(raw, "channel.routing_group.update", expired)
			stateRepo := repo.(biz.RoutingGroupStateRepo)
			err = stateRepo.SetRoutingGroupState(stale, id, actual.Group.Revision, "enabled", "restricted")
			require.ErrorIs(t, err, authorization.ErrDenied)
		})
	}
}

package data

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/authorization"
	authztest "micro-one-api/domain/authorization/testutil"
	"micro-one-api/domain/routing"
	dbtest "micro-one-api/platform/database/testutil"
	"testing"
)

func TestIAMB2RoutingGroupManagementDialects(t *testing.T) {
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
			one := &biz.Channel{Name: "shared", Key: "hidden", Group: "first,second", Models: []string{"model"}, Status: biz.ChannelStatusEnabled}
			two := &biz.Channel{Name: "candidate", Key: "hidden", Group: "second", Models: []string{"model"}, Status: biz.ChannelStatusEnabled}
			require.NoError(t, store.CreateChannel(raw, one))
			require.NoError(t, store.CreateChannel(raw, two))
			account := &biz.SubscriptionAccount{Name: "subscription candidate", Platform: "openai", AccountType: "oauth", Group: "second", Models: []string{"model"}, Status: biz.ChannelStatusEnabled}
			require.NoError(t, store.CreateSubscriptionAccount(raw, account))
			desired := []routing.Source{{Kind: routing.Channel, ID: two.ID}, {Kind: routing.Subscription, ID: account.ID}}
			policy := &authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{"channel.routing_group.members.update": authztest.Resources(first.Group.ID), "channel.channel.update": authztest.Groups(first.Group.ID), "channel.account.update": authztest.All(), "channel.routing_group.archive": authztest.Resources(first.Group.ID)}}
			uc.SetAuthorization(policy)
			request := authztest.Context()
			current, err := repo.GetRoutingGroup(raw, first.Group.ID)
			require.NoError(t, err)
			_, err = uc.ReplaceMembers(request, first.Group.ID, current.Group.Revision, nil, "remove shared source")
			require.ErrorIs(t, err, authorization.ErrDenied)
			policy.Scopes["channel.routing_group.members.update"] = authztest.Resources(first.Group.ID, second.Group.ID)
			_, err = uc.ReplaceMembers(request, first.Group.ID, current.Group.Revision, nil, "resource old facts are also mandatory")
			require.ErrorIs(t, err, authorization.ErrDenied)
			policy.Scopes["channel.channel.update"] = authztest.Groups(first.Group.ID, second.Group.ID)
			changed, err := uc.ReplaceMembers(request, first.Group.ID, current.Group.Revision, desired, "replace complete membership")
			require.NoError(t, err)
			require.Greater(t, changed.Revision, current.Group.Revision)
			var a, b channelModel
			require.NoError(t, db.First(&a, one.ID).Error)
			require.NoError(t, db.First(&b, two.ID).Error)
			require.Equal(t, "second", a.Group)
			require.Equal(t, "second,first", b.Group)
			var c subscriptionAccountModel
			require.NoError(t, db.First(&c, account.ID).Error)
			require.Equal(t, "second,first", c.Group)
			require.EqualValues(t, account.CredentialRevision+1, c.CredentialRevision)
			var firstMembers []int64
			require.NoError(t, db.Table("channel_routing_groups").Where("routing_group_id = ?", first.Group.ID).Pluck("channel_id", &firstMembers).Error)
			require.Equal(t, []int64{two.ID}, firstMembers)
			var abilities []abilityModel
			require.NoError(t, db.Where("channel_id = ?", one.ID).Find(&abilities).Error)
			require.Len(t, abilities, 1)
			require.Equal(t, "second", abilities[0].Group)
			_, err = uc.ReplaceMembers(request, first.Group.ID, current.Group.Revision, nil, "stale CAS")
			require.ErrorIs(t, err, biz.ErrRoutingGroupBaselineConflict)
			// A retained member is not a metadata write and preserves its override.
			priority := int64(12)
			require.NoError(t, db.Table("channel_routing_groups").Where("routing_group_id = ? AND channel_id = ?", first.Group.ID, two.ID).Update("priority_override", priority).Error)
			delete(policy.Scopes, "channel.channel.update")
			same, err := uc.ReplaceMembers(request, first.Group.ID, changed.Revision, desired, "retain complete set")
			require.NoError(t, err)
			var override int64
			require.NoError(t, db.Table("channel_routing_groups").Select("priority_override").Where("routing_group_id = ? AND channel_id = ?", first.Group.ID, two.ID).Scan(&override).Error)
			require.EqualValues(t, 12, override)
			// Both side-effect failures must roll back the entire replacement.
			policy.Scopes["channel.channel.update"] = authztest.Groups(first.Group.ID, second.Group.ID)
			for _, table := range []string{"routing_change_outbox", "resource_write_audits"} {
				injected := false
				callback := "test:group:rollback:" + table
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
					if tx.Statement.Table == table && !injected {
						injected = true
						tx.AddError(errors.New("injected side effect failure"))
					}
				}))
				_, err = uc.ReplaceMembers(request, first.Group.ID, same.Revision, nil, "side effect failure must roll back")
				require.Error(t, err)
				require.True(t, injected)
				require.NoError(t, db.Callback().Create().Remove(callback))
				unchanged, lookupErr := repo.GetRoutingGroup(raw, first.Group.ID)
				require.NoError(t, lookupErr)
				require.Equal(t, same.Revision, unchanged.Group.Revision)
				require.NoError(t, db.First(&b, two.ID).Error)
				require.Equal(t, "second,first", b.Group)
				require.NoError(t, db.First(&c, account.ID).Error)
				require.Equal(t, "second,first", c.Group)
				require.EqualValues(t, account.CredentialRevision+1, c.CredentialRevision)
				var members int64
				require.NoError(t, db.Table("channel_routing_groups").Where("routing_group_id = ? AND channel_id = ?", first.Group.ID, two.ID).Count(&members).Error)
				require.EqualValues(t, 1, members)
			}
			archived, err := uc.Archive(request, first.Group.ID, same.Revision, "retire routing group")
			require.NoError(t, err)
			require.Equal(t, "archived", archived.Status)
			_, err = uc.ReplaceMembers(request, first.Group.ID, archived.Revision, nil, "archived cannot be revived")
			require.ErrorIs(t, err, biz.ErrRoutingGroupInvalid)
			_, err = uc.Archive(request, second.Group.ID, second.Group.Revision, "wrong scope")
			require.ErrorIs(t, err, authorization.ErrDenied)
		})
	}
}

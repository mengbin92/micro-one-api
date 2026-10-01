package data

import (
	"context"
	"github.com/stretchr/testify/require"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/authorization"
	authztest "micro-one-api/domain/authorization/testutil"
	dbtest "micro-one-api/platform/database/testutil"
	"testing"
)

func TestIAMB2ChannelOwnerDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := dbtest.RoutingContextDB(t, driver)
			repo := &Repository{db: db, routingGroupDualWrite: true, routingGroupRelations: true, encKey: []byte("0123456789abcdef0123456789abcdef")}
			ctx := context.Background()
			g1, g2 := routingGroupModel{Key: "one", DisplayName: "one", Status: "enabled", AccessMode: "restricted", ModelAccessMode: "restricted", Revision: 1}, routingGroupModel{Key: "two", DisplayName: "two", Status: "enabled", AccessMode: "restricted", ModelAccessMode: "restricted", Revision: 1}
			require.NoError(t, db.Create(&g1).Error)
			require.NoError(t, db.Create(&g2).Error)
			shared := &biz.Channel{Name: "shared", Key: "secret", Group: "one,two", Status: 1, RestrictModels: true}
			single := &biz.Channel{Name: "one", Key: "single-secret", Group: "one", Status: 1, RestrictModels: true}
			hidden := &biz.Channel{Name: "hidden", Key: "hidden-secret", Group: "two", Status: 1, RestrictModels: true}
			for _, c := range []*biz.Channel{shared, single, hidden} {
				require.NoError(t, repo.CreateChannel(ctx, c))
			}
			policy := &authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{
				"channel.channel.list": authztest.Groups(g1.ID), "channel.channel.read": authztest.Groups(g1.ID), "channel.channel.update": authztest.Groups(g1.ID),
			}}
			uc := biz.NewChannelUsecase(repo, nil)
			uc.SetAuthorization(policy)
			request := authztest.Context()
			rows, total, err := uc.ListChannels(request, 1, 1, "", "", 0, 0)
			require.NoError(t, err)
			require.EqualValues(t, 2, total)
			require.Len(t, rows, 1)
			require.Empty(t, rows[0].Key)
			_, err = uc.ReadChannel(request, hidden.ID)
			require.Error(t, err)
			row, err := uc.ReadChannel(request, shared.ID)
			require.NoError(t, err)
			require.Empty(t, row.Key)
			current, err := repo.FindByID(ctx, shared.ID)
			require.NoError(t, err)
			current.Name = "unauthorized-write"
			require.Error(t, uc.UpdateChannel(request, current), "a read match does not cover all shared groups")
			q := policy.Scopes["channel.channel.list"]
			q.Deny = authztest.Groups(g2.ID).Allow
			policy.Scopes["channel.channel.list"] = q
			rows, total, err = uc.ListChannels(request, 1, 20, "", "", 0, 0)
			require.NoError(t, err)
			require.EqualValues(t, 1, total)
			require.Equal(t, single.ID, rows[0].ID)
			// Recheck authority against both old and new membership inside write tx.
			current, err = repo.FindByID(ctx, single.ID)
			require.NoError(t, err)
			current.Group = "two"
			require.Error(t, uc.UpdateChannel(request, current))
			stored, err := repo.FindByID(ctx, single.ID)
			require.NoError(t, err)
			require.Equal(t, "one", stored.Group)
			current.Group = "one"
			current.Key = "rotated"
			require.Error(t, uc.UpdateChannel(request, current))
			stored, err = repo.FindByID(ctx, single.ID)
			require.NoError(t, err)
			require.Equal(t, "single-secret", stored.Key)
			// Group ownership and membership authority are independent. Covering
			// both source groups alone still cannot move the membership.
			policy.Scopes["channel.channel.update"] = authztest.Groups(g1.ID, g2.ID)
			candidate, err := repo.FindByID(ctx, single.ID)
			require.NoError(t, err)
			candidate.Group = "two"
			require.Error(t, uc.UpdateChannel(request, candidate))
			policy.Scopes["channel.routing_group.members.update"] = authztest.Resources(g1.ID, g2.ID)
			require.NoError(t, uc.UpdateChannel(request, candidate))
			candidate.Group = "one"
			require.NoError(t, uc.UpdateChannel(request, candidate))
			delete(policy.Scopes, "channel.routing_group.members.update")
			policy.Scopes["channel.channel.update"] = authztest.Groups(g1.ID)
			// Unfinished mapping operations must not disable ordinary updates.
			ordinary, err := repo.FindByID(ctx, single.ID)
			require.NoError(t, err)
			ordinary.Name = "renamed"
			require.NoError(t, uc.UpdateChannel(request, ordinary))
			// General account updates cannot smuggle state, quota, credentials or mappings.
			account := &biz.SubscriptionAccount{Name: "account", Platform: "codex", AccountType: "oauth", Status: biz.ChannelStatusEnabled, Group: "one", AccessToken: "original", QuotaUsedUSD: 1, Metadata: `{"last_error":"keep"}`}
			require.NoError(t, repo.CreateSubscriptionAccount(ctx, account))
			policy.Scopes["channel.account.update"] = authztest.Groups(g1.ID)
			ordinaryAccount, err := repo.FindSubscriptionAccountByID(ctx, account.ID)
			require.NoError(t, err)
			ordinaryAccount.Name = "renamed account"
			require.NoError(t, uc.UpdateSubscriptionAccount(request, ordinaryAccount))
			for _, change := range []func(*biz.SubscriptionAccount){
				func(a *biz.SubscriptionAccount) { a.Status = biz.ChannelStatusDisabled },
				func(a *biz.SubscriptionAccount) { a.QuotaUsedUSD = 0 },
				func(a *biz.SubscriptionAccount) { a.AccessToken = "stolen" },
				func(a *biz.SubscriptionAccount) { a.ModelMapping = `{"a":"b"}` },
				func(a *biz.SubscriptionAccount) { a.RateMultiplier = 2 },
			} {
				current, err := repo.FindSubscriptionAccountByID(ctx, account.ID)
				require.NoError(t, err)
				change(current)
				require.Error(t, uc.UpdateSubscriptionAccount(request, current))
				stored, err := repo.FindSubscriptionAccountByID(ctx, account.ID)
				require.NoError(t, err)
				require.EqualValues(t, biz.ChannelStatusEnabled, stored.Status)
				require.EqualValues(t, 1, stored.QuotaUsedUSD)
				require.Equal(t, "original", stored.AccessToken)
			}

			// A compatibility Models edit can create/delete/update mappings;
			// unfinished mapping authority cannot be acquired via channel.update.
			require.NoError(t, db.Table("models").Create(map[string]any{"model_id": "registered", "display_name": "registered", "provider": "openai", "model_type": "chat", "status": 1, "is_public": true}).Error)
			mapped := &biz.Channel{Name: "mapped", Key: "key", Group: "one", Status: 1, RestrictModels: true, Models: []string{"registered"}}
			require.NoError(t, repo.CreateChannel(ctx, mapped))
			originalMapped, err := repo.FindByID(ctx, mapped.ID)
			require.NoError(t, err)
			originalMapped.Name = "ordinary mapped update"
			require.NoError(t, uc.UpdateChannel(request, originalMapped))
			for _, change := range []func(*biz.Channel){
				func(c *biz.Channel) { c.Models = nil },
				func(c *biz.Channel) { c.Priority = 7 },
				func(c *biz.Channel) { c.Balance = 42 },
				func(c *biz.Channel) { c.Status = biz.ChannelStatusDisabled },
			} {
				candidate, err := repo.FindByID(ctx, mapped.ID)
				require.NoError(t, err)
				change(candidate)
				require.Error(t, uc.UpdateChannel(request, candidate))
			}
			policy.Scopes["channel.channel.create"] = authztest.Groups(g1.ID)
			require.Error(t, uc.CreateChannel(request, &biz.Channel{Name: "implicit mapping", Group: "one", Models: []string{"registered"}, RestrictModels: true}))
			policy.Scopes["channel.account.create"] = authztest.Groups(g1.ID)
			require.Error(t, uc.CreateSubscriptionAccount(request, &biz.SubscriptionAccount{Name: "price bypass", Platform: "codex", Group: "one", RateMultiplier: 2}))
			// Plain resource DTOs cannot expose the unfinished mapping field.
			require.NoError(t, db.Table("channels").Where("id = ?", mapped.ID).Update("model_mapping", `{"source":"target"}`).Error)
			masked, err := uc.ReadChannel(request, mapped.ID)
			require.NoError(t, err)
			require.Empty(t, masked.ModelMapping)
			rawMapping, err := repo.FindByID(ctx, mapped.ID)
			require.NoError(t, err)
			require.NotEmpty(t, rawMapping.ModelMapping)
			policy.Scopes["channel.account.read"] = authztest.Groups(g1.ID)
			require.NoError(t, db.Table("subscription_accounts").Where("id = ?", account.ID).Update("model_mapping", `{"source":"target"}`).Error)
			maskedAccount, err := uc.GetSubscriptionAccount(request, account.ID)
			require.NoError(t, err)
			require.Empty(t, maskedAccount.ModelMapping)
			// Reset and error-clear re-read ownership at the write edge.
			require.NoError(t, db.Table("subscription_accounts").Where("id = ?", account.ID).Updates(map[string]any{"group": "two"}).Error)
			require.NoError(t, db.Table("account_routing_groups").Where("subscription_account_id = ?", account.ID).Delete(&accountRoutingGroupModel{}).Error)
			require.NoError(t, db.Create(&accountRoutingGroupModel{SubscriptionAccountID: account.ID, RoutingGroupID: g2.ID}).Error)
			staleQuota := authorization.WithQueryScope(ctx, "channel.account.quota.reset", authztest.Groups(g1.ID))
			require.ErrorIs(t, repo.ResetSubscriptionAccountQuota(staleQuota, account.ID, "all"), authorization.ErrDenied)
			staleRecovery := authorization.WithQueryScope(ctx, "channel.account.recovery.clear", authztest.Groups(g1.ID))
			require.ErrorIs(t, repo.SetSubscriptionAccountError(staleRecovery, account.ID, ""), authorization.ErrDenied)
			stillStored, err := repo.FindSubscriptionAccountByID(ctx, account.ID)
			require.NoError(t, err)
			require.EqualValues(t, 1, stillStored.QuotaUsedUSD)
			require.Contains(t, stillStored.Metadata, "keep")
			// A decision obtained before an ownership change cannot write afterward.
			stale := authorization.WithQueryScope(ctx, "channel.channel.update", authztest.Groups(g1.ID))
			require.NoError(t, db.Table("channels").Where("id = ?", single.ID).Updates(map[string]any{"group": "two"}).Error)
			require.NoError(t, db.Table("channel_routing_groups").Where("channel_id = ?", single.ID).Delete(&channelRoutingGroupModel{}).Error)
			require.NoError(t, db.Create(&channelRoutingGroupModel{ChannelID: single.ID, RoutingGroupID: g2.ID}).Error)
			require.Error(t, repo.UpdateChannel(stale, current))
		})
	}
}

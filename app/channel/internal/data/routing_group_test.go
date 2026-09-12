package data

import (
	"context"
	"testing"

	"micro-one-api/app/channel/internal/biz"

	"github.com/stretchr/testify/require"
)

func TestRoutingGroupDuplicatesDoNotDuplicateAbilities(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			repo := newMemoryRepository()
			if backend == "sqlite" {
				repo = setupChannelTestDB(t)
			}
			ctx := context.Background()
			channel := &biz.Channel{Name: "multi-group", Key: "test-key", Status: biz.ChannelStatusEnabled,
				Group: "default, vip,default, ,vip", Models: []string{"custom-model"}}
			require.NoError(t, repo.CreateChannel(ctx, channel))
			account := &biz.SubscriptionAccount{Name: "multi-group", Platform: "codex", Status: biz.ChannelStatusEnabled,
				Group: channel.Group, Models: []string{"custom-subscription-model"}}
			require.NoError(t, repo.CreateSubscriptionAccount(ctx, account))
			for _, group := range []string{"default", "vip"} {
				abilities, err := repo.ListAbilitiesByGroupAndModel(ctx, group, "custom-model")
				require.NoError(t, err)
				require.Len(t, abilities, 1, "duplicate memberships must not bias channel selection")
				subAbilities, err := repo.ListSubscriptionAccountAbilities(ctx, group, "custom-subscription-model", "codex")
				require.NoError(t, err)
				require.Len(t, subAbilities, 1, "duplicate memberships must not bias account selection")
			}
			// A shared suffix is not membership in a routing group.
			abilities, _ := repo.ListAbilitiesByGroupAndModel(ctx, "svip", "custom-model")
			require.Empty(t, abilities)
			subAbilities, _ := repo.ListSubscriptionAccountAbilities(ctx, "svip", "custom-subscription-model", "codex")
			require.Empty(t, subAbilities)
		})
	}
}

package biz

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStickySubscriptionAccountRoutingMembership(t *testing.T) {
	for _, tt := range []struct {
		membership string
		allowed    bool
	}{
		{"vip, default ,default", true},
		{"vip,svip", false},
		{"Default,vip", false},
		{"", false},
	} {
		t.Run(tt.membership, func(t *testing.T) {
			account := &SubscriptionAccount{ID: 9, Platform: "codex", Status: 1, Group: tt.membership, Models: []string{"gpt-5"}}
			client := &recordingChannelClient{byID: map[int64]*SubscriptionAccount{9: account}}
			uc := NewRelayUsecase(&testIdentityClientAllowAll{}, client, nil, nil)
			require.Equal(t, tt.allowed, uc.stickySubscriptionAccountValid(context.Background(), account, "default", "gpt-5", "gpt-5"))
			_, _, err := uc.ResolveSubscriptionRoutingSource(context.Background(), 9, "default", "gpt-5", "gpt-5")
			if tt.allowed {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestPlanReusesMultiGroupSubscriptionAccount(t *testing.T) {
	account := &SubscriptionAccount{ID: 9, Platform: "codex", Status: 1, Group: "vip,default", Models: []string{"gpt-5"}}
	client := &recordingChannelClient{byID: map[int64]*SubscriptionAccount{9: account}}
	uc := NewRelayUsecase(&testIdentityClientAllowAll{}, client, nil, nil)
	store := &fakeSessionStore{bound: map[string]int64{sessKey("default", "multi-group"): 9}}
	uc.SetSessionAccountStore(store, time.Hour, true)
	plan, err := uc.Plan(context.Background(), RelayRequest{Token: "demo", Model: "gpt-5", SessionHash: "multi-group"})
	require.NoError(t, err)
	require.NotNil(t, plan.Account)
	require.Equal(t, account.ID, plan.Account.ID)
	require.Empty(t, client.subscriptionModels, "a valid multi-group binding must avoid reselection")
	require.Equal(t, 1, store.refreshed)
}

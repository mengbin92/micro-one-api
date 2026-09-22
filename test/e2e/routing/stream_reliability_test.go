package routingtest

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	channelv1 "micro-one-api/api/channel/v1"
)

// This phase uses real gateway/channel/billing/identity/log binaries and storage.
// Only the two upstream protocols are deterministic local fixtures.
func (s *suite) streamReliability() {
	for _, first := range []string{"channel", "subscription"} {
		for _, fault := range []string{"500", "refused", "partial"} {
			name := fmt.Sprintf("r-batch-%s-%s-%s", os.Getenv("RELIABILITY_EXECUTOR"), first, fault)
			s.t.Run(name, func(t *testing.T) {
				previous := s.t
				s.t = t
				defer func() { s.t = previous }()
				ctx, conn := s.conn("CHANNEL_GRPC_ENDPOINT")
				client := channelv1.NewChannelServiceClient(conn)
				model, err := client.CreateModel(ctx, &channelv1.CreateModelRequest{ModelId: name, DisplayName: name, Provider: "openai", ModelType: "chat", IsPublic: true, PricingInput: 1, PricingOutput: 1})
				require.NoError(t, err)
				require.True(t, model.Success)
				channelPriority, accountPriority := int64(20), int64(10)
				channelModel, accountModel := "channel-"+name, "account-"+name
				channelURL, accountURL := "http://mock-upstream:9999/v1", "http://mock-upstream:9999/v1"
				if first == "subscription" {
					accountPriority = 30
				}
				if fault == "refused" {
					if first == "channel" {
						channelURL = "http://mock-upstream:1/v1"
					} else {
						accountURL = "http://mock-upstream:1/v1"
					}
				} else {
					prefix := "fault-"
					if fault == "partial" {
						prefix = "partial-"
					}
					if first == "channel" {
						channelModel = prefix + channelModel
					} else {
						accountModel = prefix + accountModel
					}
				}
				channel, err := client.CreateChannel(ctx, &channelv1.CreateChannelRequest{Name: name, Type: 1, BaseUrl: channelURL, Key: "sk-local-fixture", Models: name, Group: "default", Priority: channelPriority, Weight: 1})
				require.NoError(t, err)
				require.True(t, channel.Success)
				account, err := client.CreateSubscriptionAccount(ctx, &channelv1.CreateSubscriptionAccountRequest{Name: name, Platform: "kimi", AccountType: "static_key", BaseUrl: accountURL, AccessToken: "sk-local-fixture", Models: name, Group: "default", Priority: accountPriority, Weight: 1})
				require.NoError(t, err)
				require.True(t, account.Success)
				// Source creation publishes asynchronous model projections. Wait
				// for fixture setup before editing its canonical mappings.
				require.Eventually(t, func() bool {
					return s.scalar("SELECT COUNT(*) FROM model_subscription_mapping WHERE subscription_account_id=? AND model_id=?", account.AccountId, model.ModelPk) == 1 &&
						s.scalar("SELECT COUNT(*) FROM model_channel_mapping WHERE channel_id=? AND model_id=?", channel.ChannelId, model.ModelPk) == 1
				}, 5*time.Second, 20*time.Millisecond)
				cm, err := client.UpsertChannelModelMapping(ctx, &channelv1.UpsertChannelModelMappingRequest{ChannelId: channel.ChannelId, ModelPk: model.ModelPk, Priority: int32(channelPriority), UpstreamModelId: channelModel})
				require.NoError(t, err)
				require.True(t, cm.Success, cm.Message)
				am, err := client.UpsertSubscriptionModelMapping(ctx, &channelv1.UpsertSubscriptionModelMappingRequest{SubscriptionAccountId: account.AccountId, ModelPk: model.ModelPk, GroupName: "default", Priority: int32(accountPriority), UpstreamModelId: accountModel})
				require.NoError(t, err)
				require.True(t, am.Success, am.Message)
				before := s.scalar("SELECT COALESCE(MAX(id),0) FROM billing_reservations")
				calls := s.calls()
				status, body, err := send("POST", s.relay+"/v1/chat/completions", s.state.Reliability, object{"model": name, "messages": []any{object{"role": "user", "content": "isolated stream"}}, "stream": true, "max_tokens": 32}, name)
				require.NoError(t, err)
				require.Equal(t, 200, status, "relay: %s", body)
				wantCommits := int64(1)
				if fault == "partial" {
					wantCommits = 0
					require.NotContains(t, string(body), `"finish_reason":"stop"`)
				} else {
					require.Contains(t, string(body), "[DONE]")
				}
				require.Eventually(t, func() bool {
					return s.scalar("SELECT COUNT(*) FROM billing_reservations WHERE id > ? AND status = 'reserved'", before) == 0
				}, 15*time.Second, 100*time.Millisecond)
				require.Equal(t, wantCommits, s.scalar("SELECT COUNT(*) FROM billing_reservations WHERE id > ? AND status='committed'", before))
				require.Positive(t, s.scalar("SELECT COUNT(*) FROM billing_reservations WHERE id > ? AND status='released'", before))
				require.Equal(t, wantCommits, s.scalar("SELECT COUNT(*) FROM billing_ledgers l JOIN billing_reservations r ON r.reservation_id=l.reference_id WHERE r.id > ? AND l.type='consume'", before))
				if fault == "partial" {
					require.EqualValues(t, 1, s.calls()-calls, "output must prevent another upstream attempt")
					require.EqualValues(t, 1, s.scalar("SELECT COUNT(*) FROM billing_reservations WHERE id > ?", before))
				} else {
					var source, upstream, root string
					require.NoError(t, s.db.QueryRow("SELECT source_kind,upstream_model_id,root_request_id FROM billing_reservations WHERE id > ? AND status='committed'", before).Scan(&source, &upstream, &root))
					wantSource, wantModel := "subscription", accountModel
					if first == "subscription" {
						wantSource, wantModel = "channel", channelModel
					}
					require.Equal(t, wantSource, source)
					require.Equal(t, wantModel, upstream)
					require.NotEmpty(t, root)
					require.EqualValues(t, 1, s.scalar("SELECT COUNT(DISTINCT root_request_id) FROM billing_reservations WHERE id > ?", before))
					require.Eventually(t, func() bool {
						return s.scalar("SELECT COUNT(*) FROM logs WHERE root_request_id=? AND level='consume'", root) == 1
					}, 15*time.Second, 100*time.Millisecond)
					require.Eventually(t, func() bool {
						return s.scalar("SELECT COUNT(*) FROM logs WHERE root_request_id=? AND source='routing-selection'", root) == 2
					}, 5*time.Second, 50*time.Millisecond)
					require.False(t, strings.HasPrefix(upstream, "fault-"))
				}
				t.Logf("executor=%s first=%s fault=%s status=%d commits=%d releases=%d upstream_calls=%d", os.Getenv("RELIABILITY_EXECUTOR"), first, fault, status, wantCommits, s.scalar("SELECT COUNT(*) FROM billing_reservations WHERE id > ? AND status='released'", before), s.calls()-calls)
			})
		}
	}
}

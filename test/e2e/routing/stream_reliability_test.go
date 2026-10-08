package routingtest

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/pkg/jsonx"
)

// This phase uses real gateway/channel/billing/identity/log binaries and storage.
// Only the two upstream protocols are deterministic local fixtures.
func (s *suite) streamReliability() {
	// Upstream subscription accounts are still exercised; user billing stays
	// wallet-only so a finite fixture budget cannot split consume records.
	if s.scalar("SELECT COUNT(*) FROM user_subscriptions WHERE id=? AND status='active'", s.state.LegacySubscription) == 1 {
		s.revoke(s.state.LegacySubscription)
	}
	faults := []string{"500", "refused", "partial", "cancel", "paused-cancel"}
	if os.Getenv("RELIABILITY_BUDGET") == "1" {
		faults = []string{"budget"}
	}
	for _, first := range []string{"channel", "subscription"} {
		for _, fault := range faults {
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
					if fault == "cancel" || fault == "paused-cancel" || fault == "budget" {
						prefix = "long-1024-"
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
				payload := object{"model": name, "messages": []any{object{"role": "user", "content": "isolated stream"}}, "stream": true, "max_tokens": 32}
				interrupted := fault == "partial" || fault == "cancel" || fault == "paused-cancel" || fault == "budget"
				var status int
				var body []byte
				if fault == "cancel" || fault == "paused-cancel" || fault == "budget" {
					raw, err := jsonx.Marshal(payload)
					require.NoError(t, err)
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					r, err := http.NewRequestWithContext(ctx, "POST", s.relay+"/v1/chat/completions", bytes.NewReader(raw))
					require.NoError(t, err)
					r.Header.Set("Authorization", "Bearer "+s.state.Reliability)
					r.Header.Set("Content-Type", "application/json")
					response, err := http.DefaultClient.Do(r)
					require.NoError(t, err)
					status = response.StatusCode
					reader := bufio.NewReader(response.Body)
					for {
						line, err := reader.ReadString('\n')
						require.NoError(t, err, "fault must occur after output starts")
						body = append(body, line...)
						if strings.HasPrefix(line, "data:") {
							break
						}
					}
					if fault == "budget" {
						rest, err := io.ReadAll(reader)
						require.NoError(t, err)
						body = append(body, rest...)
					} else {
						if fault == "paused-cancel" {
							time.Sleep(100 * time.Millisecond)
						}
						cancel()
					}
					require.NoError(t, response.Body.Close())
				} else {
					status, body, err = send("POST", s.relay+"/v1/chat/completions", s.state.Reliability, payload, name)
					require.NoError(t, err)
				}
				require.Equal(t, 200, status, "relay: %s", body)
				wantCommits := int64(1)
				if interrupted {
					wantCommits = 0
					require.NotContains(t, string(body), `"finish_reason":"stop"`)
					if strings.Contains(string(body), "[DONE]") {
						require.Contains(t, string(body), `"finish_reason":"error"`, "converted streams may terminate an explicit error frame")
					}
				} else {
					require.Contains(t, string(body), "[DONE]")
				}
				require.Eventually(t, func() bool {
					return s.scalar("SELECT COUNT(*) FROM billing_reservations WHERE id > ? AND status = 'reserved'", before) == 0
				}, 15*time.Second, 100*time.Millisecond)
				require.Equal(t, wantCommits, s.scalar("SELECT COUNT(*) FROM billing_reservations WHERE id > ? AND status='committed'", before))
				require.Positive(t, s.scalar("SELECT COUNT(*) FROM billing_reservations WHERE id > ? AND status='released'", before))
				require.Equal(t, wantCommits, s.scalar("SELECT COUNT(*) FROM billing_ledgers l JOIN billing_reservations r ON r.reservation_id=l.reference_id WHERE r.id > ? AND l.type='consume'", before))
				upstreamCalls := s.calls() - calls
				if interrupted {
					require.EqualValues(t, 1, s.calls()-calls, "output must prevent another upstream attempt")
					require.EqualValues(t, 1, s.scalar("SELECT COUNT(*) FROM billing_reservations WHERE id > ?", before))
					// A completed request after the fault proves storage and
					// source selection recover without replaying the canceled request.
					if fault != "partial" {
						recoveryBefore := s.scalar("SELECT COALESCE(MAX(id),0) FROM billing_reservations")
						payload["stream"] = false
						status, recovered, err := send("POST", s.relay+"/v1/chat/completions", s.state.Reliability, payload, name+"-recovery")
						require.NoError(t, err)
						require.Equal(t, 200, status, "%s", recovered)
						require.Eventually(t, func() bool {
							return s.scalar("SELECT COUNT(*) FROM billing_reservations WHERE id>? AND status='committed'", recoveryBefore) == 1
						}, 5*time.Second, 20*time.Millisecond)
						require.EqualValues(t, 1, s.scalar("SELECT COUNT(*) FROM billing_ledgers l JOIN billing_reservations r ON l.reference_id=r.reservation_id WHERE r.id>? AND l.type='consume'", recoveryBefore))
					}
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
				t.Logf("executor=%s first=%s fault=%s status=%d commits=%d releases=%d upstream_calls=%d", os.Getenv("RELIABILITY_EXECUTOR"), first, fault, status, wantCommits, s.scalar("SELECT COUNT(*) FROM billing_reservations WHERE id > ? AND status='released'", before), upstreamCalls)
			})
		}
	}
}

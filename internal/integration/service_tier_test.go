package integration

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	billingv1 "micro-one-api/api/billing/v1"
	logv1 "micro-one-api/api/log/v1"
	billingtest "micro-one-api/app/billing/testutil"
	logtest "micro-one-api/app/log/testutil"
	billingdomain "micro-one-api/domain/billing"
	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	relaydata "micro-one-api/internal/data"
	relayserver "micro-one-api/internal/server"
	"micro-one-api/pkg/jsonx"
	dbtest "micro-one-api/platform/database/testutil"
)

func tierTestGRPC(t *testing.T, register func(*grpc.Server)) *grpc.ClientConn {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		response, callErr := next(ctx, req)
		if callErr != nil {
			t.Logf("%s failed: %v", info.FullMethod, callErr)
		}
		switch result := response.(type) {
		case *billingv1.ReserveQuotaResponse:
			if result != nil && !result.Success {
				t.Logf("reserve rejected: %s", result.ErrorMessage)
			}
		case *billingv1.CommitQuotaResponse:
			if result != nil && !result.Success {
				t.Logf("commit rejected: %s", result.ErrorMessage)
			}
		}
		return response, callErr
	}))
	register(srv)
	go func() { _ = srv.Serve(lis) }()
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(); srv.Stop(); _ = lis.Close() })
	return conn
}

func tierTestAddress(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := lis.Addr().String()
	require.NoError(t, lis.Close())
	return addr
}

// TestServiceTierOwnerChain crosses real HTTP and generated gRPC contracts,
// production Billing/Log owners and the complete migration tree. The upstream
// is deterministic and never spends money or reads deployment credentials.
func TestServiceTierOwnerChain(t *testing.T) {
	runServiceTierOwnerChain(t, false)
}

func TestLongContextTimePricingOwnerChain(t *testing.T) {
	runServiceTierOwnerChain(t, true)
}

func runServiceTierOwnerChain(t *testing.T, longContextTime bool) {
	t.Helper()
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			t.Setenv("BILLING_REQUEST_SNAPSHOT_V2", "true")
			t.Setenv("RELAY_CANONICAL_USAGE_PRODUCER", "true")
			t.Setenv("RELAY_EXECUTOR_ENABLED", "false")
			t.Setenv("BILLING_EXECUTION_DELIVERY_V1", "false")
			db := dbtest.RoutingContextDB(t, dialect)
			require.NoError(t, db.Table("users").Create(map[string]any{"id": 1, "username": "tier-chain", "group": "default", "status": 1, "balance": 100000000}).Error)
			price := func(in, out, cached float64) billingdomain.ModelPrice {
				zero := 0.0
				return billingdomain.ModelPrice{InputPrice: in, OutputPrice: out, CacheReadPrice: &cached, CacheCreation5mPrice: &zero, CacheCreation1hPrice: &zero}
			}
			config := billingdomain.ServiceTierConfig{
				Version: 1, Enabled: true, SourceKind: "channel", SourceID: 1, Model: "gpt-4o-mini",
				Prices: map[string]billingdomain.ModelPrice{
					"default": price(.001, .002, .0002), "priority": price(.003, .006, .0005), "flex": price(.0005, .001, .0001),
				},
				UpstreamPrices: map[string]billingdomain.ModelPrice{
					"default": price(.0005, .001, .0001), "priority": price(.0015, .003, .00025), "flex": price(.00025, .0005, .00005),
				},
			}
			if longContextTime {
				config.Version, config.SourceID, config.Model = 3, 11, "deepseek-flash"
				config.LongContext = &billingdomain.LongContextConfig{ThresholdTokens: 9, Policy: billingdomain.LongContextPolicy, Prices: scaleTierPrices(config.Prices, 2), UpstreamPrices: scaleTierPrices(config.UpstreamPrices, 2)}
				config.TimePricing = &billingdomain.TimePricingConfig{
					Timezone: "Asia/Shanghai", CalendarValidThrough: "2099-12-31", Holidays: []string{},
					PeakWindows: []billingdomain.PricingTimeWindow{{Weekdays: []int{1, 2, 3, 4, 5, 6, 7}, StartMinute: 0, EndMinute: 1440}},
					PeakPrices:  scaleTierPrices(config.Prices, 2), PeakUpstreamPrices: scaleTierPrices(config.UpstreamPrices, 2),
					PeakLongContextPrices: scaleTierPrices(config.Prices, 4), PeakLongContextUpstreamPrices: scaleTierPrices(config.UpstreamPrices, 4),
				}
			}
			storeConfig := func(c billingdomain.ServiceTierConfig) {
				b, err := jsonx.Marshal(c)
				require.NoError(t, err)
				_, err = billingdomain.ParseServiceTierConfig(string(b))
				require.NoError(t, err)
				require.NoError(t, db.Exec("DELETE FROM system_options WHERE option_key = ?", "ServiceTierPricing").Error)
				require.NoError(t, db.Table("system_options").Create(map[string]any{"option_key": "ServiceTierPricing", "option_value": string(b)}).Error)
			}
			storeConfig(config)
			billingConn := tierTestGRPC(t, func(s *grpc.Server) { billingv1.RegisterBillingServiceServer(s, billingtest.NewServiceTierBilling(db)) })
			billingClient := billingv1.NewBillingServiceClient(billingConn)
			logConn := tierTestGRPC(t, func(s *grpc.Server) { logv1.RegisterLogServiceServer(s, logtest.NewServiceTierLog(db)) })
			logClient := logv1.NewLogServiceClient(logConn)

			type scenario struct {
				actual                         string
				stream, responses, mutate      bool
				zeroInput, zeroOutput, partial bool
				cached                         bool
			}
			var active atomic.Pointer[scenario]
			var calls atomic.Int64
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				sc := active.Load()
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				var request map[string]any
				if err := jsonx.Unmarshal(body, &request); err != nil {
					t.Error(err)
					return
				}
				if request["service_tier"] != "priority" {
					t.Errorf("upstream tier changed: %v", request["service_tier"])
				}
				if sc.mutate {
					// Replace every live tariff while the admitted request keeps its
					// reserve-time context, clock decision and procurement prices.
					changed := scaleTierConfig(config, 1000)
					storeConfig(changed)
				}
				if sc.responses {
					input, output := int64(10), int64(5)
					if sc.zeroInput {
						input = 0
					}
					if sc.zeroOutput {
						output = 0
					}
					usage := map[string]any{"input_tokens": input, "output_tokens": output, "total_tokens": input + output}
					if sc.partial {
						delete(usage, "output_tokens")
					}
					response := map[string]any{"id": "resp-tier", "object": "response", "status": "completed", "model": config.Model, "output": []any{}, "usage": usage}
					if sc.actual != "" {
						response["service_tier"] = sc.actual
					}
					if sc.stream {
						w.Header().Set("Content-Type", "text/event-stream")
						b, _ := jsonx.Marshal(map[string]any{"type": "response.completed", "response": response})
						_, _ = fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", b)
					} else {
						w.Header().Set("Content-Type", "application/json")
						_ = jsonx.NewEncoder(w).Encode(response)
					}
					return
				}
				chatUsage := map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
				if longContextTime {
					cached := 0
					if sc.cached {
						cached = 4
					}
					chatUsage["prompt_cache_hit_tokens"], chatUsage["prompt_cache_miss_tokens"] = cached, 10-cached
					chatUsage["prompt_tokens_details"] = map[string]any{"cached_tokens": cached}
				}
				if sc.stream {
					w.Header().Set("Content-Type", "text/event-stream")
					chunk := map[string]any{"id": "chat-tier", "object": "chat.completion.chunk", "model": config.Model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": "hello"}, "finish_reason": "stop"}}}
					if longContextTime {
						chunk["usage"] = chatUsage
					}
					if sc.actual != "" {
						chunk["service_tier"] = sc.actual
					}
					b, _ := jsonx.Marshal(chunk)
					_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
					if !longContextTime {
						_, _ = io.WriteString(w, "data: {\"id\":\"chat-tier\",\"object\":\"chat.completion.chunk\",\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5,\"total_tokens\":15}}\n\n")
					}
					_, _ = io.WriteString(w, "data: [DONE]\n\n")
					return
				}
				response := map[string]any{"id": "chat-tier", "object": "chat.completion", "model": config.Model, "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "hello"}, "finish_reason": "stop"}}, "usage": chatUsage}
				if sc.actual != "" {
					response["service_tier"] = sc.actual
				}
				w.Header().Set("Content-Type", "application/json")
				_ = jsonx.NewEncoder(w).Encode(response)
			}))
			t.Cleanup(upstream.Close)
			identityCleanup, identityClient := setupIdentityAllowAll(t, tierTestAddress(t))
			t.Cleanup(identityCleanup)
			channelType := relayprovider.ChannelTypeOpenAI
			if longContextTime {
				channelType = relayprovider.ChannelTypeDeepSeek
			}
			channelCleanup, channelClient := setupChannelWithUpstreamModel(t, tierTestAddress(t), upstream.URL, config.SourceID, channelType, []string{config.Model})
			t.Cleanup(channelCleanup)
			uc := relaybiz.NewRelayUsecase(relaydata.NewIdentityAdapter(identityClient), relaydata.NewChannelAdapter(channelClient), nil, nil)
			relay := relayserver.NewHTTPServer(identityClient, channelClient, billingClient, relayprovider.NewProviderFactory(5*time.Second), uc, logClient)
			baseURL := startRelayHTTPServer(t, relay)
			var expectedCalls int64
			for _, tc := range []struct {
				name, actual                   string
				stream, responses, mutate      bool
				zeroInput, zeroOutput, partial bool
				cached                         bool
				cost                           int64
			}{
				{name: "chat_priority", actual: "priority", cost: 600},
				{name: "chat_fallback_flex", actual: "flex", cost: 100},
				{name: "chat_missing", cost: 600},
				{name: "chat_future", actual: "future", cost: 600},
				{name: "chat_stream", actual: "flex", stream: true, cost: 100},
				{name: "responses_json", actual: "default", responses: true, cost: 200},
				{name: "responses_stream", actual: "flex", stream: true, responses: true, cost: 100},
				{name: "responses_stream_zero_output", actual: "flex", stream: true, responses: true, zeroOutput: true, cost: 50},
				{name: "responses_stream_zero_input", actual: "flex", stream: true, responses: true, zeroInput: true, cost: 50},
				{name: "responses_stream_zero_usage", actual: "flex", stream: true, responses: true, zeroInput: true, zeroOutput: true, cost: 1},
				{name: "responses_json_zero_usage", actual: "flex", responses: true, zeroInput: true, zeroOutput: true, cost: 1},
				{name: "responses_json_partial_usage", actual: "flex", responses: true, partial: true},
				{name: "responses_stream_partial_usage", actual: "flex", stream: true, responses: true, partial: true},
				{name: "inflight_price_change", actual: "flex", mutate: true, cost: 100},
				{name: "chat_cached_json", actual: "flex", cached: true, cost: 84},
				{name: "chat_cached_stream", actual: "flex", cached: true, stream: true, cost: 84},
			} {
				if tc.cached && !longContextTime {
					continue
				}
				t.Run(tc.name, func(t *testing.T) {
					if longContextTime && tc.cost > 1 {
						if tc.zeroInput {
							tc.cost *= 2
						} else {
							tc.cost *= 4
						}
					}
					expectedCalls++
					storeConfig(config)
					active.Store(&scenario{actual: tc.actual, stream: tc.stream, responses: tc.responses, mutate: tc.mutate, zeroInput: tc.zeroInput, zeroOutput: tc.zeroOutput, partial: tc.partial, cached: tc.cached})
					var previousID int64
					require.NoError(t, db.Table("billing_ledgers").Select("COALESCE(MAX(id),0)").Scan(&previousID).Error)
					var previousReservationID int64
					require.NoError(t, db.Table("billing_reservations").Select("COALESCE(MAX(id),0)").Scan(&previousReservationID).Error)
					payload := map[string]any{"model": config.Model, "service_tier": "priority", "stream": tc.stream}
					path := "/v1/chat/completions"
					if tc.responses {
						path = "/v1/responses"
						payload["input"] = "hello"
						payload["max_output_tokens"] = 20
					} else {
						payload["messages"] = []any{map[string]any{"role": "user", "content": "hello"}}
						if longContextTime {
							payload["max_tokens"] = 20
						} else {
							payload["max_completion_tokens"] = 20
						}
					}
					body, err := jsonx.Marshal(payload)
					require.NoError(t, err)
					req, err := http.NewRequest(http.MethodPost, baseURL+path, strings.NewReader(string(body)))
					require.NoError(t, err)
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("Authorization", "Bearer test-token")
					resp, err := http.DefaultClient.Do(req)
					require.NoError(t, err)
					result, err := io.ReadAll(resp.Body)
					require.NoError(t, resp.Body.Close())
					require.NoError(t, err)
					require.Equal(t, 200, resp.StatusCode, string(result))
					if tc.partial {
						var reservation struct{ ReservationID, Status string }
						require.Eventually(t, func() bool {
							return db.Table("billing_reservations").Where("id > ?", previousReservationID).First(&reservation).Error == nil && reservation.Status == "released"
						}, 3*time.Second, 10*time.Millisecond)
						var consumed int64
						require.NoError(t, db.Table("billing_ledgers").Where("id > ? AND type = ?", previousID, "consume").Count(&consumed).Error)
						require.Zero(t, consumed, "partial usage cannot prove output tokens")
						var outcome struct {
							ActualServiceTier, Message string
							Quota                      int64
						}
						require.Eventually(t, func() bool {
							return db.Table("logs").Where("reservation_id = ? AND source = ?", reservation.ReservationID, "relay-gateway").First(&outcome).Error == nil
						}, 3*time.Second, 10*time.Millisecond)
						require.Equal(t, "flex", outcome.ActualServiceTier)
						require.Zero(t, outcome.Quota)
						require.Contains(t, outcome.Message, "settlement_outcome=released/")
						return
					}
					var ledger struct {
						ID                                                                                                                                         int64
						ReferenceID                                                                                                                                string
						Amount, UpstreamCost                                                                                                                       int64
						RequestedServiceTier, ActualServiceTier, ServiceTierStatus, BilledServiceTier, ServiceTierPolicy, ServiceTierConfigHash, PricingConfigHash string
						LongContextPolicy, LongContextTier, PricingTimeBand, PricingTimezone                                                                       string
						LongContextThreshold, LongContextInputTokens, PricingTimeUnix, UncachedInputTokens, CacheReadTokens                                        int64
					}
					require.Eventually(t, func() bool {
						return db.Table("billing_ledgers").Where("id > ? AND type = ?", previousID, "consume").First(&ledger).Error == nil
					}, 3*time.Second, 10*time.Millisecond)
					require.Equal(t, -tc.cost, ledger.Amount)
					wantSupplier := int64(0)
					if billingdomain.KnownActualServiceTier(tc.actual) {
						wantSupplier = tc.cost / 2
					}
					require.Equal(t, wantSupplier, ledger.UpstreamCost, "supplier estimate requires actual tier evidence and the frozen tariff")
					require.Equal(t, "priority", ledger.RequestedServiceTier)
					require.Equal(t, tc.actual, ledger.ActualServiceTier)
					require.NotEmpty(t, ledger.ServiceTierPolicy)
					require.NotEmpty(t, ledger.ServiceTierConfigHash)
					require.NotEmpty(t, ledger.PricingConfigHash)
					if longContextTime {
						input, contextTier := int64(10), "long"
						if tc.zeroInput {
							input, contextTier = 0, "base"
						}
						require.Equal(t, billingdomain.LongContextPolicy, ledger.LongContextPolicy)
						require.EqualValues(t, 9, ledger.LongContextThreshold)
						require.Equal(t, input, ledger.LongContextInputTokens)
						require.Equal(t, contextTier, ledger.LongContextTier)
						require.Equal(t, "peak", ledger.PricingTimeBand)
						require.Equal(t, "Asia/Shanghai", ledger.PricingTimezone)
						require.Positive(t, ledger.PricingTimeUnix)
						if tc.cached {
							require.EqualValues(t, 6, ledger.UncachedInputTokens)
							require.EqualValues(t, 4, ledger.CacheReadTokens)
						}
						var snapshot struct {
							SnapshotVersion                                                      int32
							LongContextPolicy, LongContextTier, PricingTimeBand, PricingTimezone string
							LongContextThreshold                                                 int64
						}
						require.NoError(t, db.Table("billing_pricing_snapshots").Where("config_hash = ?", ledger.PricingConfigHash).First(&snapshot).Error)
						require.EqualValues(t, 4, snapshot.SnapshotVersion)
						require.Equal(t, ledger.LongContextPolicy, snapshot.LongContextPolicy)
						require.Equal(t, ledger.LongContextThreshold, snapshot.LongContextThreshold)
						require.Equal(t, ledger.LongContextTier, snapshot.LongContextTier)
						require.Equal(t, ledger.PricingTimeBand, snapshot.PricingTimeBand)
						require.Equal(t, ledger.PricingTimezone, snapshot.PricingTimezone)
					}
					var reserved struct {
						Amount     int64
						ActualCost *int64
						Status     string
					}
					require.NoError(t, db.Table("billing_reservations").Where("reservation_id = ?", ledger.ReferenceID).First(&reserved).Error)
					require.GreaterOrEqual(t, reserved.Amount, tc.cost)
					require.Equal(t, "committed", reserved.Status)
					// Replaying with a different reported tier cannot rewrite money or evidence.
					replay, err := billingClient.CommitQuota(context.Background(), &billingv1.CommitQuotaRequest{ReservationId: ledger.ReferenceID, Success: true, ActualTokens: 999, ActualServiceTier: "priority"})
					require.NoError(t, err)
					require.True(t, replay.Success)
					require.Equal(t, tc.cost, replay.CommittedAmount)
					var count int64
					require.NoError(t, db.Table("billing_ledgers").Where("reference_id = ? AND type = ?", ledger.ReferenceID, "consume").Count(&count).Error)
					require.EqualValues(t, 1, count)
					var logRow struct{ RequestedServiceTier, ActualServiceTier, ServiceTierStatus string }
					require.Eventually(t, func() bool {
						return db.Table("logs").Where("reservation_id = ? AND source = ?", ledger.ReferenceID, "relay-gateway").First(&logRow).Error == nil
					}, 3*time.Second, 10*time.Millisecond)
					require.Equal(t, "priority", logRow.RequestedServiceTier)
					require.Equal(t, tc.actual, logRow.ActualServiceTier)
					require.NotEmpty(t, logRow.ServiceTierStatus)
				})
			}
			var frozen int64
			require.NoError(t, db.Table("users").Where("id = ?", 1).Select("frozen_amount").Scan(&frozen).Error)
			require.Zero(t, frozen)
			require.EqualValues(t, expectedCalls, calls.Load())
		})
	}
}

func scaleTierPrices(prices map[string]billingdomain.ModelPrice, multiplier float64) map[string]billingdomain.ModelPrice {
	if prices == nil {
		return nil
	}
	result := make(map[string]billingdomain.ModelPrice, len(prices))
	scale := func(value *float64) *float64 {
		if value == nil {
			return nil
		}
		v := *value * multiplier
		return &v
	}
	for tier, value := range prices {
		result[tier] = billingdomain.ModelPrice{InputPrice: value.InputPrice * multiplier, OutputPrice: value.OutputPrice * multiplier, CacheReadPrice: scale(value.CacheReadPrice), CacheCreation5mPrice: scale(value.CacheCreation5mPrice), CacheCreation1hPrice: scale(value.CacheCreation1hPrice)}
	}
	return result
}

func scaleTierConfig(config billingdomain.ServiceTierConfig, multiplier float64) billingdomain.ServiceTierConfig {
	config.Prices, config.UpstreamPrices = scaleTierPrices(config.Prices, multiplier), scaleTierPrices(config.UpstreamPrices, multiplier)
	if config.LongContext != nil {
		longContext := *config.LongContext
		longContext.Prices, longContext.UpstreamPrices = scaleTierPrices(longContext.Prices, multiplier), scaleTierPrices(longContext.UpstreamPrices, multiplier)
		config.LongContext = &longContext
	}
	if config.TimePricing != nil {
		timePricing := *config.TimePricing
		timePricing.PeakPrices, timePricing.PeakUpstreamPrices = scaleTierPrices(timePricing.PeakPrices, multiplier), scaleTierPrices(timePricing.PeakUpstreamPrices, multiplier)
		timePricing.PeakLongContextPrices, timePricing.PeakLongContextUpstreamPrices = scaleTierPrices(timePricing.PeakLongContextPrices, multiplier), scaleTierPrices(timePricing.PeakLongContextUpstreamPrices, multiplier)
		config.TimePricing = &timePricing
	}
	return config
}

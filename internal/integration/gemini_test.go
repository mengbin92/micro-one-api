package integration

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	billingv1 "micro-one-api/api/billing/v1"
	logv1 "micro-one-api/api/log/v1"
	billingtest "micro-one-api/app/billing/testutil"
	logtest "micro-one-api/app/log/testutil"
	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	relaydata "micro-one-api/internal/data"
	relayserver "micro-one-api/internal/server"
	dbtest "micro-one-api/platform/database/testutil"
)

// Real HTTP -> Identity/Channel gRPC -> Billing/Log owners -> migrated scratch
// SQL. The supplier is isolated; deployment credentials are never consulted.
func TestGeminiNativeOwnerChain(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			t.Setenv("RELAY_CANONICAL_USAGE_PRODUCER", "false")
			t.Setenv("BILLING_EXECUTION_DELIVERY_V1", "false")
			db := dbtest.RoutingContextDB(t, dialect)
			require.NoError(t, db.Table("users").Create(map[string]any{"id": 1, "username": "gemini-chain", "group": "default", "status": 1, "balance": 100000000}).Error)
			require.NoError(t, db.Table("system_options").Create(map[string]any{"option_key": "ModelPrice", "option_value": `{"gemini-test":{"input_price":0.001,"output_price":0.002,"cache_read_price":0.0002}}`}).Error)
			billingClient := billingv1.NewBillingServiceClient(tierTestGRPC(t, func(s *grpc.Server) { billingv1.RegisterBillingServiceServer(s, billingtest.NewServiceTierBilling(db)) }))
			logClient := logv1.NewLogServiceClient(tierTestGRPC(t, func(s *grpc.Server) { logv1.RegisterLogServiceServer(s, logtest.NewServiceTierLog(db)) }))
			var mode atomic.Value
			mode.Store("json")
			var calls atomic.Int64
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if mode.Load().(string) == "cancel" {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"partial\"}]}}]}\n\n")
					w.(http.Flusher).Flush()
					<-r.Context().Done()
					return
				}
				response := `{"responseId":"g-owner","candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"f1","name":"lookup","args":{}},"thoughtSignature":"opaque"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"cachedContentTokenCount":3,"candidatesTokenCount":4,"thoughtsTokenCount":2,"totalTokenCount":16}}`
				if mode.Load().(string) == "missing_usage" {
					response = strings.Split(response, `,"usageMetadata"`)[0] + "}"
				}
				if strings.HasSuffix(r.URL.Path, ":streamGenerateContent") {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "data: %s\n\n", response)
				} else {
					fmt.Fprint(w, response)
				}
			}))
			t.Cleanup(upstream.Close)
			identityCleanup, identityClient := setupIdentityAllowAll(t, tierTestAddress(t))
			t.Cleanup(identityCleanup)
			channelCleanup, channelClient := setupChannelWithUpstreamModel(t, tierTestAddress(t), upstream.URL, 1, relayprovider.ChannelTypeGemini, []string{"gemini-test"})
			t.Cleanup(channelCleanup)
			uc := relaybiz.NewRelayUsecase(relaydata.NewIdentityAdapter(identityClient), relaydata.NewChannelAdapter(channelClient), nil, nil)
			relay := relayserver.NewHTTPServer(identityClient, channelClient, billingClient, relayprovider.NewProviderFactory(5*time.Second), uc, logClient)
			baseURL := startRelayHTTPServer(t, relay)
			expectedCalls := int64(0)
			for _, tc := range []struct {
				name         string
				stream, fail bool
			}{{name: "json"}, {name: "sse", stream: true}, {name: "missing_usage", fail: true}, {name: "missing_usage", stream: true, fail: true}, {name: "cancel", stream: true, fail: true}} {
				t.Run(fmt.Sprintf("%s/stream=%t", tc.name, tc.stream), func(t *testing.T) {
					expectedCalls++
					mode.Store(tc.name)
					var previous int64
					require.NoError(t, db.Table("billing_reservations").Select("COALESCE(MAX(id),0)").Scan(&previous).Error)
					method := "generateContent"
					if tc.stream {
						method = "streamGenerateContent?alt=sse"
					}
					req, err := http.NewRequest("POST", baseURL+"/v1beta/models/gemini-test:"+method, strings.NewReader(`{"contents":[{"parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":32}}`))
					require.NoError(t, err)
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					req = req.WithContext(ctx)
					req.Header.Set("x-goog-api-key", "test-token")
					resp, err := http.DefaultClient.Do(req)
					require.NoError(t, err)
					var body []byte
					if tc.name == "cancel" {
						body = make([]byte, 1)
						_, err = io.ReadFull(resp.Body, body)
						require.NoError(t, err)
						cancel()
					} else {
						body, err = io.ReadAll(resp.Body)
						require.NoError(t, err)
					}
					require.NoError(t, resp.Body.Close())
					wantCode := http.StatusOK
					if tc.fail && tc.name != "cancel" {
						wantCode = http.StatusBadGateway
					}
					require.Equal(t, wantCode, resp.StatusCode, string(body))
					var reserved struct{ ReservationID, Status string }
					wantStatus := "committed"
					if tc.fail {
						wantStatus = "released"
					}
					require.Eventually(t, func() bool {
						return db.Table("billing_reservations").Where("id > ?", previous).First(&reserved).Error == nil && reserved.Status == wantStatus
					}, 3*time.Second, 10*time.Millisecond)
					var count int64
					require.NoError(t, db.Table("billing_ledgers").Where("reference_id = ? AND type = ?", reserved.ReservationID, "consume").Count(&count).Error)
					if tc.fail {
						require.Zero(t, count)
						return
					}
					require.Equal(t, 200, resp.StatusCode, string(body))
					require.Contains(t, string(body), "opaque")
					require.EqualValues(t, 1, count)
					var ledger struct {
						Amount, UncachedInputTokens, CacheReadTokens, CompletionTokens int64
						UsageContractVersion                                           int32
						UsageParseStatus, PricingConfigHash, Endpoint                  string
					}
					require.NoError(t, db.Table("billing_ledgers").Where("reference_id = ? AND type = ?", reserved.ReservationID, "consume").First(&ledger).Error)
					// Per-bucket rounding: 7*.001*10000 + 3*.0002*10000 + 6*.002*10000 = 196.
					require.EqualValues(t, -196, ledger.Amount)
					require.EqualValues(t, 1, ledger.UsageContractVersion)
					require.EqualValues(t, 7, ledger.UncachedInputTokens)
					require.EqualValues(t, 3, ledger.CacheReadTokens)
					require.EqualValues(t, 6, ledger.CompletionTokens)
					require.Equal(t, "verified", ledger.UsageParseStatus)
					require.NotEmpty(t, ledger.PricingConfigHash)
					require.Contains(t, ledger.Endpoint, "/v1beta/models/")
					replay, err := billingClient.CommitQuota(context.Background(), &billingv1.CommitQuotaRequest{ReservationId: reserved.ReservationID, ActualTokens: 999, Success: true})
					require.NoError(t, err)
					require.True(t, replay.Success)
					require.EqualValues(t, 196, replay.CommittedAmount)
					require.NoError(t, db.Table("billing_ledgers").Where("reference_id = ? AND type = ?", reserved.ReservationID, "consume").Count(&count).Error)
					require.EqualValues(t, 1, count)
					var logRow struct {
						UncachedInputTokens, CacheReadTokens, CompletionTokens int64
						UsageProtocol, UsageParseStatus                        string
					}
					require.Eventually(t, func() bool {
						return db.Table("logs").Where("reservation_id = ? AND source = ?", reserved.ReservationID, "relay-gateway").First(&logRow).Error == nil
					}, 3*time.Second, 10*time.Millisecond)
					require.EqualValues(t, 7, logRow.UncachedInputTokens)
					require.EqualValues(t, 3, logRow.CacheReadTokens)
					require.EqualValues(t, 6, logRow.CompletionTokens)
					require.Equal(t, "verified", logRow.UsageParseStatus)
					require.Equal(t, "gemini", logRow.UsageProtocol)
				})
			}
			require.EqualValues(t, expectedCalls, calls.Load())
		})
	}
}

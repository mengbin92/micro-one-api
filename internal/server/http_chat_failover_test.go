package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"micro-one-api/domain/routing"
	relaycredential "micro-one-api/domain/upstream/credential"
	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/metrics"
)

type chatCrossSourceSelector struct {
	orchestratorFailoverChannelClient
	account       *relaybiz.SubscriptionAccount
	denyAccount   bool
	accountHealth []accountHealthOutcome
}

type chatCrossSourceIdentity struct{}

func (chatCrossSourceIdentity) GetAuthSnapshot(context.Context, string, string) (*relaybiz.AuthSnapshot, error) {
	return &relaybiz.AuthSnapshot{UserID: 42, TokenID: 7, Group: "default", UserEnabled: true, TokenEnabled: true, AllowedModels: []string{"kimi-k3"}}, nil
}

func (c *chatCrossSourceSelector) SelectSubscriptionAccount(context.Context, string, string, string, bool) (*relaybiz.SubscriptionAccount, error) {
	return c.account, nil
}

func (c *chatCrossSourceSelector) GetSubscriptionAccountByID(_ context.Context, id int64) (*relaybiz.SubscriptionAccount, error) {
	if id == c.account.ID {
		return c.account, nil
	}
	return nil, nil
}

func (c *chatCrossSourceSelector) CanRoute(_ context.Context, group, model string, source routing.Source) (routing.Permission, error) {
	if group != "default" || model != "kimi-k3" {
		return routing.Permission{}, nil
	}
	if source.Kind == routing.Channel && source.ID == c.first.ID {
		return routing.Permission{Allowed: true, UpstreamModelID: "Kimi-K3"}, nil
	}
	if source.Kind == routing.Subscription && source.ID == c.account.ID {
		return routing.Permission{Allowed: !c.denyAccount, UpstreamModelID: "k3"}, nil
	}
	return routing.Permission{}, nil
}

func (c *chatCrossSourceSelector) RecordSubscriptionAccountHealth(_ context.Context, id int64, success bool) error {
	c.accountHealth = append(c.accountHealth, accountHealthOutcome{accountID: id, success: success})
	return nil
}

func TestHTTPChatCrossSourceFailover(t *testing.T) {
	for _, orchestrated := range []bool{false, true} {
		for _, subscriptionFirst := range []bool{false, true} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("orchestrator=%t/subscription_first=%t/stream=%t", orchestrated, subscriptionFirst, stream), func(t *testing.T) {
					var calls []string
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						var body struct {
							Model string `json:"model"`
						}
						if err := jsonx.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
						}
						calls = append(calls, body.Model)
						wantAuth := "Bearer channel-secret"
						if body.Model == "k3" {
							wantAuth = "Bearer subscription-secret"
						}
						if r.Header.Get("Authorization") != wantAuth {
							t.Errorf("model %s used incorrect credential", body.Model)
						}
						if (body.Model == "k3") == subscriptionFirst {
							http.Error(w, `{"error":{"message":"controlled failure"}}`, http.StatusBadGateway)
							return
						}
						bodyJSON := `{"id":"chat-test","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":5,"total_tokens":9}}`
						if body.Model == "k3" {
							bodyJSON = `{"id":"msg-test","type":"message","role":"assistant","model":"k3","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":4,"output_tokens":5}}`
						}
						if stream {
							w.Header().Set("Content-Type", "text/event-stream")
							if body.Model == "k3" {
								_, _ = fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-test\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"k3\",\"content\":[],\"usage\":{\"input_tokens\":4,\"output_tokens\":0}}}\n\n"+
									"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n\n"+
									"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
								return
							}
							_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", bodyJSON)
							return
						}
						w.Header().Set("Content-Type", "application/json")
						_, _ = w.Write([]byte(bodyJSON))
					}))
					defer upstream.Close()
					channel := &relaybiz.Channel{ID: 1, Type: relayprovider.ChannelTypeOpenAI, Status: 1, Group: "default", BaseURL: upstream.URL + "/v1", Key: "channel-secret", Priority: 20, UpstreamModelID: "Kimi-K3"}
					account := &relaybiz.SubscriptionAccount{ID: 5, Platform: "kimi", AccountType: "static_key", Status: 1, Group: "default", BaseURL: upstream.URL + "/v1", Models: []string{"kimi-k3"}, AccessToken: "redacted***secret", Priority: 10, UpstreamModelID: "k3"}
					if subscriptionFirst {
						account.Priority = 30
					}
					selector := &chatCrossSourceSelector{orchestratorFailoverChannelClient: orchestratorFailoverChannelClient{first: channel}, account: account}
					uc := relaybiz.NewRelayUsecase(chatCrossSourceIdentity{}, selector, nil, &relaybiz.RetryPolicy{MaxAttempts: 2, RetryableStatus: map[int]bool{http.StatusBadGateway: true}})
					audit := &selectionTestRecorder{}
					uc.SetSelectionRecorder(audit)
					billing := &rawBillingClient{}
					logs := &rawLogClient{}
					s := NewHTTPServer(nil, nil, billing, relayprovider.NewProviderFactory(time.Second), uc, logs)
					s.SetHybridAdaptorEnabled(true)
					resolver := &testSubscriptionResolver{meta: &relaycredential.SubscriptionAccountMetadata{ID: 5, Platform: relaycredential.PlatformKimi, AccountType: "static_key", AccessToken: "subscription-secret"}}
					s.SetSubscriptionAccountResolver(resolver)
					req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(fmt.Sprintf(`{"model":"kimi-k3","messages":[{"role":"user","content":"hi"}],"stream":%t}`, stream)))
					req.Header.Set("Authorization", "Bearer client-token")
					rec := httptest.NewRecorder()
					failoversBefore := testutil.ToFloat64(metrics.RelaySubscriptionFailoverTotal.WithLabelValues("5xx", "switched"))
					if orchestrated {
						s.handleChatCompletionsWithOrchestrator(rec, req)
					} else {
						s.handleChatCompletions(rec, req)
					}
					if rec.Code != http.StatusOK {
						t.Fatalf("status=%d body=%s attempts=%v", rec.Code, rec.Body.String(), calls)
					}
					wantCalls := []string{"Kimi-K3", "k3"}
					if subscriptionFirst {
						wantCalls = []string{"k3", "Kimi-K3"}
					}
					if !reflect.DeepEqual(calls, wantCalls) {
						t.Fatalf("attempt models=%v want=%v", calls, wantCalls)
					}
					if resolver.accountID != 5 {
						t.Fatalf("resolved account=%d want=5", resolver.accountID)
					}
					if len(billing.reserveRequests) != 2 || billing.releases != 1 || len(billing.commitRequests) != 1 || len(logs.entries) != 1 {
						t.Fatalf("reserve/release/commit/log=%d/%d/%d/%d", len(billing.reserveRequests), billing.releases, len(billing.commitRequests), len(logs.entries))
					}
					a, b := billing.reserveRequests[0], billing.reserveRequests[1]
					if a.RootRequestId == "" || a.RootRequestId != b.RootRequestId || a.RequestId == b.RequestId || a.AttemptNumber != 1 || b.AttemptNumber != 2 {
						t.Fatalf("invalid attempt linkage: %v / %v", a, b)
					}
					wantKind, wantAccount := "subscription", int64(5)
					if subscriptionFirst {
						wantKind, wantAccount = "channel", 0
					}
					if b.SourceKind != wantKind || b.SubscriptionAccountId != wantAccount || b.UpstreamModelId != wantCalls[1] {
						t.Fatalf("incorrect final reservation: %v", b)
					}
					commit := billing.commitRequests[0]
					if commit.ReservationId != "reservation-2" || commit.SubscriptionAccountId != wantAccount || commit.ActualTokens != 9 {
						t.Fatalf("incorrect final commit: %v", commit)
					}
					if logs.entries[0].SubscriptionAccountId != wantAccount || logs.entries[0].Quota != 9 {
						t.Fatalf("incorrect final log: %v", logs.entries[0])
					}
					if len(audit.events) != 2 {
						t.Fatalf("selection events=%v, want planned and outcome", audit.events)
					}
					outcome := audit.events[1]
					wantID := int64(5)
					if subscriptionFirst {
						wantID = channel.ID
					}
					if !outcome.Fallback || outcome.FallbackReason != "upstream_5xx" || outcome.Result != "success" || outcome.FinalKind != wantKind || outcome.FinalSourceID != wantID || outcome.RootRequestID != b.RootRequestId {
						t.Fatalf("incorrect selection outcome: %+v", outcome)
					}
					if !reflect.DeepEqual(selector.accountHealth, []accountHealthOutcome{{accountID: 5, success: !subscriptionFirst}}) {
						t.Fatalf("subscription health must record each attempt once: %+v", selector.accountHealth)
					}
					if !orchestrated && subscriptionFirst {
						if got := testutil.ToFloat64(metrics.RelaySubscriptionFailoverTotal.WithLabelValues("5xx", "switched")); got != failoversBefore+1 {
							t.Fatalf("subscription failovers=%v want=%v", got, failoversBefore+1)
						}
					}
				})
			}
		}
	}
}

func TestHTTPChatCrossSourceFailureBoundaries(t *testing.T) {
	for _, orchestrated := range []bool{false, true} {
		for _, scenario := range []string{"disabled", "resolver_error", "empty_credential", "route_revoked", "commit_failed", "client_error", "same_account_busy"} {
			if orchestrated && (scenario == "disabled" || scenario == "same_account_busy") {
				continue
			}
			t.Run(fmt.Sprintf("orchestrator=%t/%s", orchestrated, scenario), func(t *testing.T) {
				var calls []string
				selector := &chatCrossSourceSelector{account: &relaybiz.SubscriptionAccount{ID: 5, Platform: "kimi", Status: 1, Group: "default", AccessToken: "redacted***secret", UpstreamModelID: "k3"}}
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body struct{ Model string }
					if err := jsonx.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					calls = append(calls, body.Model)
					switch scenario {
					case "same_account_busy":
						if body.Model == "k3" {
							http.Error(w, `{"error":"temporarily busy"}`, http.StatusConflict)
						} else {
							_, _ = fmt.Fprint(w, `{"choices":[],"usage":{"prompt_tokens":4,"completion_tokens":5,"total_tokens":9}}`)
						}
					case "commit_failed":
						_, _ = fmt.Fprint(w, `{"choices":[],"usage":{"prompt_tokens":4,"completion_tokens":5,"total_tokens":9}}`)
					case "client_error":
						http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
					default:
						selector.denyAccount = scenario == "route_revoked"
						http.Error(w, `{"error":"temporary"}`, http.StatusBadGateway)
					}
				}))
				defer upstream.Close()
				selector.first = &relaybiz.Channel{ID: 1, Type: relayprovider.ChannelTypeOpenAI, Group: "default", BaseURL: upstream.URL + "/v1", Key: "channel-secret", UpstreamModelID: "Kimi-K3", Priority: 20}
				selector.account.BaseURL = upstream.URL + "/v1"
				if scenario == "client_error" || scenario == "same_account_busy" {
					selector.account.Priority = 30
				}
				uc := relaybiz.NewRelayUsecase(chatCrossSourceIdentity{}, selector, nil, &relaybiz.RetryPolicy{MaxAttempts: 2, RetryableStatus: map[int]bool{502: true}})
				billing := &rawBillingClient{}
				if scenario == "commit_failed" {
					billing.commitMessage = "billing timeout"
				}
				s := NewHTTPServer(nil, nil, billing, relayprovider.NewProviderFactory(time.Second), uc)
				s.SetHybridAdaptorEnabled(scenario != "disabled")
				resolver := &testSubscriptionResolver{meta: &relaycredential.SubscriptionAccountMetadata{ID: 5, Platform: relaycredential.PlatformKimi, AccessToken: "subscription-secret"}}
				if scenario == "resolver_error" {
					resolver.err = errors.New("secrets unavailable")
				}
				if scenario == "empty_credential" {
					resolver.meta.AccessToken = ""
				}
				s.SetSubscriptionAccountResolver(resolver)
				req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"kimi-k3","messages":[{"role":"user","content":"hi"}]}`))
				req.Header.Set("Authorization", "Bearer client-token")
				rec := httptest.NewRecorder()
				if orchestrated {
					s.handleChatCompletionsWithOrchestrator(rec, req)
				} else {
					s.handleChatCompletions(rec, req)
				}
				if scenario == "same_account_busy" {
					if rec.Code != http.StatusOK || !reflect.DeepEqual(calls, []string{"k3", "k3", "k3", "k3", "Kimi-K3"}) {
						t.Fatalf("status=%d attempts=%v body=%s", rec.Code, calls, rec.Body.String())
					}
					if len(billing.reserveRequests) != 5 || billing.releases != 4 || len(billing.commitRequests) != 1 {
						t.Fatalf("reserve/release/commit=%d/%d/%d", len(billing.reserveRequests), billing.releases, len(billing.commitRequests))
					}
					for i, reservation := range billing.reserveRequests {
						if reservation.AttemptNumber != int32(i+1) || reservation.RootRequestId != billing.reserveRequests[0].RootRequestId {
							t.Fatalf("invalid attempt linkage: %v", reservation)
						}
					}
					return
				}
				if rec.Code < 400 {
					t.Fatalf("status=%d, want failure", rec.Code)
				}
				wantCalls := []string{"Kimi-K3"}
				if scenario == "disabled" {
					wantCalls = append(wantCalls, "Kimi-K3")
				}
				if scenario == "client_error" {
					wantCalls = []string{"k3"}
				}
				if !reflect.DeepEqual(calls, wantCalls) {
					t.Fatalf("unexpected upstream replay: calls=%v want=%v", calls, wantCalls)
				}
				wantCommits := 0
				if scenario == "commit_failed" {
					wantCommits = 1
				}
				if len(billing.commitRequests) != wantCommits {
					t.Fatalf("commits=%d want=%d", len(billing.commitRequests), wantCommits)
				}
			})
		}
	}
}

package server

import (
	"context"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	billingv1 "micro-one-api/api/billing/v1"
	identityv1 "micro-one-api/api/identity/v1"
	relayprovider "micro-one-api/domain/upstream/provider"
	"micro-one-api/internal/apicompat"
	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/pkg/jsonx"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAnthropicFallbackReservesActualAccount(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	billing := &rawBillingClient{reserveMessage: "quota exhausted"}
	s := NewHTTPServer(nil, nil, billing, relayprovider.NewProviderFactory(time.Second), nil)
	plan := &relaybiz.RelayPlan{Auth: &relaybiz.AuthSnapshot{UserID: 42}, Channel: &relaybiz.Channel{ID: 1, SubscriptionAccountID: 99}}
	attempt := &relaybiz.Channel{ID: 2, Type: relayprovider.ChannelTypeAnthropic, BaseURL: "https://api.anthropic.com", Key: "key"}
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"max_tokens":8}`)
	var parsed apicompat.AnthropicRequest
	require.NoError(t, jsonx.Unmarshal(body, &parsed))
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(string(body)))
	err := s.executeAnthropicChannelAttempt(context.Background(), httptest.NewRecorder(), r, plan, attempt, &parsed, body, "m", "m")
	require.Error(t, err)
	require.Len(t, billing.reserveRequests, 1)
	require.EqualValues(t, 0, billing.reserveRequests[0].SubscriptionAccountId)
	require.Equal(t, "2", billing.reserveRequests[0].ChannelId)
}

func TestMergeStreamUsagePreservesPromptAndCache(t *testing.T) {
	previous := relayprovider.Usage{PromptTokens: 100, TotalTokens: 100, PromptTokensDetails: relayprovider.UsageTokenDetails{CachedTokens: 30, CacheCreation5mTokens: 20}}
	next := mergeProviderUsage(relayprovider.Usage{CompletionTokens: 5, TotalTokens: 5}, previous)
	if next.PromptTokens != 100 || next.TotalTokens != 105 || next.PromptTokensDetails.CachedTokens != 30 || next.PromptTokensDetails.CacheCreation5mTokens != 20 {
		t.Fatalf("usage=%+v", next)
	}
}
func TestTypedAdmissionStatusPreserved(t *testing.T) {
	if got := mapUpstreamOrInternalStatus(status.Error(codes.PermissionDenied, "denied")); got != http.StatusForbidden {
		t.Fatalf("status=%d", got)
	}
}

func TestLegacyWSQuotaFailureStopsNextTurn(t *testing.T) {
	billing := &rawBillingClient{reserveMessage: "quota exhausted"}
	server := &HTTPServer{billingClient: billing, identityClient: rawIdentityClient{userIDByToken: map[string]int64{"token": 1}}}
	turns := server.newRoutingWSTurns("token", "model", "model", &relaybiz.RelayPlan{Auth: &relaybiz.AuthSnapshot{UserID: 1, TokenID: 7, Group: "default"}}, &relaybiz.Channel{ID: 1}, &billingv1.ReserveQuotaResponse{ReservationId: "first"})
	frame := []byte(`{"type":"response.create","model":"model"}`)
	_, err := turns.beforeWrite(context.Background(), coderws.MessageText, frame)
	require.NoError(t, err)
	turns.take()
	forwarded, err := turns.beforeWrite(context.Background(), coderws.MessageText, frame)
	require.ErrorIs(t, err, errWSRoutingAdmission)
	require.Nil(t, forwarded)
	require.Nil(t, turns.take())
	require.Len(t, billing.reserveRequests, 1)
}

type revokedWSIdentity struct {
	identityv1.IdentityServiceClient
}

func (revokedWSIdentity) GetAuthSnapshot(context.Context, *identityv1.GetAuthSnapshotRequest, ...grpc.CallOption) (*identityv1.GetAuthSnapshotReply, error) {
	return nil, status.Error(codes.PermissionDenied, "token revoked or exhausted")
}

func TestLegacyWSRevalidatesTokenBeforeNextTurn(t *testing.T) {
	t.Setenv("RELAY_ROUTING_CONTEXT_V2", "false")
	billing := &rawBillingClient{}
	server := &HTTPServer{billingClient: billing, identityClient: revokedWSIdentity{}}
	turns := server.newRoutingWSTurns("token", "model", "model", &relaybiz.RelayPlan{Auth: &relaybiz.AuthSnapshot{UserID: 1, TokenID: 7}}, &relaybiz.Channel{ID: 1}, &billingv1.ReserveQuotaResponse{ReservationId: "first"})
	frame := []byte(`{"type":"response.create","model":"model"}`)
	_, err := turns.beforeWrite(context.Background(), coderws.MessageText, frame)
	require.NoError(t, err)
	turns.take()
	forwarded, err := turns.beforeWrite(context.Background(), coderws.MessageText, frame)
	require.ErrorIs(t, err, errWSRoutingAdmission)
	require.Nil(t, forwarded)
	require.Empty(t, billing.reserveRequests)
}

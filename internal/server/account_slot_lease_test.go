package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	relayprovider "micro-one-api/domain/upstream/provider"
	relayadaptor "micro-one-api/internal/adaptor"
	relaybiz "micro-one-api/internal/biz"
)

type timeoutAccountSlotClient struct {
	slotLifecycleClient
	mu       sync.Mutex
	leases   map[string]bool
	ids      []string
	events   []bool
	cleanups int
}

func (c *timeoutAccountSlotClient) RecordSubscriptionAccountSlot(ctx context.Context, _ int64, slotID string, acquired bool) error {
	c.mu.Lock()
	c.ids = append(c.ids, slotID)
	c.events = append(c.events, acquired)
	if acquired {
		c.leases = map[string]bool{slotID: true}
		c.mu.Unlock()
		// The owner acquired the lease, but its acknowledgment was lost.
		<-ctx.Done()
		return ctx.Err()
	}
	defer c.mu.Unlock()
	c.cleanups++
	if c.cleanups == 1 {
		return errors.New("first cleanup unavailable")
	}
	delete(c.leases, slotID)
	return nil
}

func TestSubscriptionSlotTimeoutRetriesCleanupWhenLocalAdmissionEnds(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "orchestrator admission", true: "legacy adaptor"}[legacy], func(t *testing.T) {
			client := &timeoutAccountSlotClient{}
			uc := relaybiz.NewRelayUsecase(nil, client, nil, nil)
			s := NewHTTPServer(nil, nil, nil, nil, uc)
			require.True(t, s.accountRPM.TryAcquire(context.Background(), 3, 1))
			plan := &relaybiz.RelayPlan{
				Auth:    &relaybiz.AuthSnapshot{UserID: 1, Group: "default"},
				Channel: &relaybiz.Channel{ID: 3, Type: relayprovider.ChannelTypeCodexOAuth, SubscriptionAccountID: 3},
				Account: &relaybiz.SubscriptionAccount{ID: 3, Concurrency: 1, RPMLimit: 1},
			}
			if legacy {
				result := s.executeSubscriptionAccountViaAdaptor(context.Background(), plan, "gpt-5", nil, []byte(`{"model":"gpt-5","messages":[]}`), relayadaptor.FormatOpenAIChatCompletions, "")
				require.Equal(t, http.StatusServiceUnavailable, result.statusCode)
				require.True(t, result.rpmFull)
			} else {
				_, err := (httpRelayLifecycleHooks{s: s}).AcquireRelayAttempt(context.Background(), plan, relaybiz.ExecutorRequest{})
				require.Error(t, err)
			}
			require.Zero(t, s.accountConcurrency.(*relaybiz.MemoryAccountConcurrencyLimiter).Inflight(3))
			client.mu.Lock()
			defer client.mu.Unlock()
			require.Empty(t, client.leases, "final release must retry cleanup after a lost acquire acknowledgment")
			require.Equal(t, []bool{true, false, false}, client.events)
			require.NotEmpty(t, client.ids[0])
			require.Equal(t, []string{client.ids[0], client.ids[0], client.ids[0]}, client.ids)
		})
	}
}

func TestOrchestratorSubscriptionRPMRejectionDoesNotRecordUpstreamHealth(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "nonstream", true: "stream"}[stream], func(t *testing.T) {
			client := &adaptorFailoverChannelClient{accounts: []*relaybiz.SubscriptionAccount{{
				ID: 3, Platform: "codex", Status: 1, Group: "default", Models: []string{"gpt-4o-mini"}, Concurrency: 1, RPMLimit: 1,
			}}}
			uc := relaybiz.NewRelayUsecase(orchestratorIdentityClient{}, client, nil, &relaybiz.RetryPolicy{MaxAttempts: 1})
			s := NewHTTPServer(nil, nil, nil, nil, uc)
			require.True(t, s.accountRPM.TryAcquire(context.Background(), 3, 1))
			o := newRelayOrchestrator(uc, relayprovider.NewProviderFactory(time.Second), httpRelayLifecycleHooks{s: s}, nil)
			forward, streamForward := &matrixForwarder{}, &orchestratorFailoverStreamForwarder{}
			o.forwardPort, o.streamPort = forward, streamForward
			result, err := o.Execute(context.Background(), &RelayRequest{
				Token: "token", Model: "gpt-4o-mini", Endpoint: EndpointChatCompletions, IsStream: stream,
				Body: strings.NewReader(`{"model":"gpt-4o-mini","messages":[]}`),
			})
			require.Error(t, err)
			require.Equal(t, http.StatusServiceUnavailable, result.StatusCode, "%v", err)
			require.Zero(t, forward.calls)
			require.Empty(t, streamForward.calls)
			require.Empty(t, client.health, "local admission never attempted this upstream account")
			require.Empty(t, client.modelHealth, "local admission never attempted this upstream model")
			require.Zero(t, s.accountConcurrency.(*relaybiz.MemoryAccountConcurrencyLimiter).Inflight(3))
		})
	}
}

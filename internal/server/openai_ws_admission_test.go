package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"micro-one-api/domain/routing"
	relaycredential "micro-one-api/domain/upstream/credential"
	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
)

type wsAdmissionChannelClient struct {
	wsStickySubscriptionClient
	mu                    sync.Mutex
	slots                 []accountSlotReport
	unhealthy             []int64
	fallback              *relaybiz.Channel
	rejectChannel         bool
	channelSlots          []bool
	channelHealthFailures int
}

func (c *wsAdmissionChannelClient) RecordChannelSlot(_ context.Context, _ int64, _ string, acquired bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.channelSlots = append(c.channelSlots, acquired)
	if acquired && c.rejectChannel {
		return errors.New("channel at slot limit")
	}
	return nil
}

func (c *wsAdmissionChannelClient) RecordChannelHealth(_ context.Context, _ int64, success bool, _ string, _ int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !success {
		c.channelHealthFailures++
	}
	return nil
}

func (c *wsAdmissionChannelClient) RecordSubscriptionAccountSlot(_ context.Context, id int64, _ string, acquired bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.slots = append(c.slots, accountSlotReport{accountID: id, acquired: acquired})
	return nil
}

func (c *wsAdmissionChannelClient) RecordSubscriptionAccountHealth(_ context.Context, id int64, success bool) error {
	if !success {
		c.mu.Lock()
		c.unhealthy = append(c.unhealthy, id)
		c.mu.Unlock()
	}
	return nil
}

func (c *wsAdmissionChannelClient) SelectChannelExcluding(_ context.Context, _, _ string, excluded map[int64]bool) (*relaybiz.Channel, error) {
	if c.fallback != nil && !excluded[c.fallback.ID] {
		return c.fallback, nil
	}
	return nil, nil
}

func (c *wsAdmissionChannelClient) CanRoute(_ context.Context, _, _ string, source routing.Source) (routing.Permission, error) {
	return routing.Permission{Allowed: source.Kind == routing.Subscription && source.ID == c.account.ID || source.Kind == routing.Channel && c.fallback != nil && source.ID == c.fallback.ID}, nil
}

func newWSAdmissionGateway(t *testing.T, upstreamURL string) (*HTTPServer, *wsAdmissionChannelClient, *rawBillingClient, *coderws.Conn, <-chan struct{}) {
	t.Helper()
	account := &relaybiz.SubscriptionAccount{ID: 3, Platform: "codex", Group: "default", Status: 1, Concurrency: 1, RPMLimit: 1}
	channels := &wsAdmissionChannelClient{wsStickySubscriptionClient: wsStickySubscriptionClient{account: account}}
	uc := relaybiz.NewRelayUsecase(nil, channels, nil, nil)
	billing := &rawBillingClient{}
	s := NewHTTPServer(rawIdentityClient{}, nil, billing, relayprovider.NewProviderFactory(time.Second), uc)
	s.accountResolver = &testSubscriptionResolver{meta: &relaycredential.SubscriptionAccountMetadata{AccessToken: "subscription-secret"}}
	plan := &relaybiz.RelayPlan{
		Auth:    &relaybiz.AuthSnapshot{UserID: 42, TokenID: 7, Group: "default", UserEnabled: true, TokenEnabled: true},
		Channel: &relaybiz.Channel{ID: -3, SubscriptionAccountID: 3, Type: relayprovider.ChannelTypeOpenAI, BaseURL: upstreamURL},
		Account: account, ClientModel: "gpt-5", GlobalModel: "gpt-5", ResolvedModel: "gpt-5",
	}
	s.wsScheduler = NewOpenAIWSRoutingScheduler(s)
	s.wsScheduler.planner = &schedulerPlannerStub{plan: plan}
	s.SetOpenAIWSTimeouts(time.Second, time.Second, time.Second, time.Second)
	done := make(chan struct{})
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		s.handleResponsesWebSocket(r.Context(), w, r)
	}))
	t.Cleanup(gateway.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := coderws.Dial(ctx, wsURL(gateway.URL)+"/v1/responses", &coderws.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer user-token"}, "X-Session-Hash": []string{"admission-session"}}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.CloseNow() })
	return s, channels, billing, conn, done
}

func writeWSAdmissionTurn(t *testing.T, conn *coderws.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5","input":"hello"}`)))
}

func awaitWSAdmissionDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("websocket relay did not finish")
	}
}

func TestSubscriptionWebSocketAdmissionRejectsBeforeDial(t *testing.T) {
	for _, busy := range []bool{true, false} {
		t.Run(map[bool]string{true: "concurrency", false: "RPM"}[busy], func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				http.Error(w, "should not dial", http.StatusBadGateway)
			}))
			defer upstream.Close()
			s, channels, billing, conn, done := newWSAdmissionGateway(t, upstream.URL)
			if busy {
				release, acquired := s.accountConcurrency.TryAcquire(context.Background(), 3, 1)
				require.True(t, acquired)
				defer release()
			} else {
				require.True(t, s.accountRPM.TryAcquire(context.Background(), 3, 1))
			}
			writeWSAdmissionTurn(t, conn)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, _, err := conn.Read(ctx)
			require.Error(t, err)
			awaitWSAdmissionDone(t, done)
			require.Zero(t, calls.Load(), "local admission rejection must stop before dial")
			require.Equal(t, 1, billing.releases)
			channels.mu.Lock()
			defer channels.mu.Unlock()
			require.Empty(t, channels.unhealthy, "local saturation must not mark the account unhealthy")
			if busy {
				require.Empty(t, channels.slots)
			} else {
				require.Equal(t, []accountSlotReport{{accountID: 3, acquired: true}, {accountID: 3, acquired: false}}, channels.slots)
			}
		})
	}
}

func TestSubscriptionWebSocketSlotHeldUntilConnectionCloses(t *testing.T) {
	received := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		_, _, err = conn.Read(r.Context())
		if err != nil {
			t.Error(err)
			return
		}
		close(received)
		_, _, _ = conn.Read(r.Context())
	}))
	defer upstream.Close()
	s, channels, _, conn, done := newWSAdmissionGateway(t, upstream.URL)
	writeWSAdmissionTurn(t, conn)
	select {
	case <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream did not receive first turn")
	}
	require.EqualValues(t, 1, s.accountConcurrency.(*relaybiz.MemoryAccountConcurrencyLimiter).Inflight(3))
	channels.mu.Lock()
	reports := append([]accountSlotReport(nil), channels.slots...)
	channels.mu.Unlock()
	require.Equal(t, []accountSlotReport{{accountID: 3, acquired: true}}, reports)
	_ = conn.CloseNow()
	awaitWSAdmissionDone(t, done)
	require.Zero(t, s.accountConcurrency.(*relaybiz.MemoryAccountConcurrencyLimiter).Inflight(3))
	channels.mu.Lock()
	defer channels.mu.Unlock()
	require.Equal(t, []accountSlotReport{{accountID: 3, acquired: true}, {accountID: 3, acquired: false}}, channels.slots)
}

func TestSubscriptionWebSocketSlotReleasedAfterDialFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
	}))
	defer upstream.Close()
	s, channels, billing, conn, done := newWSAdmissionGateway(t, upstream.URL)
	writeWSAdmissionTurn(t, conn)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, err := conn.Read(ctx)
	require.Error(t, err)
	awaitWSAdmissionDone(t, done)
	require.Zero(t, s.accountConcurrency.(*relaybiz.MemoryAccountConcurrencyLimiter).Inflight(3))
	require.Equal(t, 1, billing.releases)
	channels.mu.Lock()
	defer channels.mu.Unlock()
	require.Equal(t, []accountSlotReport{{accountID: 3, acquired: true}, {accountID: 3, acquired: false}}, channels.slots)
}

func TestSubscriptionWebSocketBusyAccountFailsOver(t *testing.T) {
	var usedSubscription atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		usedSubscription.Store(r.Header.Get("Authorization") != "Bearer channel-secret")
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		if _, _, err := conn.Read(r.Context()); err != nil {
			return
		}
		_ = conn.Write(r.Context(), coderws.MessageText, []byte(`{"type":"response.completed","response":{"id":"resp-fallback","status":"completed","usage":{"input_tokens":1,"output_tokens":1}}}`))
	}))
	defer upstream.Close()
	s, channels, billing, conn, done := newWSAdmissionGateway(t, upstream.URL)
	channels.fallback = &relaybiz.Channel{ID: 11, Type: relayprovider.ChannelTypeOpenAI, BaseURL: upstream.URL, Key: "channel-secret"}
	release, acquired := s.accountConcurrency.TryAcquire(context.Background(), 3, 1)
	require.True(t, acquired)
	defer release()
	writeWSAdmissionTurn(t, conn)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, err := conn.Read(ctx)
	require.NoError(t, err)
	_ = conn.CloseNow()
	awaitWSAdmissionDone(t, done)
	require.False(t, usedSubscription.Load(), "busy account must switch source before dial")
	require.Len(t, billing.reserveRequests, 2)
	require.Equal(t, 1, billing.releases, "replace first reservation on failover")
	channels.mu.Lock()
	defer channels.mu.Unlock()
	require.Empty(t, channels.unhealthy)
	require.Empty(t, channels.slots, "busy account must not acquire a slot")
}

func TestSubscriptionWebSocketLaterTurnsRespectAccountLimits(t *testing.T) {
	for _, sessionWindow := range []bool{false, true} {
		t.Run(map[bool]string{false: "RPM", true: "session window"}[sessionWindow], func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := coderws.Accept(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.CloseNow()
				for {
					if _, _, err := conn.Read(r.Context()); err != nil {
						return
					}
					calls.Add(1)
					if err := conn.Write(r.Context(), coderws.MessageText, []byte(`{"type":"response.completed","response":{"id":"resp-admission","status":"completed","usage":{"input_tokens":1,"output_tokens":1}}}`)); err != nil {
						return
					}
				}
			}))
			defer upstream.Close()
			s, channels, billing, conn, done := newWSAdmissionGateway(t, upstream.URL)
			if sessionWindow {
				channels.account.RPMLimit = 2
				channels.account.SessionWindowLimitUSD = 1e-12
			}
			writeWSAdmissionTurn(t, conn)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, _, err := conn.Read(ctx)
			require.NoError(t, err, "first turn must not count RPM twice")
			require.Eventually(t, func() bool { _, found := s.lookupResponseRoute("resp-admission"); return found }, time.Second, time.Millisecond)
			writeWSAdmissionTurn(t, conn)
			_, _, err = conn.Read(ctx)
			require.Error(t, err, "second turn must exceed account limit")
			awaitWSAdmissionDone(t, done)
			require.EqualValues(t, 1, calls.Load())
			require.Len(t, billing.reserveRequests, 1, "rejected turn must not reserve quota")
			require.Zero(t, s.accountConcurrency.(*relaybiz.MemoryAccountConcurrencyLimiter).Inflight(3))
			channels.mu.Lock()
			defer channels.mu.Unlock()
			require.Empty(t, channels.unhealthy, "turn admission rejection must not poison health")
		})
	}
}

func TestWebSocketChannelSlotRejectionFailsOverBeforeDial(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer subscription-secret" {
			t.Error("rejected API-key channel was forwarded")
		}
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		if _, _, err := conn.Read(r.Context()); err != nil {
			return
		}
		_ = conn.Write(r.Context(), coderws.MessageText, []byte(`{"type":"response.completed","response":{"id":"resp-slot-fallback","status":"completed","usage":{"input_tokens":1,"output_tokens":1}}}`))
	}))
	defer upstream.Close()
	s, channels, billing, conn, done := newWSAdmissionGateway(t, upstream.URL)
	channels.rejectChannel = true
	channels.account.BaseURL = upstream.URL
	channels.account.Models = []string{"gpt-5"}
	plan := s.wsScheduler.planner.(*schedulerPlannerStub).plan
	plan.Channel = &relaybiz.Channel{ID: 11, Type: relayprovider.ChannelTypeOpenAI, BaseURL: upstream.URL}
	plan.Account = nil
	writeWSAdmissionTurn(t, conn)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, err := conn.Read(ctx)
	require.NoError(t, err)
	_ = conn.CloseNow()
	awaitWSAdmissionDone(t, done)
	require.EqualValues(t, 1, calls.Load())
	require.Len(t, billing.reserveRequests, 2)
	require.Equal(t, 1, billing.releases)
	require.Zero(t, s.accountConcurrency.(*relaybiz.MemoryAccountConcurrencyLimiter).Inflight(3))
	channels.mu.Lock()
	defer channels.mu.Unlock()
	require.Zero(t, channels.channelHealthFailures)
	require.Equal(t, []bool{true, false}, channels.channelSlots)
	require.Equal(t, []accountSlotReport{{accountID: 3, acquired: true}, {accountID: 3, acquired: false}}, channels.slots)
}

package integration

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	billingv1 "micro-one-api/api/billing/v1"
	channelv1 "micro-one-api/api/channel/v1"
	channeltestutil "micro-one-api/app/channel/testutil"
	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	relaydata "micro-one-api/internal/data"
	relayserver "micro-one-api/internal/server"
	"micro-one-api/pkg/jsonx"
)

type channelSlotRepo struct {
	*testChannelRepo
	mu sync.Mutex
}

func (r *channelSlotRepo) FindByID(ctx context.Context, id int64) (*channeltestutil.Channel, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch, err := r.testChannelRepo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	copy := *ch
	copy.Models = append([]string(nil), ch.Models...)
	return &copy, nil
}

func (r *channelSlotRepo) RecordHealth(ctx context.Context, event channeltestutil.ChannelHealthEvent, threshold int32, cooldown time.Duration) (*channeltestutil.Channel, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch, err := r.testChannelRepo.RecordHealth(ctx, event, threshold, cooldown)
	if err != nil {
		return nil, err
	}
	copy := *ch
	copy.Models = append([]string(nil), ch.Models...)
	return &copy, nil
}

func (r *channelSlotRepo) RecordUsage(ctx context.Context, id, quota int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.testChannelRepo.RecordUsage(ctx, id, quota)
}

func setupChannelSlotOwner(t *testing.T, upstreamURL string, opts ...grpc.ServerOption) (*channeltestutil.ChannelUsecase, channelv1.ChannelServiceClient) {
	t.Helper()
	repo := &testChannelRepo{
		channels: map[int64]*channeltestutil.Channel{
			1: {
				ID: 1, Type: 1, Name: "slot-channel",
				Status: channeltestutil.ChannelStatusEnabled, BaseURL: upstreamURL,
				Group: "default", Models: []string{"gpt-4o-mini"},
				Priority: 10, Weight: 1, Key: "slot-key",
			},
		},
		abilities: map[string][]channeltestutil.Ability{
			"default:gpt-4o-mini": {
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 1, Enabled: true, Priority: 10},
			},
		},
	}
	uc := channeltestutil.NewChannelUsecase(&channelSlotRepo{testChannelRepo: repo}, nil)
	srv := grpc.NewServer(opts...)
	channelv1.RegisterChannelServiceServer(srv, channeltestutil.NewChannelService(uc))
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(lis)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		srv.Stop()
		_ = lis.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		srv.Stop()
		_ = lis.Close()
	})
	return uc, channelv1.NewChannelServiceClient(conn)
}

type channelSlotGatedBilling struct {
	mockBillingService
	arrived  chan struct{}
	release  <-chan struct{}
	released chan string
	commits  atomic.Int32
}

func (b *channelSlotGatedBilling) ReserveQuota(ctx context.Context, req *billingv1.ReserveQuotaRequest) (*billingv1.ReserveQuotaResponse, error) {
	b.arrived <- struct{}{}
	select {
	case <-b.release:
		return &billingv1.ReserveQuotaResponse{Success: true, ReservationId: req.RequestId, ReservedAmount: 100}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (b *channelSlotGatedBilling) ReleaseQuota(ctx context.Context, req *billingv1.ReleaseQuotaRequest) (*billingv1.ReleaseQuotaResponse, error) {
	b.released <- req.ReservationId
	return b.mockBillingService.ReleaseQuota(ctx, req)
}

func (b *channelSlotGatedBilling) CommitQuota(ctx context.Context, req *billingv1.CommitQuotaRequest) (*billingv1.CommitQuotaResponse, error) {
	b.commits.Add(1)
	return b.mockBillingService.CommitQuota(ctx, req)
}

func TestRelayChannelSlot_CapacityAfterPlannedBurst(t *testing.T) {
	t.Setenv("IDENTITY_ROUTING_V2", "false")
	t.Setenv("RELAY_ROUTING_CONTEXT_V2", "false")
	t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "false")
	const total = 101
	billingRelease, upstreamRelease := make(chan struct{}), make(chan struct{})
	var releaseBilling, releaseUpstream sync.Once
	upstreamArrived := make(chan struct{}, total)
	upstreamURL, upstreamCleanup := startMockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		upstreamArrived <- struct{}{}
		<-upstreamRelease
		w.Header().Set("Content-Type", "application/json")
		// Zero usage avoids unrelated token-settlement auth in the legacy identity fixture.
		_, _ = w.Write([]byte(`{"id":"capacity","object":"chat.completion","model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`))
	})
	defer upstreamCleanup()
	identityCleanup, identityClient := setupIdentityAllowAll(t, "127.0.0.1:19701")
	defer identityCleanup()
	billing := &channelSlotGatedBilling{
		arrived: make(chan struct{}, total), release: billingRelease,
		released: make(chan string, total),
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	billingServer := grpc.NewServer()
	billingv1.RegisterBillingServiceServer(billingServer, billing)
	go billingServer.Serve(lis)
	billingConn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = billingConn.Close(); billingServer.Stop(); _ = lis.Close() }()
	defer releaseBilling.Do(func() { close(billingRelease) })
	defer releaseUpstream.Do(func() { close(upstreamRelease) })
	channelUC, channelClient := setupChannelSlotOwner(t, upstreamURL)
	uc := relaybiz.NewRelayUsecase(relaydata.NewIdentityAdapter(identityClient), relaydata.NewChannelAdapter(channelClient), nil, &relaybiz.RetryPolicy{MaxAttempts: 1})
	srv := relayserver.NewHTTPServer(identityClient, channelClient, billingv1.NewBillingServiceClient(billingConn), relayprovider.NewProviderFactory(15*time.Second), uc)
	routes := khttp.NewServer(khttp.Timeout(15 * time.Second))
	srv.RegisterRoutes(routes)
	relay := httptest.NewServer(routes)
	t.Cleanup(relay.Close)
	relayURL := relay.URL
	type response struct {
		status int
		body   string
		err    error
	}
	done := make(chan response, total)
	for range total {
		go func() {
			req, _ := http.NewRequest(http.MethodPost, relayURL+"/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`))
			req.Header.Set("Authorization", "Bearer test-token")
			req.Header.Set("Content-Type", "application/json")
			resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
			if err != nil {
				done <- response{err: err}
				return
			}
			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			done <- response{status: resp.StatusCode, body: string(body), err: err}
		}()
	}
	deadline := time.After(5 * time.Second)
	for i := range total {
		select {
		case <-billing.arrived:
		case <-deadline:
			t.Fatalf("only %d/%d requests reached billing", i, total)
		}
	}
	if stats := channeltestutil.SelectorStats(channelUC); stats[1].Inflight != 0 {
		t.Fatalf("planning occupied execution slots: %+v", stats)
	}
	releaseBilling.Do(func() { close(billingRelease) })
	deadline = time.After(5 * time.Second)
	for i := range 100 {
		select {
		case <-upstreamArrived:
		case <-deadline:
			t.Fatalf("only %d/100 requests reached upstream", i)
		}
	}
	select {
	case rejected := <-done:
		if rejected.err != nil || rejected.status != http.StatusServiceUnavailable {
			t.Fatalf("overflow response=%+v, want 503", rejected)
		}
	case <-deadline:
		t.Fatal("overflow request was not rejected")
	}
	if len(billing.released) != 1 || <-billing.released == "" {
		t.Fatal("overflow request did not release exactly one reservation")
	}
	if stats := channeltestutil.SelectorStats(channelUC); stats[1].Inflight != 100 || stats[1].ErrorRate != 0 || stats[1].IsCircuitOpen {
		t.Fatalf("overflow exceeded capacity or polluted upstream health: %+v", stats)
	}
	releaseUpstream.Do(func() { close(upstreamRelease) })
	deadline = time.After(5 * time.Second)
	for range 100 {
		select {
		case completed := <-done:
			if completed.err != nil || completed.status != http.StatusOK {
				t.Fatalf("admitted response=%+v, want 200", completed)
			}
		case <-deadline:
			t.Fatal("admitted request did not complete")
		}
	}
	if stats := channeltestutil.SelectorStats(channelUC); stats[1].Inflight != 0 || stats[1].ErrorRate != 0 || billing.commits.Load() != 100 || len(upstreamArrived) != 0 {
		t.Fatalf("burst did not drain cleanly: stats=%+v commits=%d extra_upstreams=%d", stats, billing.commits.Load(), len(upstreamArrived))
	}
}

func TestRelayChannelSlot_OwnerResponseTimeoutReleasesLease(t *testing.T) {
	owner, client := setupChannelSlotOwner(t, "http://unused.invalid", grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		reply, err := handler(ctx, req)
		if slot, ok := req.(*channelv1.RecordChannelSlotRequest); ok && slot.Acquired && err == nil {
			<-ctx.Done()
		}
		return reply, err
	}))
	uc := relaybiz.NewRelayUsecase(nil, relaydata.NewChannelAdapter(client), nil, nil)
	release, err := uc.AcquireChannelSlot(context.Background(), &relaybiz.Channel{ID: 1})
	if err == nil || release != nil {
		if release != nil {
			release()
		}
		t.Fatalf("timed-out acquisition release=%v err=%v, want nil release and an error", release != nil, err)
	}
	if stats := channeltestutil.SelectorStats(owner); stats[1].Inflight != 0 {
		t.Fatalf("owner acquired before RPC timeout but cleanup left an active lease: %+v", stats)
	}
}

func TestRelayAccountSlot_OwnerResponseTimeoutReleasesLease(t *testing.T) {
	acquired := make(chan string, 1)
	released := make(chan string, 4)
	owner, client := setupChannelSlotOwner(t, "http://unused.invalid", grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		reply, err := handler(ctx, req)
		if slot, ok := req.(*channelv1.RecordSubscriptionAccountSlotRequest); ok && err == nil {
			if slot.Acquired {
				if reply.(*channelv1.RecordSubscriptionAccountSlotResponse).GetSuccess() {
					acquired <- slot.GetSlotId()
				}
				<-ctx.Done()
			} else {
				released <- slot.GetSlotId()
			}
		}
		return reply, err
	}))
	uc := relaybiz.NewRelayUsecase(nil, relaydata.NewChannelAdapter(client), nil, nil)
	limiter := relaybiz.NewAccountConcurrencyLimiter()
	releaseLocal, ok := limiter.TryAcquire(context.Background(), 42, 1)
	if !ok {
		t.Fatal("real account concurrency slot was not granted")
	}
	defer releaseLocal()
	releaseTelemetry := uc.AcquireSubscriptionAccountSlot(context.Background(), 42)
	if releaseTelemetry == nil {
		t.Fatal("best-effort telemetry must leave execution with a release function")
	}
	defer releaseTelemetry()
	var slotID string
	select {
	case slotID = <-acquired:
		if slotID == "" {
			t.Fatal("owner acquired an unnamed telemetry slot")
		}
	default:
		t.Fatal("test did not reach a successful owner acquisition before the response timeout")
	}
	if stats := owner.AccountSelectorStats(); stats[42].Inflight != 0 {
		t.Fatalf("account telemetry response timeout leaked an owner lease: %+v", stats)
	}
	if limiter.Inflight(42) != 1 {
		t.Fatal("telemetry response timeout altered the real concurrency admission")
	}
	for range 2 {
		releaseLocal()
		releaseTelemetry()
	}
	if stats := owner.AccountSelectorStats(); limiter.Inflight(42) != 0 || stats[42].Inflight != 0 {
		t.Fatalf("completed attempt retained real or telemetry slots: local=%d owner=%+v", limiter.Inflight(42), stats)
	}
	if len(released) == 0 {
		t.Fatal("timed-out telemetry acquisition did not send cleanup")
	}
	for len(released) > 0 {
		if releasedID := <-released; releasedID != slotID {
			t.Fatalf("cleanup slot ID=%q, want acquisition ID=%q", releasedID, slotID)
		}
	}
}

func TestRelayChannelSlot_UserRPMRejectionDoesNotLeak(t *testing.T) {
	t.Setenv("IDENTITY_ROUTING_V2", "false")
	t.Setenv("RELAY_ROUTING_CONTEXT_V2", "false")
	t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "false")
	identityCleanup, identityClient := setupIdentityAllowAll(t, "127.0.0.1:19701")
	defer identityCleanup()
	channelUC, channelClient := setupChannelSlotOwner(t, "http://unused.invalid")
	uc := relaybiz.NewRelayUsecase(relaydata.NewIdentityAdapter(identityClient), relaydata.NewChannelAdapter(channelClient), nil, nil)
	srv := relayserver.NewHTTPServer(identityClient, channelClient, nil, nil, uc)
	limiter := relaybiz.NewAccountRPMLimiter()
	if !limiter.TryAcquire(context.Background(), 1, 1) {
		t.Fatal("failed to fill user RPM window")
	}
	srv.SetUserRPMLimiter(limiter)
	srv.SetUserRPMLimit(1)
	relayURL := startRelayHTTPServer(t, srv)
	client := &http.Client{Timeout: 5 * time.Second}
	for i := 0; i < 101; i++ {
		req, err := http.NewRequest(http.MethodPost, relayURL+"/v1/embeddings", bytes.NewBufferString(`{"model":"gpt-4o-mini","input":"hello"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer test-token")
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("request %d: got %d (%s), want 429; owner stats=%+v", i+1, resp.StatusCode, body, channeltestutil.SelectorStats(channelUC))
		}
	}
	if stats := channeltestutil.SelectorStats(channelUC); stats[1].Inflight != 0 {
		t.Fatalf("RPM-rejected requests leaked owner slots: %+v", stats)
	}
}

func TestRelayChannelSlot_CountsExecutingRequest(t *testing.T) {
	t.Setenv("IDENTITY_ROUTING_V2", "false")
	t.Setenv("RELAY_ROUTING_CONTEXT_V2", "false")
	t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "false")
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	upstreamURL, upstreamCleanup := startMockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		w.Header().Set("Content-Type", "application/json")
		_ = jsonx.NewEncoder(w).Encode(relayprovider.ChatCompletionsResponse{
			ID: "slot-response", Object: "chat.completion", Model: "gpt-4o-mini",
			Choices: []relayprovider.Choice{{Index: 0, Message: relayprovider.Message{Role: "assistant", Content: "ok"}, FinishReason: "stop"}},
			Usage:   relayprovider.Usage{PromptTokens: 2, CompletionTokens: 1, TotalTokens: 3},
		})
	})
	defer upstreamCleanup()
	identityCleanup, identityClient := setupIdentityAllowAll(t, "127.0.0.1:19701")
	defer identityCleanup()
	billingCleanup, billingClient := setupInMemoryBillingService(t, "127.0.0.1:19703")
	defer billingCleanup()
	channelUC, channelClient := setupChannelSlotOwner(t, upstreamURL)
	uc := relaybiz.NewRelayUsecase(relaydata.NewIdentityAdapter(identityClient), relaydata.NewChannelAdapter(channelClient), nil, nil)
	srv := relayserver.NewHTTPServer(identityClient, channelClient, billingClient, relayprovider.NewProviderFactory(5*time.Second), uc)
	relayURL := startRelayHTTPServer(t, srv)
	done := make(chan error, 1)
	go func() {
		req, err := http.NewRequest(http.MethodPost, relayURL+"/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`))
		if err != nil {
			done <- err
			return
		}
		req.Header.Set("Authorization", "Bearer test-token")
		req.Header.Set("Content-Type", "application/json")
		resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err != nil {
			done <- err
			return
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err == nil && resp.StatusCode != http.StatusOK {
			err = fmt.Errorf("got %d (%s), want 200", resp.StatusCode, body)
		}
		done <- err
	}()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("request did not reach upstream: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("request did not reach upstream")
	}
	if stats := channeltestutil.SelectorStats(channelUC); stats[1].Inflight != 1 {
		unblock()
		t.Fatalf("executing request must hold one owner slot: %+v", stats)
	}
	unblock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request did not complete")
	}
	if stats := channeltestutil.SelectorStats(channelUC); stats[1].Inflight != 0 {
		t.Fatalf("completed request leaked owner slots: %+v", stats)
	}
}

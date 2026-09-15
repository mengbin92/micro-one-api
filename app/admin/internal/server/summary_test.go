package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	billingv1 "micro-one-api/api/billing/v1"
	channelv1 "micro-one-api/api/channel/v1"
	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/pkg/jsonx"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// Exercise the real HTTP handler and service adapters with deterministic RPC
// latency. Only the RPC boundary is replaced; fixtures match http_test.go.
type summaryRPCFixture struct {
	grpc.ClientConnInterface
	mu                  sync.Mutex
	fixtureMu           sync.Mutex
	active, peak, calls int
	delay               time.Duration
	failMethod          string
	identity            adminHTTPIdentityClient
	channel             adminHTTPChannelClient
	billing             adminHTTPBillingClient
}

func (f *summaryRPCFixture) Invoke(ctx context.Context, method string, args, reply any, _ ...grpc.CallOption) error {
	f.mu.Lock()
	f.calls++
	f.active++
	f.peak = max(f.peak, f.active)
	f.mu.Unlock()
	defer func() { f.mu.Lock(); f.active--; f.mu.Unlock() }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(f.delay):
	}
	if f.failMethod != "" && strings.Contains(method, f.failMethod) {
		return status.Error(codes.Unavailable, "fixture unavailable")
	}
	var client any
	switch {
	case strings.Contains(method, "IdentityService"):
		client = &f.identity
	case strings.Contains(method, "ChannelService"):
		client = &f.channel
	case strings.Contains(method, "BillingService"):
		client = &f.billing
	default:
		return fmt.Errorf("unexpected RPC %s", method)
	}
	// Existing fixtures record requests in plain fields. Serialize only the
	// in-memory fixture invocation, never the simulated I/O wait.
	f.fixtureMu.Lock()
	defer f.fixtureMu.Unlock()
	name := method[strings.LastIndex(method, "/")+1:]
	result := reflect.ValueOf(client).MethodByName(name).Call([]reflect.Value{reflect.ValueOf(ctx), reflect.ValueOf(args)})
	if !result[1].IsNil() {
		return result[1].Interface().(error)
	}
	proto.Merge(reply.(proto.Message), result[0].Interface().(proto.Message))
	return nil
}

func summaryFixtureServer(f *summaryRPCFixture) http.Handler {
	return newAdminHTTPTestServer(identityv1.NewIdentityServiceClient(f), channelv1.NewChannelServiceClient(f), billingv1.NewBillingServiceClient(f))
}

func requestSummary(srv http.Handler) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/admin/summary", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func TestAdminSummaryLoadProfile(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	for _, concurrency := range []int{1, 4} {
		t.Run(fmt.Sprintf("clients=%d", concurrency), func(t *testing.T) {
			f := &summaryRPCFixture{delay: 20 * time.Millisecond}
			srv := summaryFixtureServer(f)
			const samples = 12
			durations := make([]time.Duration, samples)
			var wg sync.WaitGroup
			for client := 0; client < concurrency; client++ {
				wg.Go(func() {
					for i := client; i < samples; i += concurrency {
						start := time.Now()
						rec := requestSummary(srv)
						durations[i] = time.Since(start)
						if rec.Code != http.StatusOK {
							t.Errorf("status=%d body=%s", rec.Code, rec.Body.String())
						}
					}
				})
			}
			wg.Wait()
			slices.Sort(durations)
			t.Logf("samples=%d rpc_delay=20ms p50=%s p95=%s rpc_calls=%d peak_rpc=%d", samples, durations[5], durations[11], f.calls, f.peak)
		})
	}
}

func TestAdminSummaryBoundedParallelism(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	f := &summaryRPCFixture{delay: 20 * time.Millisecond}
	rec := requestSummary(summaryFixtureServer(f))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Greater(t, f.peak, 1, "summary RPCs still execute serially")
	require.LessOrEqual(t, f.peak, 4, "summary exceeds its per-request RPC concurrency limit")
}

func TestAdminSummaryPartialFailure(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	f := &summaryRPCFixture{failMethod: "AggregateUsage"}
	rec := requestSummary(summaryFixtureServer(f))
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, jsonx.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, true, body.Data["partial"])
	require.Nil(t, body.Data["cost_analysis"], "failed revenue must not become zero")
	require.Nil(t, body.Data["totals"].(map[string]any)["request_count"])
	require.NotEmpty(t, body.Data["recent_users"], "healthy data must remain available")
}

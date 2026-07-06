package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redis/go-redis/v9"

	"micro-one-api/internal/pkg/metrics"
	relaybiz "micro-one-api/internal/relay/biz"
	"micro-one-api/internal/relay/passthrough"
	relayprovider "micro-one-api/internal/relay/provider"
	"micro-one-api/internal/relay/stresstest"
)

// newMiniRedis builds a hermetic Redis double and a *redis.Client for it. The
// client is real so EVAL/TTL/SETNX semantics match production; the server
// layer's openAIWSStickyStore and subscriptionSessionWindowStore are wired to
// it so stress tests exercise the actual cross-replica code paths.
func newMiniRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.NewMiniRedis()
	if err := mr.Start(); err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, rdb
}

type stressWriter struct{ t *testing.T }

func (l stressWriter) Write(p []byte) (int, error) {
	l.t.Log(string(p))
	return len(p), nil
}

// stressSubscriptionServer wires a fully Redis-backed HTTPServer for a stress
// scenario: hybrid adaptor on, session stickiness on, and the openAIWSSticky
// store + runtime blocker + account-concurrency + RPM limiters all backed by
// the shared miniredis. It returns the server plus a channel-client selector
// whose accounts list models the account pool (served sequentially so each
// failover attempt sees a distinct sibling).
func stressSubscriptionServer(t *testing.T, mr *miniredis.Miniredis, rdb *redis.Client, accounts []*relaybiz.SubscriptionAccount) (*HTTPServer, *sequencingAccountSelector) {
	t.Helper()
	selector := &sequencingAccountSelector{accounts: accounts}
	relayUsecase := relaybiz.NewRelayUsecase(stressIdentity{}, selector, nil, nil)
	httpServer := NewHTTPServer(nil, nil, nil, nil, relayUsecase)
	httpServer.SetHybridAdaptorEnabled(true)
	httpServer.wsPoolCfg.failoverMaxSwitches = len(accounts) // allow full pool failover
	httpServer.SetOpenAIWSStickyStore(rdb)
	httpServer.SetSubscriptionSessionStickyEnabled(true)
	// Redis-backed runtime blocker + limiters (mirrors wire_gen.go).
	httpServer.SetRuntimeBlocker(relaybiz.NewRedisRuntimeBlocker(rdb))
	httpServer.SetAccountConcurrencyLimiter(relaybiz.NewRedisAccountConcurrencyLimiter(rdb))
	httpServer.SetAccountRPMLimiter(relaybiz.NewRedisAccountRPMLimiter(rdb))
	return httpServer, selector
}

type stressIdentity struct{}

func (stressIdentity) GetAuthSnapshot(context.Context, string) (*relaybiz.AuthSnapshot, error) {
	return &relaybiz.AuthSnapshot{UserID: 42, Group: "default"}, nil
}

// sequencingAccountSelector serves accounts sequentially (accounts[0],
// accounts[1], ...) like the existing adaptorFailoverChannelClient, but never
// errors once exhausted: it keeps returning the last account. Failover loops
// that exclude an account via the `failed` map will still skip it (the caller
// passes the failed set and SelectSubscriptionFailover excludes them before
// calling SelectSubscriptionAccount), so a saturated run cannot accidentally
// re-pick the failed account and spin.
type sequencingAccountSelector struct {
	mu       sync.Mutex
	accounts []*relaybiz.SubscriptionAccount
	calls    int
}

func (c *sequencingAccountSelector) SelectChannel(context.Context, string, string, bool) (*relaybiz.Channel, error) {
	return nil, fmt.Errorf("no api-key channel")
}
func (c *sequencingAccountSelector) RecordChannelHealth(context.Context, int64, bool, string, int64) error {
	return nil
}
func (c *sequencingAccountSelector) SelectSubscriptionAccount(_ context.Context, _, _, _ string, _ bool) (*relaybiz.SubscriptionAccount, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.accounts) == 0 {
		return nil, fmt.Errorf("no subscription account")
	}
	idx := c.calls
	if idx >= len(c.accounts) {
		idx = len(c.accounts) - 1
	}
	c.calls++
	cp := *c.accounts[idx]
	return &cp, nil
}
func (c *sequencingAccountSelector) GetSubscriptionAccountByID(_ context.Context, accountID int64) (*relaybiz.SubscriptionAccount, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, a := range c.accounts {
		if a != nil && a.ID == accountID {
			cp := *a
			return &cp, nil
		}
	}
	return nil, nil
}

// stressAccount builds a schedulable subscription account for stress runs.
func stressAccount(id int64, token string, concurrency int32, rpm int32) *relaybiz.SubscriptionAccount {
	return &relaybiz.SubscriptionAccount{
		ID: id, Name: fmt.Sprintf("acct-%d", id), Platform: "codex", AccountType: "oauth", Status: 1,
		BaseURL: "https://example.invalid", Group: "default", Models: []string{"gpt-5"},
		Priority: int64(100 - id), AccessToken: token, AccountID: fmt.Sprintf("acct-%d", id),
		Concurrency: concurrency, RPMLimit: rpm,
	}
}

func stressCodexPlan(accountID int64, concurrency int32) *relaybiz.RelayPlan {
	return &relaybiz.RelayPlan{
		Auth:          &relaybiz.AuthSnapshot{UserID: 42, Group: "default"},
		Channel:       &relaybiz.Channel{ID: accountID, Type: relayprovider.ChannelTypeCodexOAuth, BaseURL: "https://example.invalid", Group: "default"},
		Account:       stressAccount(accountID, fmt.Sprintf("tok-%d", accountID), concurrency, 0),
		ResolvedModel: "gpt-5",
	}
}

// okResponse is the standard successful upstream response body.
const okResponse = `{"id":"resp_ok","object":"response","model":"gpt-5","status":"completed","output":[{"type":"message","id":"m","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`

const stressBody = `{"model":"gpt-5","messages":[{"role":"user","content":"hi"}],"stream":false}`

// TestStress_SessionHashSticky_CrossReplicaBindAndReuse proves the roadmap
// §阶段 3 "session_hash sticky" scenario under load: a session bound by
// replica A is reused by replica B through the shared Redis store, and the
// sticky hit rate is high under a single-session, many-turn workload.
func TestStress_SessionHashSticky_CrossReplicaBindAndReuse(t *testing.T) {
	mr, rdb := newMiniRedis(t)
	accounts := []*relaybiz.SubscriptionAccount{stressAccount(1, "tok-1", 0, 0)}
	serverA, _ := stressSubscriptionServer(t, mr, rdb, accounts)
	serverB, _ := stressSubscriptionServer(t, mr, rdb, accounts) // shares the same Redis
	serverA.SetOAuthHTTPClient(stickyOKClient())
	serverB.SetOAuthHTTPClient(stickyOKClient())

	const turns = 40
	rec := stresstest.NewRecorder(turns)
	hitsBefore := testutil.ToFloat64(metrics.RelaySubscriptionStickyTotal.WithLabelValues("hit", "codex"))
	rebindsBefore := testutil.ToFloat64(metrics.RelaySubscriptionStickyTotal.WithLabelValues("rebind", "codex"))

	const sessionHash = "stress-session-1"
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < turns; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			srv := serverA
			if i%2 == 1 {
				srv = serverB // alternate replicas
			}
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(stressBody))
			req.Header.Set("Authorization", "Bearer test-token")
			rw := httptest.NewRecorder()
			begin := time.Now()
			srv.handleChatCompletionsViaAdaptor(rw, req, stressCodexPlan(1, 0), "gpt-5", []byte(stressBody), sessionHash)
			dur := time.Since(begin)
			a := stresstest.Attempt{Duration: dur}
			if rw.Code == http.StatusOK {
				a.Succeeded = true
			}
			rec.Record(a)
		}(i)
	}
	close(start)
	wg.Wait()

	rp := rec.Report(stresstest.ConcurrencyCap{Limit: 0})
	rp.Print(stressWriter{t})

	hits := testutil.ToFloat64(metrics.RelaySubscriptionStickyTotal.WithLabelValues("hit", "codex")) - hitsBefore
	rebinds := testutil.ToFloat64(metrics.RelaySubscriptionStickyTotal.WithLabelValues("rebind", "codex")) - rebindsBefore
	// First turn rebinds (binds), the rest should be hits against the shared store.
	if hits <= 0 {
		t.Fatalf("expected cross-replica sticky hits via Redis, got hits delta=%v rebinds delta=%v", hits, rebinds)
	}
	if rp.SuccessRate() != 1.0 {
		t.Fatalf("all turns must succeed, success_rate=%.4f", rp.SuccessRate())
	}
}

// TestStress_PreviousResponseIDSticky_CrossReplicaLookup proves the roadmap
// §阶段 3 "previous_response_id route sticky" scenario: a response created by
// replica A is looked up by replica B via the shared Redis-backed sticky store.
func TestStress_PreviousResponseIDSticky_CrossReplicaLookup(t *testing.T) {
	mr, rdb := newMiniRedis(t)
	accounts := []*relaybiz.SubscriptionAccount{stressAccount(1, "tok-1", 0, 0)}
	serverA, _ := stressSubscriptionServer(t, mr, rdb, accounts)
	serverB, _ := stressSubscriptionServer(t, mr, rdb, accounts)

	// Bind a response id -> account on replica A's sticky store (simulating a
	// served create-response). Replica B must observe it via Redis.
	const responseID = "resp_stress_1"
	const group = "default"
	serverA.wsSticky.BindResponseChannel(context.Background(), group, responseID, 1, time.Hour)

	got := serverB.wsSticky.LookupResponseChannel(context.Background(), group, responseID)
	if got != 1 {
		t.Fatalf("replica B lookup of responseID bound by A = %d, want 1 (cross-replica Redis sticky)", got)
	}

	// Concurrent lookups from many "replicas" (here: many goroutines on B) all
	// resolve to the same account — proving the hot-cache miss path falls back
	// to Redis and populates correctly under load.
	var wg sync.WaitGroup
	const readers = 32
	seen := atomic.Int64{}
	start := make(chan struct{})
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if serverB.wsSticky.LookupResponseChannel(context.Background(), group, responseID) == 1 {
				seen.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if got := seen.Load(); got != int64(readers) {
		t.Fatalf("cross-replica response sticky lookups: %d/%d readers resolved the bound account", got, readers)
	}
}

// tokenRoutingClient is an upstream client whose response is determined by the
// Authorization token (i.e. the account). It is deterministic across
// goroutines so a stress run can assert exactly how many failovers occur.
type tokenRoutingClient struct {
	// behavior maps "Bearer <token>" -> func() (status, body, retryAfter)
	behavior map[string]func() (int, string, string)
}

func (c *tokenRoutingClient) RoundTrip(req *http.Request) (*http.Response, error) {
	tok := req.Header.Get("Authorization")
	fn, ok := c.behavior[tok]
	if !ok {
		// Unknown token -> success (covers siblings not explicitly configured).
		return newJSONResponse(okResponse), nil
	}
	status, body, retryAfter := fn()
	resp := newStatusResponse(status, body)
	if retryAfter != "" {
		resp.Header.Set("Retry-After", retryAfter)
	}
	return resp, nil
}

// alwaysStatus returns a behavior function that always replies with the given
// status (and optional Retry-After).
func alwaysStatus(status int, retryAfter string) func() (int, string, string) {
	return func() (int, string, string) {
		return status, `{"error":{"message":"fail"}}`, retryAfter
	}
}

// failNTimes returns a behavior function that fails the first n calls with the
// given status, then succeeds.
func failNTimes(status int, n int) func() (int, string, string) {
	var calls atomic.Int64
	return func() (int, string, string) {
		if calls.Add(1) <= int64(n) {
			return status, `{"error":{"message":"fail"}}`, ""
		}
		return http.StatusOK, okResponse, ""
	}
}

// successBehavior always returns 200.
func successBehavior() func() (int, string, string) {
	return func() (int, string, string) { return http.StatusOK, okResponse, "" }
}

// TestStress_Failover_429_UnderLoad drives the 429 failover path under
// concurrency: the first account always 429s, siblings succeed, and the
// request still returns 200 with a failover switch recorded per request.
func TestStress_Failover_429_UnderLoad(t *testing.T) {
	mr, rdb := newMiniRedis(t)
	// Account 1 always 429s; accounts 2..4 succeed. Failover must pick a sibling.
	accounts := []*relaybiz.SubscriptionAccount{
		stressAccount(1, "tok-1", 0, 0),
		stressAccount(2, "tok-2", 0, 0),
		stressAccount(3, "tok-3", 0, 0),
		stressAccount(4, "tok-4", 0, 0),
	}
	server, _ := stressSubscriptionServer(t, mr, rdb, accounts)
	// Short 429 block so cooled accounts recover promptly and don't starve the pool.
	server.SetRuntimeBlockDurations(1*time.Second, 2*time.Minute, 2*time.Minute, 30*time.Second)
	server.SetOAuthHTTPClient(&http.Client{Transport: &tokenRoutingClient{
		behavior: map[string]func() (int, string, string){
			"Bearer tok-1": alwaysStatus(http.StatusTooManyRequests, "1"),
			"Bearer tok-2": successBehavior(),
			"Bearer tok-3": successBehavior(),
			"Bearer tok-4": successBehavior(),
		},
	}})

	const reqs = 30
	rec := stresstest.NewRecorder(reqs)
	switchedBefore := testutil.ToFloat64(metrics.RelaySubscriptionFailoverTotal.WithLabelValues("429", "switched"))
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < reqs; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(stressBody))
			req.Header.Set("Authorization", "Bearer test-token")
			rw := httptest.NewRecorder()
			begin := time.Now()
			server.handleChatCompletionsViaAdaptor(rw, req, stressCodexPlan(1, 0), "gpt-5", []byte(stressBody), "")
			dur := time.Since(begin)
			a := stresstest.Attempt{Duration: dur}
			if rw.Code == http.StatusOK {
				a.Succeeded = true
				a.FailoverReason = "429"
			} else {
				a.Failed = true
			}
			rec.Record(a)
		}()
	}
	close(start)
	wg.Wait()

	rp := rec.Report(stresstest.ConcurrencyCap{Limit: 0})
	rp.Print(stressWriter{t})
	switched := testutil.ToFloat64(metrics.RelaySubscriptionFailoverTotal.WithLabelValues("429", "switched")) - switchedBefore
	if switched != float64(reqs) {
		t.Fatalf("429 failover switched delta = %v, want %d", switched, reqs)
	}
	if rp.SuccessRate() != 1.0 {
		t.Fatalf("all requests must succeed via failover, success_rate=%.4f", rp.SuccessRate())
	}
	if rp.FailoverSwitched != reqs {
		t.Fatalf("report failover_switched = %d, want %d", rp.FailoverSwitched, reqs)
	}
}

// TestStress_Failover_5xx_Then529_BothRecordReasons exercises both 5xx and 529
// failover under load and asserts the per-reason metrics distinguish them.
func TestStress_Failover_5xx_Then529_BothRecordReasons(t *testing.T) {
	mr, rdb := newMiniRedis(t)
	accounts := []*relaybiz.SubscriptionAccount{
		stressAccount(1, "tok-1", 0, 0),
		stressAccount(2, "tok-2", 0, 0),
	}
	server, selector := stressSubscriptionServer(t, mr, rdb, accounts)
	// First account always 5xx; second always 529. There is no third sibling, so
	// each request: 5xx switch -> 529 exhausted (no sibling to switch to).
	server.SetOAuthHTTPClient(&http.Client{Transport: &tokenRoutingClient{
		behavior: map[string]func() (int, string, string){
			"Bearer tok-1": alwaysStatus(http.StatusBadGateway, ""),
			"Bearer tok-2": alwaysStatus(passthrough.StatusOverloaded, ""),
		},
	}})

	switched5xxBefore := testutil.ToFloat64(metrics.RelaySubscriptionFailoverTotal.WithLabelValues("5xx", "switched"))
	switched529Before := testutil.ToFloat64(metrics.RelaySubscriptionFailoverTotal.WithLabelValues("529", "switched"))
	exhausted529Before := testutil.ToFloat64(metrics.RelaySubscriptionFailoverTotal.WithLabelValues("529", "exhausted"))

	const reqs = 20
	rec := stresstest.NewRecorder(reqs)
	// Sequential: the shared Redis runtime blocker means a 5xx/529 block written
	// by one goroutine is visible to all others, so under concurrency later
	// requests would find both accounts blocked and emit "exhausted" instead of
	// "switched". Sequential execution keeps the per-request failover chain
	// deterministic: account1 5xx -> switch to account2 -> 529 -> exhausted.
	// Blocks are cleared between requests so each starts from a clean slate.
	for i := 0; i < reqs; i++ {
		// Clear runtime blocks so each request re-selects account 1 first.
		_ = server.runtimeBlocker.Clear(context.Background(), 1)
		_ = server.runtimeBlocker.Clear(context.Background(), 2)
		// Reset the selector so failover picks account 2 (not a cycled index).
		selector.mu.Lock()
		selector.calls = 0
		selector.mu.Unlock()
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(stressBody))
		req.Header.Set("Authorization", "Bearer test-token")
		rw := httptest.NewRecorder()
		begin := time.Now()
		server.handleChatCompletionsViaAdaptor(rw, req, stressCodexPlan(1, 0), "gpt-5", []byte(stressBody), "")
		dur := time.Since(begin)
		a := stresstest.Attempt{Duration: dur}
		if rw.Code == http.StatusOK {
			a.Succeeded = true
		} else {
			a.Failed = true
		}
		rec.Record(a)
	}

	rp := rec.Report(stresstest.ConcurrencyCap{Limit: 0})
	rp.Print(stressWriter{t})
	switched5xx := testutil.ToFloat64(metrics.RelaySubscriptionFailoverTotal.WithLabelValues("5xx", "switched")) - switched5xxBefore
	switched529 := testutil.ToFloat64(metrics.RelaySubscriptionFailoverTotal.WithLabelValues("529", "switched")) - switched529Before
	exhausted529 := testutil.ToFloat64(metrics.RelaySubscriptionFailoverTotal.WithLabelValues("529", "exhausted")) - exhausted529Before
	if switched5xx != float64(reqs) {
		t.Fatalf("5xx switched delta = %v, want %d", switched5xx, reqs)
	}
	// The 529 is the terminal failure on account 2 (no third sibling), so it is
	// "exhausted", not "switched".
	if switched529 != 0 {
		t.Fatalf("529 switched delta = %v, want 0 (529 is terminal, no sibling)", switched529)
	}
	if exhausted529 != float64(reqs) {
		t.Fatalf("529 exhausted delta = %v, want %d", exhausted529, reqs)
	}
	if rp.SuccessRate() != 0 {
		t.Fatalf("no sibling succeeds, expected 0%% success, got %.4f", rp.SuccessRate())
	}
}

// TestStress_ConcurrencyFailover_UnderLoad saturates account 1's concurrency
// (cap 1) with a long-held slot, then drives many requests that must fail over
// to sibling accounts without cooling 1 down. Requests run sequentially to keep
// the concurrency invariant crisp (the cap is account 1; siblings are
// unlimited) while still exercising the failover path repeatedly.
func TestStress_ConcurrencyFailover_UnderLoad(t *testing.T) {
	mr, rdb := newMiniRedis(t)
	accounts := []*relaybiz.SubscriptionAccount{
		stressAccount(1, "tok-1", 1, 0), // cap 1
		stressAccount(2, "tok-2", 0, 0),
		stressAccount(3, "tok-3", 0, 0),
	}
	server, _ := stressSubscriptionServer(t, mr, rdb, accounts)
	server.SetOAuthHTTPClient(stickyOKClient())

	// Hold account 1's only slot for the whole run so every request that lands
	// on 1 must fail over.
	release, ok := server.accountConcurrency.TryAcquire(context.Background(), 1, 1)
	if !ok {
		t.Fatal("precondition: hold account 1 slot")
	}
	defer release()

	const reqs = 30
	rec := stresstest.NewRecorder(reqs)
	switchedBefore := testutil.ToFloat64(metrics.RelaySubscriptionFailoverTotal.WithLabelValues("concurrency", "switched"))
	blockBefore := testutil.ToFloat64(metrics.RelayRuntimeBlocksTotal.WithLabelValues("concurrency"))
	// Sequential: avoids the selector cycling past the held slot before the
	// concurrency check (which would make the assertion brittle).
	for i := 0; i < reqs; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(stressBody))
		req.Header.Set("Authorization", "Bearer test-token")
		rw := httptest.NewRecorder()
		begin := time.Now()
		// Plan points at account 1 (saturated); failover must move to 2/3.
		server.handleChatCompletionsViaAdaptor(rw, req, stressCodexPlan(1, 1), "gpt-5", []byte(stressBody), "")
		dur := time.Since(begin)
		a := stresstest.Attempt{Duration: dur}
		if rw.Code == http.StatusOK {
			a.Succeeded = true
			a.FailoverReason = "concurrency"
		} else {
			a.Failed = true
		}
		rec.Record(a)
	}

	rp := rec.Report(stresstest.ConcurrencyCap{Limit: 1, Account: 1})
	rp.Print(stressWriter{t})
	switched := testutil.ToFloat64(metrics.RelaySubscriptionFailoverTotal.WithLabelValues("concurrency", "switched")) - switchedBefore
	blocked := testutil.ToFloat64(metrics.RelayRuntimeBlocksTotal.WithLabelValues("concurrency")) - blockBefore
	if switched != float64(reqs) {
		t.Fatalf("concurrency failover switched delta = %v, want %d", switched, reqs)
	}
	if blocked != 0 {
		t.Fatalf("a concurrency-full account must NOT be cooled down, block delta = %v", blocked)
	}
	if rp.SuccessRate() != 1.0 {
		t.Fatalf("all requests must succeed via concurrency failover, success_rate=%.4f", rp.SuccessRate())
	}
}

// TestStress_StickyAccountUnavailable_FailoverRebinds is the roadmap §阶段 3
// acceptance: "sticky 账号不可用时可以 failover 并重新绑定可用账号". The
// sticky-bound account is runtime-blocked (so it's unschedulable), the request
// fails over to a sibling, and the session is rebound to the serving sibling.
func TestStress_StickyAccountUnavailable_FailoverRebinds(t *testing.T) {
	mr, rdb := newMiniRedis(t)
	accounts := []*relaybiz.SubscriptionAccount{
		stressAccount(1, "tok-1", 0, 0),
		stressAccount(2, "tok-2", 0, 0),
	}
	server, selector := stressSubscriptionServer(t, mr, rdb, accounts)
	server.SetOAuthHTTPClient(stickyOKClient())

	const sessionHash = "stress-rebind-session"
	// Bind the session to account 1, then block 1 at runtime so it is not
	// schedulable.
	// Bind the session to account 1, then make account 1 fail at runtime (429)
	// so the request fails over to account 2 and rebinds the session. A runtime
	// block alone is not enough: the block is a selection-time filter consulted
	// by SelectSubscriptionFailover, while the initial plan is pre-built and
	// executed directly. An upstream 429 is the real "sticky account
	// unavailable" trigger: it fails the attempt, cools the account, and the
	// failover loop selects + succeeds on a sibling, then rebinds.
	server.wsSticky.BindSessionChannel(context.Background(), "default", sessionHash, 1, time.Hour)
	// Reset selector so failover picks account 2.
	selector.mu.Lock()
	selector.calls = 0
	selector.mu.Unlock()
	server.SetOAuthHTTPClient(&http.Client{Transport: &tokenRoutingClient{
		behavior: map[string]func() (int, string, string){
			"Bearer tok-1": alwaysStatus(http.StatusTooManyRequests, "1"),
			"Bearer tok-2": successBehavior(),
		},
	}})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(stressBody))
	req.Header.Set("Authorization", "Bearer test-token")
	rw := httptest.NewRecorder()
	rebindBefore := testutil.ToFloat64(metrics.RelaySubscriptionStickyTotal.WithLabelValues("rebind", "codex"))

	server.handleChatCompletionsViaAdaptor(rw, req, stressCodexPlan(1, 0), "gpt-5", []byte(stressBody), sessionHash)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rw.Code, rw.Body.String())
	}
	got := server.wsSticky.LookupSessionChannel(context.Background(), "default", sessionHash)
	if got != 2 {
		t.Fatalf("session must be rebound to the serving sibling, bound = %d, want 2", got)
	}
	rebinds := testutil.ToFloat64(metrics.RelaySubscriptionStickyTotal.WithLabelValues("rebind", "codex")) - rebindBefore
	if rebinds != 1 {
		t.Fatalf("rebind delta = %v, want 1", rebinds)
	}
}

// TestStress_SessionWindowFailover_UnderLoad drives the session-window
// failover path: account 1's session window is exhausted, requests fail over
// to a sibling without cooling 1 down.
func TestStress_SessionWindowFailover_UnderLoad(t *testing.T) {
	mr, rdb := newMiniRedis(t)
	accounts := []*relaybiz.SubscriptionAccount{
		stressAccount(1, "tok-1", 0, 0),
		stressAccount(2, "tok-2", 0, 0),
	}
	// Give account 1 a tiny session-window budget so the first request exhausts it.
	accounts[0].SessionWindowLimitUSD = 0.01
	server, selector := stressSubscriptionServer(t, mr, rdb, accounts)
	server.SetOAuthHTTPClient(stickyOKClient())

	const sessionHash = "stress-session-window"
	// Pre-charge account 1's session window past its limit.
	server.sessionWindow.RecordUsage(context.Background(), "default", sessionHash, 1, "res-1", 1.0, time.Hour)

	// Build a plan whose account carries the session-window limit (the default
	// stressCodexPlan sets SessionWindowLimitUSD=0, which disables the check).
	plan := stressCodexPlan(1, 0)
	plan.Account.SessionWindowLimitUSD = 0.01

	const reqs = 20
	rec := stresstest.NewRecorder(reqs)
	switchedBefore := testutil.ToFloat64(metrics.RelaySubscriptionFailoverTotal.WithLabelValues("session_window", "switched"))
	blockBefore := testutil.ToFloat64(metrics.RelayRuntimeBlocksTotal.WithLabelValues("session_window"))
	// Sequential: the session window is per-account+session, so concurrent
	// requests would all hit the pre-charged account and cascade; sequential
	// keeps the invariant that each request fails over from account 1.
	for i := 0; i < reqs; i++ {
		// Clear runtime blocks so failover selection can re-pick account 2.
		_ = server.runtimeBlocker.Clear(context.Background(), 1)
		_ = server.runtimeBlocker.Clear(context.Background(), 2)
		selector.mu.Lock()
		selector.calls = 0
		selector.mu.Unlock()
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(stressBody))
		req.Header.Set("Authorization", "Bearer test-token")
		rw := httptest.NewRecorder()
		begin := time.Now()
		server.handleChatCompletionsViaAdaptor(rw, req, plan, "gpt-5", []byte(stressBody), sessionHash)
		dur := time.Since(begin)
		a := stresstest.Attempt{Duration: dur}
		if rw.Code == http.StatusOK {
			a.Succeeded = true
			a.FailoverReason = "session_window"
		} else {
			a.Failed = true
		}
		rec.Record(a)
	}

	rp := rec.Report(stresstest.ConcurrencyCap{Limit: 0})
	rp.Print(stressWriter{t})
	switched := testutil.ToFloat64(metrics.RelaySubscriptionFailoverTotal.WithLabelValues("session_window", "switched")) - switchedBefore
	blocked := testutil.ToFloat64(metrics.RelayRuntimeBlocksTotal.WithLabelValues("session_window")) - blockBefore
	if switched != float64(reqs) {
		t.Fatalf("session_window failover switched delta = %v, want %d", switched, reqs)
	}
	if blocked != 0 {
		t.Fatalf("a session-window-full account must NOT be cooled down, block delta = %v", blocked)
	}
	if rp.SuccessRate() != 1.0 {
		t.Fatalf("all requests must succeed via session-window failover, success_rate=%.4f", rp.SuccessRate())
	}
}

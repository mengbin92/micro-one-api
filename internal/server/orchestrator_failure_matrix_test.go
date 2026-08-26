package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
)

type matrixChannelClient struct {
	channel     *relaybiz.Channel
	selectErr   error
	healthErr   error
	healthCalls int
	healthOK    []bool
	selectCalls int
	noCandidate bool
}

func (c *matrixChannelClient) SelectChannel(context.Context, string, string, bool) (*relaybiz.Channel, error) {
	c.selectCalls++
	if c.noCandidate {
		return nil, c.selectErr
	}
	return c.channel, c.selectErr
}

func (c *matrixChannelClient) SelectChannelExcluding(ctx context.Context, group, model string, _ map[int64]bool) (*relaybiz.Channel, error) {
	return c.SelectChannel(ctx, group, model, true)
}

func (c *matrixChannelClient) RecordChannelHealth(_ context.Context, _ int64, success bool, _ string, _ int64) error {
	c.healthCalls++
	c.healthOK = append(c.healthOK, success)
	return c.healthErr
}

func (*matrixChannelClient) RecordSubscriptionAccountHealth(context.Context, int64, bool) error {
	return nil
}

type matrixForwarder struct {
	response *relaybiz.ForwardResponse
	calls    int
}

func (f *matrixForwarder) Forward(ctx context.Context, _ *relaybiz.RelayPlan, _ relaybiz.ExecutorRequest) (*relaybiz.ForwardResponse, error) {
	f.calls++
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return f.response, nil
}

type matrixLifecycleHooks struct {
	reserveErr error
	commitErr  error
	reserve    int
	commit     int
	release    int
	log        int
	logSinkErr error
}

func (h *matrixLifecycleHooks) ReserveQuota(context.Context, *relaybiz.RelayPlan, *RelayRequest, relaybiz.CanonicalUsage) (*Reservation, error) {
	h.reserve++
	if h.reserveErr != nil {
		return nil, h.reserveErr
	}
	return &Reservation{ID: "matrix-reservation"}, nil
}

func (h *matrixLifecycleHooks) CommitQuota(context.Context, *relaybiz.RelayPlan, *RelayRequest, *Reservation, relaybiz.CanonicalUsage, bool, time.Duration) error {
	h.commit++
	return h.commitErr
}

func (h *matrixLifecycleHooks) ReleaseQuota(context.Context, *Reservation, string) error {
	h.release++
	return nil
}

func (h *matrixLifecycleHooks) LogUsage(context.Context, *relaybiz.RelayPlan, *RelayRequest, relaybiz.CanonicalUsage, time.Duration, bool) {
	h.log++
	// Usage logging is best-effort by contract. A failed sink must not cause a
	// second upstream attempt or a second billing commit.
	if h.logSinkErr != nil {
		return
	}
}

func matrixUsecase(channel relaybiz.ChannelClient, retryPolicy *relaybiz.RetryPolicy) *relaybiz.RelayUsecase {
	if retryPolicy == nil {
		retryPolicy = &relaybiz.RetryPolicy{MaxAttempts: 1}
	}
	return relaybiz.NewRelayUsecase(orchestratorIdentityClient{}, channel, nil, retryPolicy)
}

func matrixRequest() relaybiz.ExecutorRequest {
	return relaybiz.ExecutorRequest{
		Token:     "client-token",
		Model:     "gpt-4o-mini",
		Endpoint:  string(EndpointChatCompletions),
		Body:      []byte(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`),
		Headers:   map[string][]string{"Content-Type": {"application/json"}},
		RequestID: "matrix-request",
	}
}

func newMatrixExecutor(uc *relaybiz.RelayUsecase, hooks *matrixLifecycleHooks, forwarder *matrixForwarder) relaybiz.Executor {
	return NewRelayExecutorWithForwarder(
		uc,
		relayprovider.NewProviderFactory(time.Second),
		hooks,
		forwarder,
		nil,
	)
}

func TestRelayOrchestratorFailureMatrixQuotaReserveErrorStopsBeforeForward(t *testing.T) {
	channel := &matrixChannelClient{channel: &relaybiz.Channel{ID: 11}}
	hooks := &matrixLifecycleHooks{reserveErr: errors.New("quota unavailable")}
	forwarder := &matrixForwarder{}
	executor := newMatrixExecutor(matrixUsecase(channel, nil), hooks, forwarder)
	req := matrixRequest()

	result, err := executor.Execute(context.Background(), req)
	if err == nil || result.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("Execute() = result:%#v err:%v, want payment-required reserve failure", result, err)
	}
	if hooks.reserve != 1 || hooks.commit != 0 || hooks.release != 0 || hooks.log != 0 || forwarder.calls != 0 {
		t.Fatalf("lifecycle = reserve:%d commit:%d release:%d log:%d forward:%d", hooks.reserve, hooks.commit, hooks.release, hooks.log, forwarder.calls)
	}
}

func TestRelayOrchestratorFailureMatrixContextCancellationReleasesOnce(t *testing.T) {
	channel := &matrixChannelClient{channel: &relaybiz.Channel{ID: 11}}
	hooks := &matrixLifecycleHooks{}
	forwarder := &matrixForwarder{}
	executor := newMatrixExecutor(matrixUsecase(channel, nil), hooks, forwarder)
	req := matrixRequest()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := executor.Execute(cancelled, req)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute() error = %v, want context canceled", err)
	}
	if hooks.reserve != 1 || hooks.release != 1 || hooks.commit != 0 || hooks.log != 0 || forwarder.calls != 1 {
		t.Fatalf("lifecycle = reserve:%d release:%d commit:%d log:%d forward:%d", hooks.reserve, hooks.release, hooks.commit, hooks.log, forwarder.calls)
	}
}

func TestRelayOrchestratorFailureMatrixCommitErrorDoesNotRetryOrRelease(t *testing.T) {
	channel := &matrixChannelClient{channel: &relaybiz.Channel{ID: 11}}
	hooks := &matrixLifecycleHooks{commitErr: errors.New("dial tcp billing: connection refused")}
	forwarder := &matrixForwarder{response: &relaybiz.ForwardResponse{
		StatusCode: http.StatusOK,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body:       []byte(`{"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`),
		Usage:      &relaybiz.CanonicalUsage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
	}}
	executor := newMatrixExecutor(matrixUsecase(channel, &relaybiz.RetryPolicy{MaxAttempts: 3}), hooks, forwarder)
	req := matrixRequest()

	result, err := executor.Execute(context.Background(), req)
	if err == nil || result.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("Execute() = result:%#v err:%v, want commit failure", result, err)
	}
	if hooks.reserve != 1 || hooks.commit != 1 || hooks.release != 0 || hooks.log != 0 || forwarder.calls != 1 {
		t.Fatalf("lifecycle = reserve:%d commit:%d release:%d log:%d forward:%d", hooks.reserve, hooks.commit, hooks.release, hooks.log, forwarder.calls)
	}
	if len(channel.healthOK) != 1 || !channel.healthOK[0] {
		t.Fatalf("channel health = %+v, want one healthy upstream outcome", channel.healthOK)
	}
}

func TestRelayOrchestratorFailureMatrixNoCandidateStopsBeforeQuota(t *testing.T) {
	channel := &matrixChannelClient{noCandidate: true, selectErr: errors.New("no available channel")}
	hooks := &matrixLifecycleHooks{}
	forwarder := &matrixForwarder{}
	executor := newMatrixExecutor(matrixUsecase(channel, nil), hooks, forwarder)
	req := matrixRequest()

	result, err := executor.Execute(context.Background(), req)
	if err == nil || result.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("Execute() = result:%#v err:%v, want no-candidate failure", result, err)
	}
	if channel.selectCalls == 0 || hooks.reserve != 0 || hooks.commit != 0 || hooks.release != 0 || forwarder.calls != 0 {
		t.Fatalf("selection/lifecycle = select:%d reserve:%d commit:%d release:%d forward:%d", channel.selectCalls, hooks.reserve, hooks.commit, hooks.release, forwarder.calls)
	}
}

func TestRelayOrchestratorFailureMatrixHealthAndLogErrorsDoNotDuplicateBilling(t *testing.T) {
	channel := &matrixChannelClient{channel: &relaybiz.Channel{ID: 11}, healthErr: errors.New("health write failed")}
	hooks := &matrixLifecycleHooks{logSinkErr: errors.New("log write failed")}
	forwarder := &matrixForwarder{response: &relaybiz.ForwardResponse{
		StatusCode: http.StatusOK,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body:       []byte(`{"choices":[]}`),
	}}
	executor := newMatrixExecutor(matrixUsecase(channel, nil), hooks, forwarder)
	req := matrixRequest()

	result, err := executor.Execute(context.Background(), req)
	if err != nil || result.StatusCode != http.StatusOK {
		t.Fatalf("Execute() = result:%#v err:%v, want successful response", result, err)
	}
	if channel.healthCalls != 1 || hooks.reserve != 1 || hooks.commit != 1 || hooks.release != 0 || hooks.log != 1 || forwarder.calls != 1 {
		t.Fatalf("health/lifecycle = health:%d reserve:%d commit:%d release:%d log:%d forward:%d", channel.healthCalls, hooks.reserve, hooks.commit, hooks.release, hooks.log, forwarder.calls)
	}
	if !strings.Contains(string(result.Body), "choices") {
		t.Fatalf("response body = %q", result.Body)
	}
}

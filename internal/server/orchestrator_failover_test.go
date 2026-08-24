package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
)

type orchestratorFailoverChannelClient struct {
	first  *relaybiz.Channel
	second *relaybiz.Channel
}

func (c orchestratorFailoverChannelClient) SelectChannel(context.Context, string, string, bool) (*relaybiz.Channel, error) {
	return c.first, nil
}

func (c orchestratorFailoverChannelClient) SelectChannelExcluding(_ context.Context, _ string, _ string, excluded map[int64]bool) (*relaybiz.Channel, error) {
	if c.second != nil && excluded[c.first.ID] {
		return c.second, nil
	}
	return c.first, nil
}

func (orchestratorFailoverChannelClient) RecordChannelHealth(context.Context, int64, bool, string, int64) error {
	return nil
}

func (orchestratorFailoverChannelClient) RecordSubscriptionAccountHealth(context.Context, int64, bool) error {
	return nil
}

type orchestratorFailoverLifecycleHooks struct {
	reservedIDs           []int64
	requestIDs            []string
	reservationsByRequest map[string]*Reservation
	released              []string
	committed             []string
	logged                []int64
	releaseErr            error
}

func (h *orchestratorFailoverLifecycleHooks) ReserveQuota(_ context.Context, plan *relaybiz.RelayPlan, req *RelayRequest, _ relaybiz.CanonicalUsage) (*Reservation, error) {
	h.reservedIDs = append(h.reservedIDs, plan.Channel.ID)
	h.requestIDs = append(h.requestIDs, req.RequestID)
	if h.reservationsByRequest == nil {
		h.reservationsByRequest = make(map[string]*Reservation)
	}
	if reservation := h.reservationsByRequest[req.RequestID]; reservation != nil {
		return reservation, nil
	}
	reservation := &Reservation{ID: fmt.Sprintf("reservation-%d", len(h.reservationsByRequest)+1)}
	h.reservationsByRequest[req.RequestID] = reservation
	return reservation, nil
}

func (h *orchestratorFailoverLifecycleHooks) CommitQuota(_ context.Context, plan *relaybiz.RelayPlan, _ *RelayRequest, reservation *Reservation, _ relaybiz.CanonicalUsage, _ bool, _ time.Duration) error {
	h.committed = append(h.committed, reservation.ID)
	if plan == nil || plan.Channel == nil {
		return fmt.Errorf("commit plan is incomplete")
	}
	return nil
}

func (h *orchestratorFailoverLifecycleHooks) ReleaseQuota(_ context.Context, reservation *Reservation, _ string) error {
	if reservation != nil {
		h.released = append(h.released, reservation.ID)
	}
	return h.releaseErr
}

func (h *orchestratorFailoverLifecycleHooks) LogUsage(_ context.Context, plan *relaybiz.RelayPlan, _ *RelayRequest, _ relaybiz.CanonicalUsage, _ time.Duration, _ bool) {
	h.logged = append(h.logged, plan.Channel.ID)
}

func TestRelayOrchestratorFailoverReleasesFailedCandidateAndCommitsOnce(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")

	var firstCalls, secondCalls int
	firstUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		firstCalls++
		http.Error(w, `{"error":{"message":"temporary upstream failure"}}`, http.StatusBadGateway)
	}))
	defer firstUpstream.Close()
	secondUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		secondCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"usage":{"prompt_tokens":4,"completion_tokens":5,"total_tokens":9},"choices":[]}`))
	}))
	defer secondUpstream.Close()

	first := &relaybiz.Channel{ID: 11, Type: relayprovider.ChannelTypeOpenAI, BaseURL: firstUpstream.URL + "/v1", Key: "sk-first"}
	second := &relaybiz.Channel{ID: 22, Type: relayprovider.ChannelTypeOpenAI, BaseURL: secondUpstream.URL + "/v1", Key: "sk-second"}
	channelClient := orchestratorFailoverChannelClient{first: first, second: second}
	relayUsecase := relaybiz.NewRelayUsecase(
		orchestratorIdentityClient{},
		channelClient,
		nil,
		&relaybiz.RetryPolicy{
			MaxAttempts:     2,
			RetryableStatus: map[int]bool{http.StatusBadGateway: true},
		},
	)
	hooks := &orchestratorFailoverLifecycleHooks{releaseErr: errors.New("billing release unavailable")}
	orchestrator := NewRelayOrchestratorWithDependencies(
		relayUsecase,
		relayprovider.NewProviderFactory(time.Second),
		hooks,
		nil,
	)

	result, err := orchestrator.Execute(context.Background(), &RelayRequest{
		Token:     "client-token",
		Model:     "gpt-4o-mini",
		Endpoint:  EndpointChatCompletions,
		Body:      strings.NewReader(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`),
		Headers:   http.Header{"Authorization": []string{"Bearer client-token"}},
		RequestID: "request-failover",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.ChannelID != second.ID {
		t.Fatalf("final channel = %d, want %d", result.ChannelID, second.ID)
	}
	body, err := io.ReadAll(result.Response)
	if err != nil {
		t.Fatalf("read result body: %v", err)
	}
	if !strings.Contains(string(body), `"total_tokens":9`) {
		t.Fatalf("result body = %s", body)
	}
	if firstCalls != 1 || secondCalls != 1 {
		t.Fatalf("upstream calls = first:%d second:%d, want 1:1", firstCalls, secondCalls)
	}
	if !reflect.DeepEqual(hooks.reservedIDs, []int64{11, 22}) || !reflect.DeepEqual(hooks.released, []string{"reservation-1"}) || !reflect.DeepEqual(hooks.committed, []string{"reservation-2"}) || !reflect.DeepEqual(hooks.logged, []int64{22}) {
		t.Fatalf("lifecycle = reserved:%v released:%v committed:%v logged:%v", hooks.reservedIDs, hooks.released, hooks.committed, hooks.logged)
	}
	if len(hooks.requestIDs) != 2 || hooks.requestIDs[0] != "request-failover" || hooks.requestIDs[1] == "" || hooks.requestIDs[1] == hooks.requestIDs[0] {
		t.Fatalf("request IDs = %v, want original then a distinct retry ID", hooks.requestIDs)
	}
}
